package notices

import (
	"testing"
	"time"
)

func TestControlLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	old := controlNow
	controlNow = func() time.Time { return now }
	defer func() { controlNow = old }()
	route := Route{SessionKey: "host-session", Channel: "telegram", Target: "-100", ThreadID: "674", AccountID: "host-account"}
	s, err := NewControlTest(route)
	if err != nil || s.Validate() != nil || s.Phase != OFF || len(s.Challenge) != 64 || s.ExpiresAt != now.Add(10*time.Minute) {
		t.Fatal("invalid test state")
	}
	another, err := NewControlTest(route)
	if err != nil || another.Challenge == s.Challenge {
		t.Fatal("challenge not fresh")
	}
	if _, err = s.Confirm(s.Challenge, true); err == nil {
		t.Fatal("enabled without receipt")
	}
	if _, err = s.RecordTest("wrong", "host-receipt"); err == nil {
		t.Fatal("wrong challenge")
	}
	if _, err = s.RecordTest(s.Challenge, ""); err == nil {
		t.Fatal("empty receipt")
	}
	tested, err := s.RecordTest(s.Challenge, "host-receipt")
	if err != nil || tested.Phase != TESTED || tested.Route != route {
		t.Fatal("record failed")
	}
	if _, err = tested.RecordTest(s.Challenge, "second"); err == nil {
		t.Fatal("replayed record")
	}
	if _, err = tested.Confirm(s.Challenge, false); err == nil {
		t.Fatal("implicit activation")
	}
	if _, err = tested.Confirm("wrong", true); err == nil {
		t.Fatal("wrong confirmation")
	}
	active, err := tested.Confirm(s.Challenge, true)
	if err != nil || active.Phase != ACTIVE || active.Route != route || active.Receipt != "host-receipt" {
		t.Fatal("confirmation failed")
	}
	if _, err = active.Confirm(s.Challenge, true); err == nil {
		t.Fatal("replayed activation")
	}
	now = now.Add(10 * time.Minute)
	if _, err = s.RecordTest(s.Challenge, "receipt"); err == nil {
		t.Fatal("expired record")
	}
	if _, err = tested.Confirm(s.Challenge, true); err == nil {
		t.Fatal("expired confirmation")
	}
	if active.Validate() != nil {
		t.Fatal("active auth unexpectedly expires")
	}
	off := active.Off()
	if off != (ControlState{Phase: OFF}) || off.Validate() != nil {
		t.Fatal("off retained authorization")
	}
}

func TestControlValidation(t *testing.T) {
	var absent *ControlState
	if absent.Validate() != nil {
		t.Fatal("legacy nil rejected")
	}
	if _, err := NewControlTest(Route{SessionKey: "s", Channel: "telegram", Target: "-1", AccountID: "a"}); err == nil {
		t.Fatal("missing actual topic accepted")
	}
	route := Route{SessionKey: "s", Channel: "telegram", Target: "-1", ThreadID: "2", AccountID: "a"}
	good, err := NewControlTest(route)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ControlState){
		func(s *ControlState) { s.Phase = "UNKNOWN" },
		func(s *ControlState) { s.Receipt = "unexpected" },
		func(s *ControlState) { s.Route.AccountID = "" },
		func(s *ControlState) { s.Challenge = "" },
		func(s *ControlState) { s.ExpiresAt = time.Time{} },
		func(s *ControlState) { s.Phase = TESTED },
		func(s *ControlState) { s.Phase = ACTIVE },
	} {
		bad := good
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid control accepted")
		}
	}
}
