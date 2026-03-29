package domain

import "time"

// UserConfig holds a user's alert preferences stored in PostgreSQL.
type UserConfig struct {
	ID         string    `json:"id"`
	Token      string    `json:"token"`
	Magnitude  float64   `json:"magnitude"`   // minimum magnitude to trigger alert
	DistanceKm int       `json:"distance_km"` // maximum distance from epicenter (km)
	Channel    string    `json:"channel"`     // "apns" | "fcm"
	Status     string    `json:"status"`      // "active" | "inactive"
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// EarthquakeEvent is published by the Gateway onto stream:earthquakes.
type EarthquakeEvent struct {
	AlertID    string    `json:"alert_id"`
	Version    int       `json:"version"` // monotonically increasing per alert_id
	Lat        float64   `json:"lat"`
	Long       float64   `json:"long"`
	Magnitude  float64   `json:"magnitude"`
	DepthKm    float64   `json:"depth_km"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NotificationJob is a chunk of device tokens to notify, enqueued onto
// stream:apns or stream:fcm by the Broadcast Service.
type NotificationJob struct {
	AlertID   string          `json:"alert_id"`
	Version   int             `json:"version"`
	Channel   string          `json:"channel"`    // "apns" | "fcm"
	DeviceIDs []string        `json:"device_ids"` // max ~500 per job
	Event     EarthquakeEvent `json:"event"`
}

// OutboxRecord is one row in notification_outbox.
type OutboxRecord struct {
	NotificationID string    `json:"notification_id"`
	AlertID        string    `json:"alert_id"`
	Version        int       `json:"version"`
	DeviceID       string    `json:"device_id"`
	Channel        string    `json:"channel"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Notification statuses (terminal = VENDOR_ACCEPTED | FAILED | CANCELLED_SUPERSEDED)
const (
	StatusEnqueued            = "ENQUEUED"
	StatusAttempted           = "ATTEMPTED"
	StatusVendorAccepted      = "VENDOR_ACCEPTED"
	StatusFailed              = "FAILED"
	StatusCancelledSuperseded = "CANCELLED_SUPERSEDED"
)

// Push channels
const (
	ChannelAPNs = "apns"
	ChannelFCM  = "fcm"
)

// IsTerminalStatus returns true when no further state transitions are expected.
func IsTerminalStatus(s string) bool {
	return s == StatusVendorAccepted || s == StatusFailed || s == StatusCancelledSuperseded
}
