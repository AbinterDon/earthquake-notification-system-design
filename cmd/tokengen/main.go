// tokengen is a development helper that mints JWT tokens for testing the API.
//
// Usage:
//
//	go run ./cmd/tokengen -token <device_token>
//	go run ./cmd/tokengen -token apns_device_001 -secret my-secret
//
// The generated Bearer token can be used in curl / Postman to authenticate
// against config-service and location-service.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	deviceToken := flag.String("token", "test-device-001", "device token to embed in the JWT")
	secret := flag.String("secret", getEnv("JWT_SECRET", "dev-secret"), "HMAC signing secret")
	ttl := flag.Duration("ttl", 24*time.Hour, "token validity duration")
	flag.Parse()

	claims := jwt.MapClaims{
		"device_token": *deviceToken,
		"iat":          time.Now().Unix(),
		"exp":          time.Now().Add(*ttl).Unix(),
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(*secret))
	if err != nil {
		log.Fatalf("tokengen: %v", err)
	}

	fmt.Printf("device_token : %s\n", *deviceToken)
	fmt.Printf("Bearer token :\n\n  %s\n\n", signed)
	fmt.Println("Example usage:")
	fmt.Printf("  curl -X POST http://localhost:8081/alerts/configuration \\\n")
	fmt.Printf("       -H 'Authorization: Bearer %s' \\\n", signed)
	fmt.Printf("       -H 'Content-Type: application/json' \\\n")
	fmt.Printf(`       -d '{"magnitude":4.0,"distance_km":300,"channel":"fcm"}'` + "\n")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
