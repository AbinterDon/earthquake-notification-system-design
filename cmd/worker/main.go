// worker consumes notification jobs from a per-channel Redis Stream and sends
// push notifications via the appropriate vendor (APNs or FCM).
//
// Run one instance per channel:
//
//	CHANNEL=apns ./worker
//	CHANNEL=fcm  ./worker
//
// Architecture highlights:
//
//   - Before sending, the worker checks the supersession cache: if the job's
//     alert version is no longer the latest, the outbox record is marked
//     CANCELLED_SUPERSEDED and the job is dropped without network I/O.
//
//   - The outbox provides an idempotency gate: if a notification_id is already
//     in a terminal state (e.g. from a previous delivery attempt), the device
//     is skipped.
//
//   - The mock sender logs deliveries to stdout. Swap sendMock with sendAPNs /
//     sendFCM to connect to real vendors.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/AbinterDon/earthquake-notification-system-design/internal/cache"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/db"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/domain"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/queue"
)

func main() {
	channel := getEnv("CHANNEL", domain.ChannelFCM) // "apns" | "fcm"
	dsn := getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/earthquake?sslmode=disable")
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	hostname, _ := os.Hostname()
	consumerGroup := channel + "-workers"
	consumerName := fmt.Sprintf("worker-%s-%s", channel, hostname)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		log.Fatalf("worker[%s]: %v", channel, err)
	}
	defer pool.Close()

	rdb, err := cache.NewClient(redisAddr)
	if err != nil {
		log.Fatalf("worker[%s]: %v", channel, err)
	}
	defer rdb.Close()

	outboxRepo := db.NewOutboxRepository(pool)
	supersession := cache.NewSupersessionCache(rdb)

	streamName := queue.StreamAPNs
	if channel == domain.ChannelFCM {
		streamName = queue.StreamFCM
	}

	consumer := queue.NewConsumer(rdb, streamName, consumerGroup, consumerName)
	if err := consumer.EnsureGroup(ctx); err != nil {
		log.Fatalf("worker[%s]: ensure group: %v", channel, err)
	}

	log.Printf("worker[%s]: running as consumer %q", channel, consumerName)

	for {
		select {
		case <-ctx.Done():
			log.Printf("worker[%s]: shutting down…", channel)
			return
		default:
		}

		msgs, err := consumer.ReadMessages(ctx, 10)
		if err != nil {
			log.Printf("worker[%s]: read error: %v", channel, err)
			continue
		}

		for _, msg := range msgs {
			var job domain.NotificationJob
			if err := json.Unmarshal([]byte(msg.Payload), &job); err != nil {
				log.Printf("worker[%s]: bad payload: %v", channel, err)
				consumer.Ack(ctx, msg.ID) //nolint:errcheck
				continue
			}

			processJob(ctx, job, outboxRepo, supersession, channel)
			consumer.Ack(ctx, msg.ID) //nolint:errcheck
		}
	}
}

func processJob(
	ctx context.Context,
	job domain.NotificationJob,
	outbox *db.OutboxRepository,
	supersession *cache.SupersessionCache,
	channel string,
) {
	// Check supersession: drop if a newer version has been published.
	latestVersion, err := supersession.GetVersion(ctx, job.AlertID)
	if err != nil {
		log.Printf("worker[%s]: supersession lookup error: %v", channel, err)
	}
	if latestVersion > job.Version {
		log.Printf("worker[%s]: job superseded alert_id=%s v=%d latest=%d — cancelling",
			channel, job.AlertID[:8], job.Version, latestVersion)
		// Mark all devices in this job as CANCELLED_SUPERSEDED.
		for _, deviceID := range job.DeviceIDs {
			nid := notificationID(job.AlertID, job.Version, deviceID)
			outbox.UpdateStatus(ctx, nid, domain.StatusCancelledSuperseded) //nolint:errcheck
		}
		return
	}

	sent, skipped, failed := 0, 0, 0

	for _, deviceID := range job.DeviceIDs {
		nid := notificationID(job.AlertID, job.Version, deviceID)

		// Idempotency gate: skip if already in a terminal state.
		status, err := outbox.GetStatus(ctx, nid)
		if err != nil {
			log.Printf("worker[%s]: outbox lookup error: %v", channel, err)
			continue
		}
		if domain.IsTerminalStatus(status) {
			skipped++
			continue
		}

		// Mark as ATTEMPTED before hitting the vendor.
		outbox.UpdateStatus(ctx, nid, domain.StatusAttempted) //nolint:errcheck

		// Send (mock implementation; replace with real APNs/FCM calls).
		err = sendMock(channel, deviceID, job.Event)
		if err != nil {
			log.Printf("worker[%s]: send failed device=%s…: %v", channel, deviceID[:8], err)
			outbox.UpdateStatus(ctx, nid, domain.StatusFailed) //nolint:errcheck
			failed++
			continue
		}

		outbox.UpdateStatus(ctx, nid, domain.StatusVendorAccepted) //nolint:errcheck
		sent++
	}

	log.Printf("worker[%s]: alert_id=%s v=%d — sent=%d skipped=%d failed=%d",
		channel, job.AlertID[:8], job.Version, sent, skipped, failed)
}

// sendMock simulates a push notification by logging to stdout.
// It introduces a small random failure rate (5%) to exercise the FAILED path.
func sendMock(channel, deviceID string, evt domain.EarthquakeEvent) error {
	if rand.Float32() < 0.05 {
		return fmt.Errorf("mock vendor: transient error")
	}
	shortDevice := deviceID
	if len(shortDevice) > 12 {
		shortDevice = shortDevice[:12] + "…"
	}
	log.Printf("  [%s] → device=%s  mag=%.1f  lat=%.4f  long=%.4f",
		strings.ToUpper(channel), shortDevice, evt.Magnitude, evt.Lat, evt.Long)
	return nil
}

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
