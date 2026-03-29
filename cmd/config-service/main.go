// config-service exposes REST endpoints for users to set their alert preferences.
//
// Endpoints:
//
//	POST /alerts/configuration   – upsert magnitude + distance settings
//	GET  /alerts/configuration   – retrieve current settings
//
// Authentication: Bearer JWT containing a "device_token" claim (see cmd/tokengen).
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/AbinterDon/earthquake-notification-system-design/internal/auth"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/db"
	"github.com/AbinterDon/earthquake-notification-system-design/internal/domain"
)

func main() {
	dsn := getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/earthquake?sslmode=disable")
	jwtSecret := getEnv("JWT_SECRET", "dev-secret")
	addr := getEnv("ADDR", ":8081")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		log.Fatalf("config-service: %v", err)
	}
	defer pool.Close()

	configRepo := db.NewConfigRepository(pool)

	mux := http.NewServeMux()
	mux.Handle("POST /alerts/configuration", auth.Middleware(jwtSecret, handleSet(configRepo)))
	mux.Handle("GET /alerts/configuration", auth.Middleware(jwtSecret, handleGet(configRepo)))

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		log.Printf("config-service: listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("config-service: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("config-service: shutting down…")
	srv.Shutdown(context.Background()) //nolint:errcheck
}

// handleSet upserts the caller's alert configuration.
func handleSet(repo *db.ConfigRepository) http.Handler {
	type request struct {
		Magnitude  float64 `json:"magnitude"`
		DistanceKm int     `json:"distance_km"`
		Channel    string  `json:"channel"` // "apns" | "fcm"  (optional, default "fcm")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if req.Magnitude < 0 || req.Magnitude > 10 {
			http.Error(w, "magnitude must be 0–10", http.StatusBadRequest)
			return
		}
		if req.DistanceKm <= 0 {
			http.Error(w, "distance_km must be > 0", http.StatusBadRequest)
			return
		}
		if req.Channel == "" {
			req.Channel = domain.ChannelFCM
		}
		if req.Channel != domain.ChannelAPNs && req.Channel != domain.ChannelFCM {
			http.Error(w, "channel must be 'apns' or 'fcm'", http.StatusBadRequest)
			return
		}

		token := auth.DeviceToken(r.Context())
		cfg := domain.UserConfig{
			Token:      token,
			Magnitude:  req.Magnitude,
			DistanceKm: req.DistanceKm,
			Channel:    req.Channel,
		}
		if err := repo.Upsert(r.Context(), cfg); err != nil {
			log.Printf("config-service: upsert failed: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleGet returns the caller's current alert configuration.
func handleGet(repo *db.ConfigRepository) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := auth.DeviceToken(r.Context())
		cfg, err := repo.GetByToken(r.Context(), token)
		if err != nil {
			log.Printf("config-service: get failed: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if cfg == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg) //nolint:errcheck
	})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
