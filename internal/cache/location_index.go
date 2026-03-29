package cache

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/AbinterDon/earthquake-notification-system-design/internal/geo"
)

// Key schema:
//
//	geo:cell:{geohash}         → SET  device tokens located in this cell
//	geo:device:{token}:cells   → SET  cells currently occupied by this device (for cleanup)
//	geo:device:{token}:loc     → STRING "lat,lng"  raw coordinates (TTL = locationTTL)

const locationTTL = 30 * 24 * time.Hour // locations expire after 30 days of inactivity

// LocationIndex stores and queries user positions using geohash cells.
type LocationIndex struct {
	rdb *redis.Client
}

func NewLocationIndex(rdb *redis.Client) *LocationIndex {
	return &LocationIndex{rdb: rdb}
}

// Upsert moves a device token from its old cell(s) to the new cell derived
// from lat/lng, and refreshes the raw location string.
func (l *LocationIndex) Upsert(ctx context.Context, token string, lat, lng float64) error {
	newCell := geo.EncodeCell(lat, lng)
	deviceCellsKey := "geo:device:" + token + ":cells"
	deviceLocKey := "geo:device:" + token + ":loc"

	// Fetch current cells to clean up stale memberships.
	oldCells, _ := l.rdb.SMembers(ctx, deviceCellsKey).Result()

	pipe := l.rdb.TxPipeline()

	// Remove device from all old cells.
	for _, old := range oldCells {
		pipe.SRem(ctx, "geo:cell:"+old, token)
	}

	// Register device in new cell.
	pipe.Del(ctx, deviceCellsKey)
	pipe.SAdd(ctx, deviceCellsKey, newCell)
	pipe.Expire(ctx, deviceCellsKey, locationTTL)
	pipe.SAdd(ctx, "geo:cell:"+newCell, token)

	// Store raw coords for precise distance filtering later.
	pipe.Set(ctx, deviceLocKey, fmt.Sprintf("%f,%f", lat, lng), locationTTL)

	_, err := pipe.Exec(ctx)
	return err
}

// GetDevicesInCells returns the deduplicated set of device tokens present in
// any of the provided geohash cells.
func (l *LocationIndex) GetDevicesInCells(ctx context.Context, cells []string) ([]string, error) {
	if len(cells) == 0 {
		return nil, nil
	}

	pipe := l.rdb.Pipeline()
	cmds := make([]*redis.StringSliceCmd, len(cells))
	for i, cell := range cells {
		cmds[i] = pipe.SMembers(ctx, "geo:cell:"+cell)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	var tokens []string
	for _, cmd := range cmds {
		for _, t := range cmd.Val() {
			if _, dup := seen[t]; !dup {
				seen[t] = struct{}{}
				tokens = append(tokens, t)
			}
		}
	}
	return tokens, nil
}

// GetDeviceLocation returns the last reported coordinates for a device.
func (l *LocationIndex) GetDeviceLocation(ctx context.Context, token string) (lat, lng float64, err error) {
	val, err := l.rdb.Get(ctx, "geo:device:"+token+":loc").Result()
	if err != nil {
		return 0, 0, err
	}
	parts := strings.SplitN(val, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid location format: %q", val)
	}
	lat, err = strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, 0, err
	}
	lng, err = strconv.ParseFloat(parts[1], 64)
	return lat, lng, err
}
