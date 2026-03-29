// broadcast-service is the geo-targeting orchestrator.
//
// It consumes earthquake events from stream:earthquakes, determines which
// users are affected (using the Redis geohash index + PostgreSQL configs),
// and enqueues per-channel notification jobs onto stream:apns / stream:fcm.
//
// Key design decisions (from the system design doc):
//
//  1. Geo lookup: geohash cell grid in Redis for O(1) candidate retrieval,
//     followed by precise haversine filtering per user.
//
//  2. Supersession: a Lua-based atomic compare-and-set in Redis ensures that
//     only the latest version of each alert is processed.
//
//  3. Outbox: every device-level notification is recorded in PostgreSQL before
//     it is enqueued, giving workers an idempotency gate.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/AbinterDon/earthquake-notification-system-design/internal/cache"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/db"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/domain"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/geo"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/queue"
)

const (
	consumerGroup = "broadcast-service"
	chunkSize     = 500 // device tokens per notification job
	queryBatch    = 1000
)

func main() {
	dsn := getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/earthquake?sslmode=disable")
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	hostname, _ := os.Hostname()
	consumerName := fmt.Sprintf("broadcast-%s", hostname)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		log.Fatalf("broadcast-service: %v", err)
	}
	defer pool.Close()

	rdb, err := cache.NewClient(redisAddr)
	if err != nil {
		log.Fatalf("broadcast-service: %v", err)
	}
	defer rdb.Close()

	svc := &broadcastService{
		configRepo:   db.NewConfigRepository(pool),
		outboxRepo:   db.NewOutboxRepository(pool),
		locIndex:     cache.NewLocationIndex(rdb),
		supersession: cache.NewSupersessionCache(rdb),
		apnsProducer: queue.NewProducer(rdb, queue.StreamAPNs),
		fcmProducer:  queue.NewProducer(rdb, queue.StreamFCM),
	}

	consumer := queue.NewConsumer(rdb, queue.StreamEarthquakes, consumerGroup, consumerName)
	if err := consumer.EnsureGroup(ctx); err != nil {
		log.Fatalf("broadcast-service: ensure group: %v", err)
	}

	log.Printf("broadcast-service: running as consumer %q", consumerName)

	for {
		select {
		case <-ctx.Done():
			log.Println("broadcast-service: shutting down…")
			return
		default:
		}

		msgs, err := consumer.ReadMessages(ctx, 10)
		if err != nil {
			log.Printf("broadcast-service: read error: %v", err)
			continue
		}

		for _, msg := range msgs {
			var evt domain.EarthquakeEvent
			if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
				log.Printf("broadcast-service: bad payload %s: %v", msg.ID, err)
				consumer.Ack(ctx, msg.ID) //nolint:errcheck
				continue
			}

			if err := svc.process(ctx, evt); err != nil {
				log.Printf("broadcast-service: process error alert_id=%s: %v", evt.AlertID, err)
				// Do not ACK — message will be re-delivered (at-least-once).
				continue
			}

			consumer.Ack(ctx, msg.ID) //nolint:errcheck
		}
	}
}

// broadcastService holds all dependencies for the orchestrator logic.
type broadcastService struct {
	configRepo   *db.ConfigRepository
	outboxRepo   *db.OutboxRepository
	locIndex     *cache.LocationIndex
	supersession *cache.SupersessionCache
	apnsProducer *queue.Producer
	fcmProducer  *queue.Producer
}

func (s *broadcastService) process(ctx context.Context, evt domain.EarthquakeEvent) error {
	// 1. Supersession check: discard events older than the latest seen version.
	accepted, err := s.supersession.TrySetVersion(ctx, evt.AlertID, evt.Version)
	if err != nil {
		return fmt.Errorf("supersession check: %w", err)
	}
	if !accepted {
		log.Printf("broadcast-service: dropping superseded alert_id=%s version=%d", evt.AlertID, evt.Version)
		return nil
	}

	// 2. Cancel outbox entries for older versions of this alert.
	if evt.Version > 1 {
		if err := s.outboxRepo.CancelSuperseded(ctx, evt.AlertID, evt.Version); err != nil {
			log.Printf("broadcast-service: cancel superseded: %v", err)
		}
	}

	// 3. Compute candidate cells using magnitude-based search radius.
	searchRadius := geo.MagnitudeToRadius(evt.Magnitude)
	cells := geo.CellsInRadius(evt.Lat, evt.Long, searchRadius)
	log.Printf("broadcast-service: alert_id=%s mag=%.1f radius=%.0fkm cells=%d",
		evt.AlertID[:8], evt.Magnitude, searchRadius, len(cells))

	// 4. Retrieve candidate device tokens from the location index.
	candidates, err := s.locIndex.GetDevicesInCells(ctx, cells)
	if err != nil {
		return fmt.Errorf("location lookup: %w", err)
	}
	if len(candidates) == 0 {
		log.Printf("broadcast-service: no candidates for alert_id=%s", evt.AlertID[:8])
		return nil
	}

	// 5. Batch-fetch user configs and apply precise distance + magnitude filters.
	var apnsDevices, fcmDevices []string

	for i := 0; i < len(candidates); i += queryBatch {
		end := i + queryBatch
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[i:end]

		configs, err := s.configRepo.GetByTokens(ctx, batch)
		if err != nil {
			return fmt.Errorf("config batch fetch: %w", err)
		}

		for _, token := range batch {
			cfg, ok := configs[token]
			if !ok {
				continue // no config → not subscribed
			}
			if evt.Magnitude < cfg.Magnitude {
				continue // below user's magnitude threshold
			}
			userLat, userLng, err := s.locIndex.GetDeviceLocation(ctx, token)
			if err != nil {
				continue // location expired or missing
			}
			if geo.HaversineKm(evt.Lat, evt.Long, userLat, userLng) > float64(cfg.DistanceKm) {
				continue // outside user's distance preference
			}

			switch cfg.Channel {
			case domain.ChannelAPNs:
				apnsDevices = append(apnsDevices, token)
			case domain.ChannelFCM:
				fcmDevices = append(fcmDevices, token)
			}
		}
	}

	total := len(apnsDevices) + len(fcmDevices)
	log.Printf("broadcast-service: alert_id=%s matched=%d (apns=%d fcm=%d)",
		evt.AlertID[:8], total, len(apnsDevices), len(fcmDevices))

	// 6. Split into chunks and enqueue per-channel notification jobs.
	for _, ch := range []struct {
		channel string
		devices []string
	}{
		{domain.ChannelAPNs, apnsDevices},
		{domain.ChannelFCM, fcmDevices},
	} {
		if len(ch.devices) == 0 {
			continue
		}
		producer := s.apnsProducer
		if ch.channel == domain.ChannelFCM {
			producer = s.fcmProducer
		}

		for i := 0; i < len(ch.devices); i += chunkSize {
			end := i + chunkSize
			if end > len(ch.devices) {
				end = len(ch.devices)
			}
			chunk := ch.devices[i:end]

			// Write outbox records before enqueuing (ensures idempotency gate
			// is in place before workers start processing).
			for _, deviceID := range chunk {
				nid := notificationID(evt.AlertID, evt.Version, deviceID)
				s.outboxRepo.InsertIfNotExists(ctx, domain.OutboxRecord{ //nolint:errcheck
					NotificationID: nid,
					AlertID:        evt.AlertID,
					Version:        evt.Version,
					DeviceID:       deviceID,
					Channel:        ch.channel,
					Status:         domain.StatusEnqueued,
				})
			}

			job := domain.NotificationJob{
				AlertID:   evt.AlertID,
				Version:   evt.Version,
				Channel:   ch.channel,
				DeviceIDs: chunk,
				Event:     evt,
			}
			if err := producer.Publish(ctx, job); err != nil {
				return fmt.Errorf("enqueue %s chunk: %w", ch.channel, err)
			}
		}
	}

	return nil
}

// notificationID returns SHA-256(alert_id|version|device_id) truncated to 32 hex chars.
func notificationID(alertID string, version int, deviceID string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s", alertID, version, deviceID)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
