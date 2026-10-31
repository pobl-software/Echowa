package feature

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/goravel/framework/facades"

	"goravel/app/backups"
	"goravel/app/models"
	"goravel/app/services"
)

const redis810 = "Redis server v=8.10.3 sha=00000000:1 malloc=jemalloc-5.3.0 bits=64 build=2e99d4bb317442f\n"

// redisSnapshot stands in for redis-cli --rdb -'s output.
const redisSnapshot = "REDIS0015 a snapshot"

// scriptRedis is scriptRestore for a Redis server, which answers whether
// it can take a restore apart from its version: whether it was started
// with DEBUG allowed from inside its container.
func (s *BackupsTestSuite) scriptRedis(restorable bool, current string, code int) {
	s.scriptRestore(redis810, current, code, nil)
	s.docker.ExecFunc = func(service string, cmd []string) ([]byte, error) {
		if strings.Contains(strings.Join(cmd, " "), "enable-debug-command") {
			if restorable {
				return []byte("1\n"), nil
			}
			return []byte("0\n"), nil
		}
		return []byte(redis810), nil
	}
}

func (s *BackupsTestSuite) TestRedisBacksUpTheWholeServerAsAnRDBSnapshot() {
	cache := s.templateApp("cache", "redis")
	s.script(redis810, redisSnapshot, 0, "Transfer finished with success after 20 bytes\n", nil)
	b := signedIn(s.T(), &s.TestCase)
	_, page := b.visit(fmt.Sprintf("/apps/%d?tab=backups", cache.ID))
	s.Equal(true, page.Props["template"].(map[string]any)["backups"])
	section := page.Props["backups"].(map[string]any)
	s.Equal(true, section["whole_server"])
	s.Equal("", section["database"])
	s.Equal("rdb", section["exports"].([]any)[0].(map[string]any)["format"])

	s.backUp(b, cache)
	services.WaitForBackups()

	rec := s.only(cache.ID)
	s.Equal(models.BackupSucceeded, rec.Status, "message: %v", rec.Message)
	s.Regexp(`^server-1/cache/cache-\d{8}T\d{6}Z-\d+\.rdb$`, rec.Key)
	s.Equal("8.10.3", rec.ServerVersion)
	s.Contains(strings.Join(s.dumps[0], " "), "redis-cli --rdb -")
	stored := must(io.ReadAll(must(s.storage.Get(context.Background(), rec.Key))))
	s.Equal(redisSnapshot, string(stored), "stored as Redis sent it, not gzipped")

	raw := must(io.ReadAll(must(s.storage.Get(context.Background(), backups.ManifestKey(rec.Key)))))
	manifest := must(backups.ParseManifest(raw))
	s.Equal("redis", manifest.Template)
	s.Equal("none", manifest.Compression)
	s.Empty(manifest.Database, "a snapshot holds every database")
}

func (s *BackupsTestSuite) TestARedisRestoreReplacesTheWholeServer() {
	cache := s.templateApp("cache", "redis")
	old := s.backedUp(cache, redis810, redisSnapshot)
	s.scriptRedis(true, "REDIS0015 newer keys", 0)
	b := signedIn(s.T(), &s.TestCase)

	preview := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?backup=%d", cache.ID, old.ID), 200)
	s.Empty(preview["refusal"])
	s.Equal(true, preview["whole_server"])
	s.Equal(false, preview["atomic"])
	s.Equal("", preview["database"])

	s.restore(b, cache, map[string]any{"backup": old.ID, "safety": true, "confirm": "cache"})
	services.WaitForRestores()

	rec := s.restores(cache.ID)[0]
	s.Equal(models.RestoreSucceeded, rec.Status, "message: %v", rec.Message)
	s.Equal([]string{redisSnapshot}, s.restoredSoFar())
	s.NotNil(rec.SafetyBackupID)
}

func (s *BackupsTestSuite) TestARedisServerWithoutDebugIsRefusedBeforeItsSafetyBackup() {
	cache := s.templateApp("cache", "redis")
	old := s.backedUp(cache, redis810, redisSnapshot)
	s.scriptRedis(false, "REDIS0015 newer keys", 0)
	b := signedIn(s.T(), &s.TestCase)
	backupsBefore := must(facades.Orm().Query().Model(&models.Backup{}).Count())

	refusal := "cache's server was started by an older release of Echowa, without what a restore needs. Redeploy cache, then restore."
	preview := s.getJSON(b, fmt.Sprintf("/apps/%d/restores/preview?backup=%d", cache.ID, old.ID), 200)
	s.Equal(refusal, preview["refusal"])
	s.restore(b, cache, map[string]any{"backup": old.ID, "safety": true, "confirm": "cache"})
	s.Equal(refusal, s.restoreError(b, cache, "source"))

	s.Empty(s.restores(cache.ID))
	s.Equal(backupsBefore, must(facades.Orm().Query().Model(&models.Backup{}).Count()), "no safety backup")
	s.Empty(s.restoredSoFar())
}

func (s *BackupsTestSuite) TestARedisRestoreThatRefusesTheDumpSaysNothingChanged() {
	cache := s.templateApp("cache", "redis")
	old := s.backedUp(cache, redis810, redisSnapshot)
	s.scriptRedis(true, "REDIS0015 newer keys", 3)
	b := signedIn(s.T(), &s.TestCase)

	s.restore(b, cache, map[string]any{"backup": old.ID, "safety": true, "confirm": "cache"})
	services.WaitForRestores()

	rec := s.restores(cache.ID)[0]
	s.Equal(models.RestoreFailed, rec.Status)
	s.Equal("The restore refused the dump. Nothing was restored.", *rec.Message)
}

func (s *BackupsTestSuite) TestAFailedRedisRestoreSaysTheKeysMayBeGone() {
	cache := s.templateApp("cache", "redis")
	old := s.backedUp(cache, redis810, redisSnapshot)
	s.scriptRedis(true, "REDIS0015 newer keys", 1)
	b := signedIn(s.T(), &s.TestCase)

	s.restore(b, cache, map[string]any{"backup": old.ID, "safety": true, "confirm": "cache"})
	services.WaitForRestores()

	rec := s.restores(cache.ID)[0]
	s.Equal(models.RestoreFailed, rec.Status)
	s.Equal(fmt.Sprintf("The restore exited with status 1. cache may have lost its keys. The safety backup is #%d.", *rec.SafetyBackupID), *rec.Message)

	s.restore(b, cache, map[string]any{"backup": old.ID, "safety": false, "confirm": "cache"})
	services.WaitForRestores()
	rec = s.restores(cache.ID)[1]
	s.Equal("The restore exited with status 1. cache may have lost its keys, and there's no safety backup.", *rec.Message)
}
