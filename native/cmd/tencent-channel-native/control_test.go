package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tencent-channel-community/native/notices"
)

func invokeControl(t *testing.T, args ...string) (map[string]any, string, int) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	handled, exit := runControl(args)
	outW.Close()
	errW.Close()
	out, _ := io.ReadAll(outR)
	diagnostic, _ := io.ReadAll(errR)
	outR.Close()
	errR.Close()
	if !handled {
		t.Fatal("command not handled")
	}
	var result map[string]any
	if exit == 0 {
		if json.Unmarshal(out, &result) != nil {
			t.Fatalf("not JSON: %q", out)
		}
		if len(diagnostic) != 0 {
			t.Fatal("success diagnostic")
		}
	} else if len(out) != 0 || !json.Valid(diagnostic) {
		t.Fatal("unsafe failure output")
	}
	return result, string(diagnostic), exit
}

func activateControl(t *testing.T, path string) string {
	t.Helper()
	test, _, exit := invokeControl(t, "route-test", "--state", path, "--session-key", "private-session", "--channel", "telegram", "--target", "-100", "--thread", "674", "--account", "host-account")
	if exit != 0 || test["kind"] != "test" {
		t.Fatal("test request failed")
	}
	challenge := test["challenge"].(string)
	if _, _, exit = invokeControl(t, "record-test", "--state", path, "--challenge", challenge, "--receipt", "real-host-test-receipt"); exit != 0 {
		t.Fatal("record failed")
	}
	if _, _, exit = invokeControl(t, "enable", "--state", path, "--challenge", challenge, "--confirm"); exit != 0 {
		t.Fatal("enable failed")
	}
	return challenge
}

func TestControlCLIFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedicated.json")
	challenge := activateControl(t, path)
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("state not private")
	}
	result, _, exit := invokeControl(t, "status", "--state", path)
	b, _ := json.Marshal(result)
	if exit != 0 || result["phase"] != "ACTIVE" || strings.Contains(string(b), "private-session") || strings.Contains(string(b), challenge) || strings.Contains(string(b), "receipt") {
		t.Fatal("unsafe status")
	}
	if _, _, exit = invokeControl(t, "route-test", "--state", path, "--session-key", "other", "--channel", "telegram", "--target", "-100", "--thread", "2", "--account", "a"); exit == 0 {
		t.Fatal("active route replaced")
	}
	if _, _, exit = invokeControl(t, "off", "--state", path); exit != 0 {
		t.Fatal("off failed")
	}
	store, err := notices.NewStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	store.Close()
	if err != nil || state.Control == nil || *state.Control != (notices.ControlState{Phase: notices.OFF}) {
		t.Fatal("off retained metadata")
	}
	if _, _, exit = invokeControl(t, "once", "--state", path); exit == 0 {
		t.Fatal("off poll allowed")
	}
}

type fixturePoller struct{ called bool }

func (p *fixturePoller) Poll(_ context.Context, s notices.SchemaEnvelope) (notices.SchemaEnvelope, []notices.OwnedDelivery, error) {
	p.called = true
	key := controlRouteKey(s.Control.Route)
	// Fixture engine remains the actual pure engine, with empty synthetic reads.
	engine, err := notices.NewEngine(emptyControlFetcher{}, key)
	if err != nil {
		return s, nil, err
	}
	return engine.Poll(context.Background(), s)
}

type emptyControlFetcher struct{}

func (emptyControlFetcher) ReadInteractions(context.Context, string) ([]map[string]any, error) {
	return []map[string]any{}, nil
}
func (emptyControlFetcher) ReadSystem(context.Context) ([]map[string]any, error) {
	return []map[string]any{}, nil
}
func (emptyControlFetcher) ReadDMSnapshot(context.Context) ([]notices.DMSnapshotEntry, error) {
	return []notices.DMSnapshotEntry{}, nil
}
func (emptyControlFetcher) ReadDMDetails(context.Context, notices.DMRange) ([]map[string]any, error) {
	return []map[string]any{}, nil
}

func TestControlCLIOnceAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedicated.json")
	activateControl(t, path)
	store, err := notices.NewStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	routeKey := controlRouteKey(s.Control.Route)
	s.Deliveries["exact-key"] = notices.DeliveryRecord{RouteKey: routeKey, Source: notices.System, EventKey: "event", State: notices.DeliveryPending, UpdatedAt: s.Control.ExpiresAt}
	if err = store.Save(s); err != nil {
		t.Fatal(err)
	}
	store.Close()
	old := controlEngineFactory
	fixture := &fixturePoller{}
	controlEngineFactory = func(string) (controlPoller, error) { return fixture, nil }
	defer func() { controlEngineFactory = old }()
	out, _, exit := invokeControl(t, "once", "--state", path)
	if exit != 0 || !fixture.called {
		t.Fatal("fixture poll failed")
	}
	box := out["outbox"].([]any)
	if len(box) != 1 {
		t.Fatal("missing pending")
	}
	delivery := box[0].(map[string]any)
	if delivery["delivery_id"] != "exact-key" || delivery["route_key"] != routeKey || delivery["text"] != "system更新" {
		t.Fatal("outbox identity mismatch")
	}
	if _, _, exit = invokeControl(t, "ack", "--state", path, "--key", "wrong", "--receipt", "real-receipt"); exit == 0 {
		t.Fatal("unknown key acknowledged")
	}
	if _, _, exit = invokeControl(t, "ack", "--state", path, "--key", "exact-key", "--receipt", "real-receipt"); exit != 0 {
		t.Fatal("ack failed")
	}
	if _, _, exit = invokeControl(t, "ack", "--state", path, "--key", "exact-key", "--receipt", "real-receipt"); exit == 0 {
		t.Fatal("ack replay allowed")
	}
	out, _, exit = invokeControl(t, "once", "--state", path)
	if exit != 0 || len(out["outbox"].([]any)) != 0 {
		t.Fatal("delivered replayed")
	}
	store, err = notices.NewStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err = store.Load()
	store.Close()
	if err != nil || !s.Initialized[notices.System] || s.Deliveries["exact-key"].Receipt != "real-receipt" {
		t.Fatal("poll/ack not persisted")
	}
}

func TestControlCLIRejectsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	for _, args := range [][]string{
		{"status"}, {"status", "--state", path, "extra"},
		{"status", "--state", path, "--unknown", "private-value"},
		{"status", "--state", path, "--state", path},
		{"enable", "--state", path, "--challenge", "x", "--confirm"},
		{"route-test", "--state", path, "--session-key", "s", "--channel", "telegram", "--target", "-100", "--account", "a"},
	} {
		_, diagnostic, exit := invokeControl(t, args...)
		if exit == 0 || strings.Contains(diagnostic, path) || strings.Contains(diagnostic, "private-value") {
			t.Fatal("malformed command leaked or passed")
		}
	}
	if handled, _ := runControl([]string{"probe"}); handled {
		t.Fatal("probe intercepted")
	}
}

type partialControlFetcher struct{ emptyControlFetcher }

func (partialControlFetcher) ReadSystem(context.Context) ([]map[string]any, error) {
	return nil, notices.ErrInvalid
}
func TestControlCLIIsolatesAndPersistsHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.json")
	activateControl(t, path)
	old := controlEngineFactory
	defer func() { controlEngineFactory = old }()
	controlEngineFactory = func(key string) (controlPoller, error) { return notices.NewEngine(partialControlFetcher{}, key) }
	out, diagnostic, exit := invokeControl(t, "once", "--state", path)
	if exit != 0 || diagnostic != "" {
		t.Fatal("partial poll rejected")
	}
	health, ok := out["health"].(map[string]any)
	if !ok || health["status"] != "degraded" {
		t.Fatal("partial health missing")
	}
	sources := health["sources"].(map[string]any)
	if sources["system"] != "failed" || sources["dm"] != "ok" || sources["interaction"] != "ok" {
		t.Fatal("wrong source labels")
	}
	store, err := notices.NewStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Initialized[notices.Interaction] || !saved.Initialized[notices.DM] || saved.Initialized[notices.System] || saved.Control.Phase != notices.ACTIVE {
		t.Fatal("partial state not durably isolated")
	}
}
