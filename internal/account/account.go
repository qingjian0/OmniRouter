package account

import "time"

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusError    Status = "error"
)

type Account struct {
	ID         string    `json:"id"`
	ProviderID string    `json:"provider_id"`
	Name       string    `json:"name"`
	Status     Status    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
