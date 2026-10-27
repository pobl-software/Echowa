package migrations

import (
	"goravel/app/facades"
	"goravel/app/templates"
)

// redisHealthcheckWithArgument is the Redis template's first healthcheck,
// which put the password in redis-cli's arguments, where anyone on the host
// could read it.
const redisHealthcheckWithArgument = `redis-cli -a "$REDIS_PASSWORD" --no-auth-warning ping | grep -q PONG`

// M20261027180000UpdateRedisHealthcheck moves Redis apps still on that
// healthcheck to the template's current one, which passes the password in
// REDISCLI_AUTH. A healthcheck the admin changed is left alone. It applies
// on each app's next deploy.
type M20261027180000UpdateRedisHealthcheck struct{}

// Signature The unique signature for the migration.
func (r *M20261027180000UpdateRedisHealthcheck) Signature() string {
	return "20261027180000_update_redis_healthcheck"
}

// Up Run the migrations.
func (r *M20261027180000UpdateRedisHealthcheck) Up() error {
	redis, ok := templates.Find("redis")
	if !ok {
		return nil
	}
	// The migrator's transaction: any other query waits on its write lock.
	_, err := facades.Schema().Orm().Query().Table("apps").
		Where("template", "redis").
		Where("healthcheck_cmd", redisHealthcheckWithArgument).
		Update("healthcheck_cmd", redis.Healthcheck)

	return err
}

// Down Reverse the migrations. The new healthcheck works on any release.
func (r *M20261027180000UpdateRedisHealthcheck) Down() error {
	return nil
}
