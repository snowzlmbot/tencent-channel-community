package notices

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// ControlState is host-authorized metadata, not proof of transport delivery.
// Only a trusted host with the messaging capability may supply actual receipts.
// Neither receipts nor the explicit-confirm flag are cryptographically verified.
type ControlState struct {
	Phase     Phase     `json:"phase"`
	Route     Route     `json:"route"`
	Challenge string    `json:"challenge,omitempty"`
	Receipt   string    `json:"receipt,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

var controlNow = time.Now

// Validate accepts nil for envelopes created before control was introduced.
// Expiration gates confirmation, not an already active authorization.
func (s *ControlState) Validate() error {
	if s == nil {
		return nil
	}
	if s.Phase == OFF && s.Route == (Route{}) && s.Challenge == "" && s.Receipt == "" && s.ExpiresAt.IsZero() {
		return nil
	}
	if s.Route.Validate() != nil || !valid(s.Challenge) || s.ExpiresAt.IsZero() {
		return ErrInvalid
	}
	switch s.Phase {
	case OFF:
		if s.Receipt != "" {
			return ErrInvalid
		}
	case TESTED, ACTIVE:
		if !valid(s.Receipt) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func NewControlTest(route Route) (ControlState, error) {
	if route.Validate() != nil {
		return ControlState{}, ErrInvalid
	}
	var challenge [32]byte
	if _, err := rand.Read(challenge[:]); err != nil {
		return ControlState{}, ErrInvalid
	}
	return ControlState{Phase: OFF, Route: route, Challenge: hex.EncodeToString(challenge[:]), ExpiresAt: controlNow().UTC().Add(10 * time.Minute)}, nil
}

func (s ControlState) RecordTest(challenge, receipt string) (ControlState, error) {
	if s.Validate() != nil || s.Phase != OFF || s.Challenge == "" || challenge != s.Challenge || !valid(receipt) || !controlNow().Before(s.ExpiresAt) {
		return s, ErrInvalid
	}
	s.Phase, s.Receipt = TESTED, receipt
	return s, nil
}

func (s ControlState) Confirm(challenge string, explicit bool) (ControlState, error) {
	if s.Validate() != nil || !explicit || s.Phase != TESTED || challenge != s.Challenge || !controlNow().Before(s.ExpiresAt) {
		return s, ErrInvalid
	}
	s.Phase = ACTIVE
	return s, nil
}

func (s ControlState) Off() ControlState { return ControlState{Phase: OFF} }
