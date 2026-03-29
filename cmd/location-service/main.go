// location-service receives periodic location updates from user devices and
// stores them in the Redis geohash location index.
//
// Endpoints:
//
//	POST /alerts/user_location  – update caller's lat/lng
//
// Update frequency: clients typically call this once per hour (or when they
// have moved >50 km). The service converts coordinates to a geohash cell
// (precision 5 ≈ 4.9 km) and updates the cell→devices index in Redis.
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
	"github.com/AbinterDon/earthquake-notification-system-design/internal/cache"
)

func main() {
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	jwtSecret := getEnv("JWT_SECRET", "dev-secret")
	addr := getEnv("ADDR", ":8082")

	_, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb, err := cache.NewClient(redisAddr)
	if err != nil {
		log.Fatalf("location-service: %v", err)
	}
	defer rdb.Close()

	locIndex := cache.NewLocationIndex(rdb)

	mux := http.NewServeMux()
	mux.Handle("POST /alerts/user_location", auth.Middleware(jwtSecret, handleLocation(locIndex)))

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		log.Printf("location-service: listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("location-service: %v", err)
		}
	}()

	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	<-ctx.Done()
	log.Println("location-service: shutting down…")
	srv.Shutdown(context.Background()) //nolint:errcheck
}

func handleLocation(idx *cache.LocationIndex) http.Handler {
	type request struct {
		Lat  float64 `json:"lat"`
		Long float64 `json:"long"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if req.Lat < -90 || req.Lat > 90 || req.Long < -180 || req.Long > 180 {
			http.Error(w, "invalid coordinates", http.StatusBadRequest)
			return
		}

		token := auth.DeviceToken(r.Context())
		if err := idx.Upsert(r.Context(), token, req.Lat, req.Long); err != nil {
			log.Printf("location-service: upsert failed: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
