package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Client for tests. Its zero value is a healthy Swarm
// where every pull and deploy succeeds. Set the func fields to script
// failures. It's safe for concurrent use.
type Fake struct {
	// CheckErr is returned by Check.
	CheckErr error
	// Pull resolves a pull. Nil returns FakeDigest(repo) for any ref.
	Pull func(ref string, auth *RegistryAuth) (string, error)
	// Status overrides ServiceStatus, e.g. to return an error.
	Status func(name string) (ServiceStatus, error)
	// Remove is called before a service is removed. A non-nil error fails it.
	Remove func(name string) error
	// VolumeRemove is called before a volume is removed. A non-nil error
	// fails it.
	VolumeRemove func(name string) error
	// Login checks a registry login. Nil accepts any.
	Login func(auth RegistryAuth) error
	// Deploy is called before a deploy is recorded. A non-nil error fails it.
	Deploy func(spec ServiceSpec) error
	// Wait decides how the deploy of spec ends. Nil means it completes.
	Wait func(spec ServiceSpec) (UpdateResult, error)
	// Prune decides what PruneImages reports. Nil removes nothing.
	Prune func(opts PruneOptions) (PruneReport, error)
	// Stats answers ContainerStats. Nil reports no containers.
	Stats func() ([]ContainerStats, error)
	// HostInfo is what Host returns. An empty DataRoot means
	// /var/lib/docker, and no CPUs or memory means 4 cores and 8 GiB.
	HostInfo Host
	// OOMKilled is when each service's tasks were last killed for using
	// too much memory.
	OOMKilled map[string]time.Time
	// ExecFunc answers Exec. Nil means the service isn't running.
	ExecFunc func(service string, cmd []string) ([]byte, error)
	// ExecToFunc answers ExecTo. Nil means the service isn't running.
	ExecToFunc func(ctx context.Context, service string, cmd []string, out io.Writer) (int, error)
	// ExecStreamFunc answers ExecStream. Nil means the service isn't running.
	ExecStreamFunc func(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
	// ExecTTYFunc answers ExecTTY. Nil means the service isn't running.
	ExecTTYFunc func(ctx context.Context, service string, cmd []string, size TerminalSize) (Terminal, error)
	// Logs holds what StreamServiceLogs yields per service. With Follow, it
	// then waits for lines added with WriteLog until ctx ends.
	Logs map[string][]LogLine

	mu        sync.Mutex
	followers map[string][]chan LogLine
	openLogs  int
	services  map[string]fakeService
	removed   []string
	stopped   []string
	// volumes exist from a deploy that mounts them, or AddVolume, until
	// RemoveVolume.
	volumes        map[string]bool
	removedVolumes []string
	deploys        []ServiceSpec
	pulls          []string
	prunes         []PruneOptions
}

type fakeService struct {
	spec ServiceSpec
	// stopped means scaled to 0: the spec stays as deployed, as Swarm's
	// labels do.
	stopped  bool
	previous *ServiceSpec
	state    UpdateState
	message  string
	// tasks has one task per deploy, whatever the replicas, and one for a
	// rollback, which goes back to the previous deploy's generation.
	tasks []Task
}

// start replaces the running task with a new one of generation.
func (s *fakeService) start(generation uint64) {
	for i := range s.tasks {
		s.tasks[i].Running = false
	}
	s.tasks = append(s.tasks, Task{
		ID:         fmt.Sprintf("%s.%d", s.spec.Name, len(s.tasks)+1),
		Generation: generation,
		Image:      s.spec.Image,
		CreatedAt:  time.Now(),
		Running:    true,
	})
}

// running is the ID of the task that's running, if one is.
func (s *fakeService) running() string {
	for _, task := range s.tasks {
		if task.Running {
			return task.ID
		}
	}

	return ""
}

var _ Client = (*Fake)(nil)

// FakeDigest is the digest reference Fake.PullImage returns for ref by
// default: stable for the same input, different across inputs. A digest
// reference resolves to itself, as it does in Docker.
func FakeDigest(ref string) string {
	if strings.Contains(ref, "@sha256:") {
		return ref
	}
	sum := sha256.Sum256([]byte(ref))
	repo, _, _ := strings.Cut(ref, "@")
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}

	return repo + "@sha256:" + hex.EncodeToString(sum[:])
}

func (f *Fake) Check(context.Context) error {
	return f.CheckErr
}

func (f *Fake) PullImage(_ context.Context, ref string, auth *RegistryAuth) (string, error) {
	f.mu.Lock()
	f.pulls = append(f.pulls, ref)
	f.mu.Unlock()
	if f.Pull != nil {
		return f.Pull(ref, auth)
	}

	return FakeDigest(ref), nil
}

func (f *Fake) RegistryLogin(_ context.Context, auth RegistryAuth) error {
	if f.Login != nil {
		return f.Login(auth)
	}

	return nil
}

func (f *Fake) DeployService(_ context.Context, spec ServiceSpec) error {
	if f.Deploy != nil {
		if err := f.Deploy(spec); err != nil {
			return err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.services == nil {
		f.services = map[string]fakeService{}
	}
	svc := fakeService{spec: spec, state: UpdateUpdating}
	// Swarm creates a service with ForceUpdate 0, so its first deploy is
	// generation 0.
	generation := uint64(0)
	if current, ok := f.services[spec.Name]; ok {
		svc.previous = &current.spec
		svc.tasks = current.tasks
		generation = current.tasks[len(current.tasks)-1].Generation + 1
	}
	svc.start(generation)
	f.services[spec.Name] = svc
	f.deploys = append(f.deploys, spec)
	for _, v := range spec.Volumes {
		f.addVolume(v.Name)
	}

	return nil
}

// StopService stops the service's task at once and records the name. It
// leaves the spec and the generation as they were.
func (f *Fake) StopService(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	svc, ok := f.services[name]
	if !ok {
		return fmt.Errorf("%s: %w", name, ErrServiceNotFound)
	}
	if svc.stopped {
		return nil
	}
	for i := range svc.tasks {
		svc.tasks[i].Running = false
	}
	svc.stopped = true
	f.services[name] = svc
	f.stopped = append(f.stopped, name)

	return nil
}

// ServiceMatches compares spec with the one the service was last deployed
// or rolled back to.
func (f *Fake) ServiceMatches(_ context.Context, spec ServiceSpec) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	svc, ok := f.services[spec.Name]

	return ok && reflect.DeepEqual(svc.spec, spec), nil
}

func (f *Fake) ServiceStatus(_ context.Context, name string) (ServiceStatus, error) {
	if f.Status != nil {
		return f.Status(name)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	svc, ok := f.services[name]
	if !ok {
		return ServiceStatus{}, fmt.Errorf("%s: %w", name, ErrServiceNotFound)
	}

	desired := svc.spec.Replicas
	if svc.stopped {
		desired = 0
	}

	return ServiceStatus{
		Name:          name,
		Image:         svc.spec.Image,
		Desired:       desired,
		Running:       desired,
		UpdateState:   svc.state,
		UpdateMessage: svc.message,
		Limits:        svc.spec.Limits,
		OOMKilledAt:   f.oomKilledAt(name),
	}, nil
}

// oomKilledAt needs f.mu held.
func (f *Fake) oomKilledAt(name string) *time.Time {
	at, ok := f.OOMKilled[name]
	if !ok {
		return nil
	}

	return &at
}

// WaitForUpdate applies the outcome like Swarm would: a rollback restores
// the previous spec.
func (f *Fake) WaitForUpdate(_ context.Context, name string) (UpdateResult, error) {
	f.mu.Lock()
	svc, ok := f.services[name]
	f.mu.Unlock()
	if !ok {
		return UpdateResult{}, fmt.Errorf("%s: %w", name, ErrServiceNotFound)
	}

	result := UpdateResult{Outcome: OutcomeCompleted}
	if f.Wait != nil {
		var err error
		if result, err = f.Wait(svc.spec); err != nil {
			return UpdateResult{}, err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch result.Outcome {
	case OutcomeRolledBack:
		if svc.previous != nil {
			svc.spec = *svc.previous
			svc.start(svc.tasks[len(svc.tasks)-1].Generation - 1)
		}
		svc.state = UpdateRollbackCompleted
	case OutcomePaused:
		svc.state = UpdatePaused
	default:
		svc.state = UpdateCompleted
	}
	svc.message = result.Message
	f.services[name] = svc

	return result, nil
}

func (f *Fake) StreamServiceLogs(ctx context.Context, name string, opts LogOptions) (iter.Seq2[LogLine, error], error) {
	f.mu.Lock()
	_, ok := f.services[name]
	lines := slices.Clone(f.Logs[name])
	var live chan LogLine
	if ok && opts.Follow {
		live = make(chan LogLine, 100)
		if f.followers == nil {
			f.followers = map[string][]chan LogLine{}
		}
		f.followers[name] = append(f.followers[name], live)
	}
	if ok {
		f.openLogs++
	}
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, ErrServiceNotFound)
	}
	if opts.Tail > 0 && len(lines) > opts.Tail {
		lines = lines[len(lines)-opts.Tail:]
	}
	keep := func(line LogLine) bool { return opts.Since.IsZero() || !line.Time.Before(opts.Since) }

	return func(yield func(LogLine, error) bool) {
		defer f.closeLogs(name, live)
		for _, line := range lines {
			if keep(line) && !yield(line, nil) {
				return
			}
		}
		if live == nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case line := <-live:
				if keep(line) && !yield(line, nil) {
					return
				}
			}
		}
	}, nil
}

// WriteLog adds a line to the service's log, and sends it to streams
// following it. A line without a Task comes from the running task.
func (f *Fake) WriteLog(name string, line LogLine) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if line.Task == "" {
		svc := f.services[name]
		line.Task = svc.running()
	}
	if f.Logs == nil {
		f.Logs = map[string][]LogLine{}
	}
	f.Logs[name] = append(f.Logs[name], line)
	for _, live := range f.followers[name] {
		live <- line
	}
}

// OpenLogStreams is how many StreamServiceLogs iterators haven't finished:
// each stands for a Docker log request still open.
func (f *Fake) OpenLogStreams() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.openLogs
}

func (f *Fake) closeLogs(name string, live chan LogLine) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openLogs--
	if live != nil {
		f.followers[name] = slices.DeleteFunc(f.followers[name], func(c chan LogLine) bool { return c == live })
	}
}

func (f *Fake) ServiceTasks(_ context.Context, name string) ([]Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	svc, ok := f.services[name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, ErrServiceNotFound)
	}

	return slices.Clone(svc.tasks), nil
}

func (f *Fake) RemoveService(_ context.Context, name string) error {
	if f.Remove != nil {
		if err := f.Remove(name); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.services, name)
	f.removed = append(f.removed, name)

	return nil
}

func (f *Fake) ServicesWithRole(_ context.Context, role string) ([]ServiceRef, error) {
	if role == "" {
		return nil, errors.New("list services: no role given")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	refs := []ServiceRef{}
	for _, name := range slices.Sorted(maps.Keys(f.services)) {
		if spec := f.services[name].spec; spec.Role == role {
			refs = append(refs, ServiceRef{Name: name, App: spec.App})
		}
	}

	return refs, nil
}

// RemoveVolume fails with ErrVolumeInUse while a service mounts the
// volume. Fake removes services at once, so that only happens before
// RemoveService.
func (f *Fake) RemoveVolume(_ context.Context, name string) error {
	if f.VolumeRemove != nil {
		if err := f.VolumeRemove(name); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, svc := range f.services {
		if slices.ContainsFunc(svc.spec.Volumes, func(v Volume) bool { return v.Name == name }) {
			return fmt.Errorf("volume %s: %w", name, ErrVolumeInUse)
		}
	}
	delete(f.volumes, name)
	f.removedVolumes = append(f.removedVolumes, name)

	return nil
}

func (f *Fake) VolumeExists(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.volumes[name], nil
}

// AddVolume makes a volume exist, like one an earlier app left behind.
func (f *Fake) AddVolume(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addVolume(name)
}

func (f *Fake) addVolume(name string) {
	if f.volumes == nil {
		f.volumes = map[string]bool{}
	}
	f.volumes[name] = true
}

// RemovedVolumes returns every volume RemoveVolume removed, in order.
func (f *Fake) RemovedVolumes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.removedVolumes)
}

// Stopped returns every name passed to StopService that wasn't already
// stopped, in order.
func (f *Fake) Stopped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.stopped)
}

// Removed returns every name passed to RemoveService, in order.
func (f *Fake) Removed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.removed)
}

func (f *Fake) PruneImages(_ context.Context, opts PruneOptions) (PruneReport, error) {
	f.mu.Lock()
	f.prunes = append(f.prunes, opts)
	f.mu.Unlock()
	if f.Prune != nil {
		return f.Prune(opts)
	}

	return PruneReport{}, nil
}

func (f *Fake) ContainerStats(context.Context) ([]ContainerStats, error) {
	if f.Stats != nil {
		return f.Stats()
	}

	return nil, nil
}

func (f *Fake) Exec(_ context.Context, service string, cmd []string) ([]byte, error) {
	if f.ExecFunc != nil {
		return f.ExecFunc(service, cmd)
	}

	return nil, fmt.Errorf("%s: %w", service, ErrNotRunning)
}

func (f *Fake) ExecTo(ctx context.Context, service string, cmd []string, out io.Writer) (int, error) {
	if f.ExecToFunc != nil {
		return f.ExecToFunc(ctx, service, cmd, out)
	}

	return 0, fmt.Errorf("%s: %w", service, ErrNotRunning)
}

func (f *Fake) ExecStream(ctx context.Context, service string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if f.ExecStreamFunc != nil {
		return f.ExecStreamFunc(ctx, service, cmd, stdin, stdout, stderr)
	}

	return 0, fmt.Errorf("%s: %w", service, ErrNotRunning)
}

func (f *Fake) ExecTTY(ctx context.Context, service string, cmd []string, size TerminalSize) (Terminal, error) {
	if f.ExecTTYFunc != nil {
		return f.ExecTTYFunc(ctx, service, cmd, size)
	}

	return nil, fmt.Errorf("%s: %w", service, ErrNotRunning)
}

func (f *Fake) Host(context.Context) (Host, error) {
	host := f.HostInfo
	if host.DataRoot == "" {
		host.DataRoot = "/var/lib/docker"
	}
	if host.CPUs == 0 {
		host.CPUs = 4
	}
	if host.MemoryBytes == 0 {
		host.MemoryBytes = 8 << 30
	}

	return host, nil
}

// Deploys returns every spec passed to DeployService, in order.
func (f *Fake) Deploys() []ServiceSpec {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.deploys)
}

// Pulls returns every ref passed to PullImage, in order.
func (f *Fake) Pulls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.pulls)
}

// Prunes returns every PruneImages call's options, in order.
func (f *Fake) Prunes() []PruneOptions {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.prunes)
}

// FakeTerminal is a Terminal in memory, for ExecTTYFunc to return. Print
// is the command's output and Exit its end. Typed, Sizes and Closed say
// what was done with it. It's safe for concurrent use.
type FakeTerminal struct {
	ID string // what Container returns

	out  *io.PipeReader
	outW *io.PipeWriter

	mu     sync.Mutex
	typed  strings.Builder
	sizes  []TerminalSize
	code   int
	exited bool
	closed bool
}

func NewFakeTerminal(container string) *FakeTerminal {
	out, outW := io.Pipe()

	return &FakeTerminal{ID: container, out: out, outW: outW}
}

// Read returns what Print writes, then io.EOF after Exit, or an error after
// Close.
func (t *FakeTerminal) Read(p []byte) (int, error) {
	return t.out.Read(p)
}

func (t *FakeTerminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exited || t.closed {
		return 0, io.ErrClosedPipe
	}

	return t.typed.Write(p)
}

func (t *FakeTerminal) Resize(_ context.Context, size TerminalSize) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sizes = append(t.sizes, size)

	return nil
}

func (t *FakeTerminal) ExitCode(context.Context) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.exited {
		return 0, errors.New("the command is still running")
	}

	return t.code, nil
}

func (t *FakeTerminal) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	// After Exit, Read still gives io.EOF: a pipe keeps its first error.
	_ = t.outW.CloseWithError(errors.New("terminal closed"))

	return nil
}

func (t *FakeTerminal) Container() string {
	return t.ID
}

// Print writes s as the command's output. It returns once all of it has
// been read, or the terminal has ended.
func (t *FakeTerminal) Print(s string) {
	_, _ = io.WriteString(t.outW, s)
}

// Exit ends the command with code.
func (t *FakeTerminal) Exit(code int) {
	t.mu.Lock()
	t.code, t.exited = code, true
	t.mu.Unlock()
	_ = t.outW.Close()
}

// Typed is everything written to the terminal.
func (t *FakeTerminal) Typed() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.typed.String()
}

// Sizes lists every Resize, in order.
func (t *FakeTerminal) Sizes() []TerminalSize {
	t.mu.Lock()
	defer t.mu.Unlock()

	return slices.Clone(t.sizes)
}

// Closed reports whether Close was called.
func (t *FakeTerminal) Closed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.closed
}
