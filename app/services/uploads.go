package services

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"goravel/app/facades"
	"goravel/app/models"
	"goravel/app/templates"
)

// UploadChunk is the most one request of an upload carries: small enough
// for a slow link to send inside a proxy's limits, Cloudflare's 100 MB body
// and Traefik's 60 s read.
const UploadChunk = 4 << 20

// UploadHeadroom is the free disk an upload has to leave.
const UploadHeadroom = 1 << 30

// UploadIdle is how long an upload waits for its next chunk, or for its
// restore, before it's deleted. Tests shorten it.
var UploadIdle = 30 * time.Minute

// DiskFree is the space free to Echowa on dir's filesystem. Tests swap it.
var DiskFree = func(dir string) (uint64, error) {
	usage, err := disk.Usage(dir)
	if err != nil {
		return 0, err
	}

	return usage.Free, nil
}

var (
	// ErrUploadGone means the upload was cancelled, replaced, expired or
	// taken by its restore.
	ErrUploadGone = errors.New("the upload isn't there any more")
	// ErrChunkCut is a chunk whose request ended before all of it came.
	ErrChunkCut = errors.New("the chunk didn't arrive whole")
	// ErrChunkTooBig is a chunk over UploadChunk.
	ErrChunkTooBig = fmt.Errorf("a chunk carries %d bytes at most", UploadChunk)
	// ErrUploadTooLong is a chunk past the size the upload started with.
	ErrUploadTooLong = errors.New("the chunk goes past the end of the file")
)

// UploadOffsetError is a chunk that doesn't start where the upload has got
// to. Received is where it has, for the browser to carry on from.
type UploadOffsetError struct {
	Received int64
}

func (e *UploadOffsetError) Error() string {
	return fmt.Sprintf("the upload has %d bytes so far", e.Received)
}

// Upload is a dump the browser sends in chunks, for a restore from a file
// (ADR 0034). It's kept in a file in UploadDir until a restore takes it, and
// nothing about it is stored: Echowa deletes uploads when it starts.
type Upload struct {
	ID    string
	AppID uint
	// Name is the file's, as the browser gave it.
	Name string
	Size int64

	path string

	mu       sync.Mutex
	received int64
	sha256   hash.Hash
	gone     bool
	// expire forgets the upload once it has waited UploadIdle.
	expire *time.Timer
}

// uploads are the uploads no restore has taken, by ID.
var uploads = struct {
	sync.Mutex
	byID map[string]*Upload
}{byID: map[string]*Upload{}}

// UploadDir is where uploads are kept, beside Echowa's database.
func UploadDir() string {
	config := facades.Config()
	database := config.GetString("database.connections." + config.GetString("database.default") + ".database")

	return filepath.Join(filepath.Dir(database), "uploads")
}

// StartUpload makes an empty upload of size bytes for the app, once the
// disk has room for it. It replaces the app's earlier upload, so an app has
// one at most.
func StartUpload(app *models.App, name string, size int64) (*Upload, error) {
	if size <= 0 {
		return nil, refuse("This file is empty.")
	}
	dir := UploadDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	uploads.Lock()
	var earlier, others []*Upload
	for _, u := range uploads.byID {
		if u.AppID == app.ID {
			earlier = append(earlier, u)
		} else {
			others = append(others, u)
		}
	}
	uploads.Unlock()
	for _, u := range earlier {
		u.forget()
	}
	// Other apps' uploads will take the rest of their size.
	var promised int64
	for _, u := range others {
		promised += u.Size - u.Received()
	}
	free, err := DiskFree(dir)
	if err != nil {
		return nil, fmt.Errorf("read the free disk: %w", err)
	}
	if uint64(size+promised)+UploadHeadroom > free {
		others := ""
		if promised > 0 {
			others = fmt.Sprintf(", and uploads to other apps still need %s of it", sizeText(promised))
		}
		return nil, refuse("The server has %s of disk free%s. This %s file needs that much and %s to spare.",
			sizeText(int64(free)), others, sizeText(size), sizeText(UploadHeadroom))
	}

	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	u := &Upload{ID: hex.EncodeToString(id), AppID: app.ID, Name: uploadName(name), Size: size, sha256: sha256.New()}
	u.path = filepath.Join(dir, u.ID)
	f, err := os.OpenFile(u.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	u.expire = time.AfterFunc(UploadIdle, u.forget)
	uploads.Lock()
	uploads.byID[u.ID] = u
	uploads.Unlock()

	return u, nil
}

// FindUpload is the app's upload with ID id that no restore has taken.
func FindUpload(appID uint, id string) (*Upload, bool) {
	uploads.Lock()
	defer uploads.Unlock()
	u, ok := uploads.byID[id]

	return u, ok && u.AppID == appID
}

// Write adds the chunk that starts at offset, and returns how much of the
// file has arrived. A chunk that ends early writes nothing, so the browser
// sends it again whole.
func (u *Upload) Write(offset int64, chunk io.Reader) (int64, error) {
	buf, err := io.ReadAll(io.LimitReader(chunk, UploadChunk+1))
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrChunkCut, err)
	}
	if len(buf) > UploadChunk {
		return 0, ErrChunkTooBig
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case u.gone:
		return 0, ErrUploadGone
	case offset != u.received:
		return u.received, &UploadOffsetError{Received: u.received}
	case u.received+int64(len(buf)) > u.Size:
		return u.received, ErrUploadTooLong
	}
	f, err := os.OpenFile(u.path, os.O_WRONLY, 0)
	if err != nil {
		return u.received, err
	}
	_, err = f.WriteAt(buf, offset)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return u.received, err
	}
	u.sha256.Write(buf)
	u.received += int64(len(buf))
	u.expire.Reset(UploadIdle)

	return u.received, nil
}

// Received is how much of the file has arrived.
func (u *Upload) Received() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.received
}

// Cancel deletes the upload, unless a restore has taken it.
func (u *Upload) Cancel() {
	u.forget()
}

func (u *Upload) forget() {
	uploads.Lock()
	ours := uploads.byID[u.ID] == u
	delete(uploads.byID, u.ID)
	uploads.Unlock()
	if !ours {
		return
	}
	u.mu.Lock()
	u.gone = true
	u.expire.Stop()
	u.mu.Unlock()
	_ = os.Remove(u.path)
}

// take hands the whole file to a restore, which deletes it when it ends,
// and returns its SHA-256. It's no longer an upload.
func (u *Upload) take() (string, error) {
	uploads.Lock()
	defer uploads.Unlock()
	if uploads.byID[u.ID] != u {
		return "", refuse("The upload isn't there any more. Choose the file again.")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.received != u.Size {
		return "", refuse("The file hasn't finished uploading.")
	}
	delete(uploads.byID, u.ID)
	u.gone = true
	u.expire.Stop()

	return hex.EncodeToString(u.sha256.Sum(nil)), nil
}

// head is the start of the file, as much as templates.ReadDumpHead reads,
// once it has arrived.
func (u *Upload) head() ([]byte, error) {
	want := min(u.Size, templates.DumpHeadSize)
	if u.Received() < want {
		return nil, refuse("The start of the file hasn't arrived yet.")
	}
	f, err := os.Open(u.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, refuse("The upload isn't there any more. Choose the file again.")
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, want)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, err
	}

	return head, nil
}

// CancelUploads deletes every upload no restore has taken. For tests.
func CancelUploads() {
	uploads.Lock()
	all := slices.Collect(maps.Values(uploads.byID))
	uploads.Unlock()
	for _, u := range all {
		u.forget()
	}
}

// RemoveUploads deletes the uploads an earlier Echowa left, when it
// starts: any file in UploadDir last written before this one started. It
// runs while requests are served, so this Echowa's uploads, and those its
// restores have taken, may be there already. Their restores, if any
// began, are interrupted.
func RemoveUploads() error {
	entries, err := os.ReadDir(UploadDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.ModTime().Before(backupStart) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(UploadDir(), entry.Name())); err != nil {
			return err
		}
	}

	return nil
}

// uploadName is the name the page shows for an upload: the browser's,
// without any folder.
func uploadName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	if name == "" || name == "." || name == "/" {
		return "upload"
	}

	return name
}

// sizeText is a size as the page shows one: "1.5 GiB", "120 MiB".
func sizeText(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value, unit := float64(n), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if value >= 10 || unit == 0 {
		return fmt.Sprintf("%.0f %s", value, units[unit])
	}

	return fmt.Sprintf("%.1f %s", value, units[unit])
}
