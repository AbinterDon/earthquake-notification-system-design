.PHONY: help deps build up down logs test token seed

# ── Variables ─────────────────────────────────────────────────────────────────
JWT_SECRET ?= dev-secret
DEVICE     ?= test-device-001
CHANNEL    ?= fcm
SERVICES   := config-service location-service gateway broadcast-service worker tokengen

help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ── Development ───────────────────────────────────────────────────────────────

deps: ## Download Go module dependencies
	go mod tidy
	go mod download

build: ## Build all service binaries locally (output: bin/)
	@mkdir -p bin
	@for svc in $(SERVICES); do \
	  echo "  building $$svc…"; \
	  go build -o bin/$$svc ./cmd/$$svc; \
	done

vet: ## Run go vet on all packages
	go vet ./...

# ── Docker ────────────────────────────────────────────────────────────────────

up: ## Start all services with Docker Compose
	docker compose up --build -d
	@echo ""
	@echo "Services started:"
	@echo "  config-service   http://localhost:8081"
	@echo "  location-service http://localhost:8082"
	@echo "  gateway          http://localhost:8084"
	@echo ""
	@echo "Run 'make logs' to tail all service logs."

down: ## Stop and remove all Docker Compose containers
	docker compose down

logs: ## Tail logs from all containers
	docker compose logs -f

# ── Testing helpers ───────────────────────────────────────────────────────────

token: ## Generate a JWT for DEVICE= (default: test-device-001)
	@go run ./cmd/tokengen -token $(DEVICE) -secret $(JWT_SECRET)

seed: ## Register 3 test devices and set their locations + configs
	@echo "==> Generating tokens and seeding test data…"
	@$(MAKE) _seed_device DEVICE=apns_device_001 CHANNEL=apns LAT=25.0330 LNG=121.5654 MAG=4.0 DIST=200
	@$(MAKE) _seed_device DEVICE=fcm_device_001  CHANNEL=fcm  LAT=24.1477 LNG=120.6736 MAG=5.0 DIST=150
	@$(MAKE) _seed_device DEVICE=fcm_device_002  CHANNEL=fcm  LAT=22.6273 LNG=120.3014 MAG=3.5 DIST=300
	@echo "==> Seed complete. Trigger an earthquake with: make quake"

_seed_device:
	$(eval TOKEN := $(shell go run ./cmd/tokengen -token $(DEVICE) -secret $(JWT_SECRET) 2>/dev/null | grep 'Bearer token' -A1 | tail -1 | tr -d ' '))
	@curl -sf -X POST http://localhost:8081/alerts/configuration \
	     -H "Authorization: Bearer $(TOKEN)" \
	     -H "Content-Type: application/json" \
	     -d '{"magnitude":$(MAG),"distance_km":$(DIST),"channel":"$(CHANNEL)"}' && \
	 echo " ✓ config set for $(DEVICE)"
	@curl -sf -X POST http://localhost:8082/alerts/user_location \
	     -H "Authorization: Bearer $(TOKEN)" \
	     -H "Content-Type: application/json" \
	     -d '{"lat":$(LAT),"long":$(LNG)}' && \
	 echo " ✓ location set for $(DEVICE)"

quake: ## Fire a synthetic M6.5 earthquake near Taipei
	@curl -sf -X POST http://localhost:8084/earthquake \
	     -H "Content-Type: application/json" \
	     -d '{"lat":25.0330,"long":121.5654,"magnitude":6.5,"depth_km":10}' | jq .

simulate: ## Toggle automatic random earthquake generation
	@curl -sf -X POST http://localhost:8084/simulate | jq .
