package session

import "time"

type Status string

const (
	StatusUnknown Status = "unknown"
	StatusActive  Status = "active"
	StatusExpired Status = "expired"
	StatusRevoked Status = "revoked"
	StatusError   Status = "error"
)

type Session struct {
	ID            string     `json:"id"`
	AccountID     string     `json:"account_id"`
	Status        Status     `json:"status"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
