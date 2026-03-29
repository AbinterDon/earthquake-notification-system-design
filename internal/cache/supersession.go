package cache

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Key schema:
//
//	supersession:{alert_id} → STRING integer (latest known version)

const supersessionTTL = 24 * time.Hour

// SupersessionCache tracks the latest version seen for each alert_id.
// It is used by the Broadcast Service to drop outdated events and by Workers
// to skip sending stale notification jobs.
type SupersessionCache struct {
	rdb *redis.Client
}

func NewSupersessionCache(rdb *redis.Client) *SupersessionCache {
	return &SupersessionCache{rdb: rdb}
}

// TrySetVersion atomically sets the version for alertID if it is newer than
// the current stored value. Returns true if the update was applied (i.e. the
// caller's version is the latest and should be processed).
//
// Implemented as a Lua script to avoid TOCTOU races in concurrent deployments.
func (c *SupersessionCache) TrySetVersion(ctx context.Context, alertID string, version int) (bool, error) {
	script := redis.NewScript(`
		local key    = KEYS[1]
		local newVer = tonumber(ARGV[1])
		local cur    = tonumber(redis.call('GET', key))
		if cur == nil or newVer >= cur then
			redis.call('SET', key, ARGV[1], 'EX', ARGV[2])
			return 1
		end
		return 0
	`)
	result, err := script.Run(ctx, c.rdb,
		[]string{"supersession:" + alertID},
		version,
		int(supersessionTTL.Seconds()),
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// GetVersion returns the latest version stored for alertID, or 0 if unknown.
func (c *SupersessionCache) GetVersion(ctx context.Context, alertID string) (int, error) {
	val, err := c.rdb.Get(ctx, "supersession:"+alertID).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(val)
}
