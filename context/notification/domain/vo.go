package domain

import (
	"fmt"

	"github.com/google/uuid"
)

type NotificationID string
type TenantID string

func NewNotificationID() NotificationID { return NotificationID(uuid.NewString()) }

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func (id NotificationID) String() string { return string(id) }
func (id TenantID) String() string       { return string(id) }

// ── Channel ───────────────────────────────────────────────────────────────────

type Channel string

const (
	ChannelEmail Channel = "email"
	// ChannelSMS  Channel = "sms"   // Fase 4
	// ChannelPush Channel = "push"  // Fase 4
)

func ParseChannel(s string) (Channel, error) {
	c := Channel(s)
	switch c {
	case ChannelEmail:
		return c, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidChannel, s)
}

func (c Channel) String() string { return string(c) }

// ── NotificationStatus ────────────────────────────────────────────────────────

type NotificationStatus string

const (
	StatusPending   NotificationStatus = "pending"
	StatusSent      NotificationStatus = "sent"
	StatusFailed    NotificationStatus = "failed"
)

func (s NotificationStatus) String() string { return string(s) }
