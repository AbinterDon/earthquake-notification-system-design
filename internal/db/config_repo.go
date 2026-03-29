package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AbinterDon/earthquake-notification-system-design/internal/domain"
)

// ConfigRepository persists user alert preferences in PostgreSQL.
type ConfigRepository struct {
	pool *pgxpool.Pool
}

func NewConfigRepository(pool *pgxpool.Pool) *ConfigRepository {
	return &ConfigRepository{pool: pool}
}

// Upsert inserts or updates the alert configuration for a device token.
func (r *ConfigRepository) Upsert(ctx context.Context, cfg domain.UserConfig) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_configs (token, magnitude, distance_km, channel, status)
		VALUES ($1, $2, $3, $4, 'active')
		ON CONFLICT (token) DO UPDATE
		    SET magnitude   = EXCLUDED.magnitude,
		        distance_km = EXCLUDED.distance_km,
		        channel     = EXCLUDED.channel,
		        status      = 'active',
		        updated_at  = NOW()
	`, cfg.Token, cfg.Magnitude, cfg.DistanceKm, cfg.Channel)
	return err
}

// GetByToken returns the config for a single device token, or nil if not found.
func (r *ConfigRepository) GetByToken(ctx context.Context, token string) (*domain.UserConfig, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, token, magnitude, distance_km, channel, status, created_at, updated_at
		FROM user_configs
		WHERE token = $1
	`, token)

	cfg, err := scanConfig(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return cfg, err
}

// GetByTokens batch-fetches active configs for a slice of device tokens.
// Returns a map keyed by token.
func (r *ConfigRepository) GetByTokens(ctx context.Context, tokens []string) (map[string]domain.UserConfig, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, token, magnitude, distance_km, channel, status, created_at, updated_at
		FROM user_configs
		WHERE token = ANY($1) AND status = 'active'
	`, tokens)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]domain.UserConfig, len(tokens))
	for rows.Next() {
		cfg, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		result[cfg.Token] = *cfg
	}
	return result, rows.Err()
}

func scanConfig(row pgx.Row) (*domain.UserConfig, error) {
	var cfg domain.UserConfig
	var createdAt, updatedAt time.Time
	err := row.Scan(
		&cfg.ID, &cfg.Token, &cfg.Magnitude, &cfg.DistanceKm,
		&cfg.Channel, &cfg.Status, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	cfg.CreatedAt = createdAt
	cfg.UpdatedAt = updatedAt
	return &cfg, nil
}
