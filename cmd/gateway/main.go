// gateway ingests earthquake events and publishes them onto stream:earthquakes.
//
// In production this service would maintain a persistent feed to seismological
// APIs (e.g. USGS, CWA Taiwan), handling TLS, heartbeats, reconnects, and
// exponential backoff.
//
// For local development it exposes a trigger endpoint so you can simulate
// earthquakes without an external data source.
//
// Endpoints:
//
//	POST /earthquake  – publish a synthetic earthquake event (no auth required)
//	POST /simulate    – toggle automatic random earthquake generation
package main

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/AbinterDon/earthquake-notification-system-design/internal/cache"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/domain"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/queue"
)

func main() {
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	addr := getEnv("ADDR", ":8084")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb, err := cache.NewClient(redisAddr)
	if err != nil {
		log.Fatalf("gateway: %v", err)
	}
	defer rdb.Close()

	producer := queue.NewProducer(rdb, queue.StreamEarthquakes)

	var simulating atomic.Bool

	mux := http.NewServeMux()

	// Manual earthquake trigger
	mux.HandleFunc("POST /earthquake", func(w http.ResponseWriter, r *http.Request) {
		var evt domain.EarthquakeEvent
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if evt.AlertID == "" {
			evt.AlertID = uuid.NewString()
		}
		if evt.Version == 0 {
			evt.Version = 1
		}
		if evt.OccurredAt.IsZero() {
			evt.OccurredAt = time.Now().UTC()
		}

		if err := producer.Publish(ctx, evt); err != nil {
			log.Printf("gateway: publish failed: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		log.Printf("gateway: published alert_id=%s version=%d mag=%.1f lat=%.4f long=%.4f",
			evt.AlertID, evt.Version, evt.Magnitude, evt.Lat, evt.Long)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"alert_id": evt.AlertID}) //nolint:errcheck
	})

	// Toggle automatic random simulation
	mux.HandleFunc("POST /simulate", func(w http.ResponseWriter, r *http.Request) {
		if simulating.Load() {
			simulating.Store(false)
			log.Println("gateway: simulation stopped")
			w.Write([]byte(`{"status":"stopped"}`)) //nolint:errcheck
		} else {
			simulating.Store(true)
			log.Println("gateway: simulation started")
			go runSimulator(ctx, producer, &simulating)
			w.Write([]byte(`{"status":"started"}`)) //nolint:errcheck
		}
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		log.Printf("gateway: listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("gateway: %v", err)
		}
	}()

	<-ctx.Done()
	simulating.Store(false)
	log.Println("gateway: shutting down…")
	srv.Shutdown(context.Background()) //nolint:errcheck
}

// runSimulator fires random earthquake events every 10-30 seconds while active.
// Coordinates are biased toward Taiwan (a seismically active region).
func runSimulator(ctx context.Context, p *queue.Producer, active *atomic.Bool) {
	alertID := uuid.NewString()
	version := 0

	for active.Load() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(10+rand.Intn(20)) * time.Second):
		}

		if !active.Load() {
			return
		}

		// ~30% chance this is an update (same alert, higher version)
		if rand.Float32() < 0.3 && version > 0 {
			version++
		} else {
			alertID = uuid.NewString()
			version = 1
		}

		evt := domain.EarthquakeEvent{
			AlertID:    alertID,
			Version:    version,
			Lat:        23.5 + (rand.Float64()-0.5)*4,  // roughly Taiwan
			Long:       121.0 + (rand.Float64()-0.5)*4,
			Magnitude:  3.0 + rand.Float64()*5.0,       // M3–M8
			DepthKm:    5 + rand.Float64()*35,
			OccurredAt: time.Now().UTC(),
		}

		if err := p.Publish(ctx, evt); err != nil {
			log.Printf("gateway: simulator publish error: %v", err)
			continue
		}
		log.Printf("gateway: [SIM] alert_id=%s v=%d mag=%.1f lat=%.4f long=%.4f",
			evt.AlertID[:8], evt.Version, evt.Magnitude, evt.Lat, evt.Long)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
