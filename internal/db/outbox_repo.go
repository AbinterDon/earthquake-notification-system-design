package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AbinterDon/earthquake-notification-system-design/internal/domain"
)

// OutboxRepository manages the notification_outbox table in PostgreSQL.
// It acts as an idempotency gate and a per-device status ledger.
type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// InsertIfNotExists writes an ENQUEUED record only when the notification_id
// is not already present. Safe to call multiple times (idempotent).
func (r *OutboxRepository) InsertIfNotExists(ctx context.Context, rec domain.OutboxRecord) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notification_outbox
		    (notification_id, alert_id, version, device_id, channel, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (notification_id) DO NOTHING
	`, rec.NotificationID, rec.AlertID, rec.Version, rec.DeviceID, rec.Channel, rec.Status)
	return err
}

// GetStatus returns the current status of a notification, or "" if not found.
func (r *OutboxRepository) GetStatus(ctx context.Context, notificationID string) (string, error) {
	var status string
	err := r.pool.QueryRow(ctx,
		`SELECT status FROM notification_outbox WHERE notification_id = $1`,
		notificationID,
	).Scan(&status)
	if err != nil {
		// pgx returns pgx.ErrNoRows; treat as "not found" → empty string
		return "", nil
	}
	return status, nil
}

// UpdateStatus transitions a notification to a new status.
// Noop if the record is already in a terminal state.
func (r *OutboxRepository) UpdateStatus(ctx context.Context, notificationID, status string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE notification_outbox
		SET    status = $2, updated_at = NOW()
		WHERE  notification_id = $1
		  AND  status NOT IN ('VENDOR_ACCEPTED', 'FAILED', 'CANCELLED_SUPERSEDED')
	`, notificationID, status)
	return err
}

// CancelSuperseded marks all non-terminal outbox entries for the given
// alert_id whose version is older than newerVersion as CANCELLED_SUPERSEDED.
func (r *OutboxRepository) CancelSuperseded(ctx context.Context, alertID string, newerVersion int) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE notification_outbox
		SET    status = 'CANCELLED_SUPERSEDED', updated_at = NOW()
		WHERE  alert_id = $1
		  AND  version  < $2
		  AND  status NOT IN ('VENDOR_ACCEPTED', 'FAILED', 'CANCELLED_SUPERSEDED')
	`, alertID, newerVersion)
	return err
}
