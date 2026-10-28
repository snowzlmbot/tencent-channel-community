package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"tencent-channel-community/native/notices"
)

type controlPoller interface {
	Poll(context.Context, notices.SchemaEnvelope) (notices.SchemaEnvelope, []notices.OwnedDelivery, error)
}

// Test seam: production constructs only the read-only native caller. No send
// capability, daemon, shell command, or messaging RPC exists in this CLI.
var controlEngineFactory = func(routeKey string) (controlPoller, error) {
	return notices.NewEngine(notices.Fetcher{Caller: notices.NewNativeHTTPCaller(notices.DefaultCredential)}, routeKey)
}

func controlRouteKey(route notices.Route) string {
	// Fixed field order and JSON encoding define the canonical route identity.
	b, _ := json.Marshal(route)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func controlError() (bool, int) {
	_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"success": false, "message": "control command rejected; input and state details withheld"})
	return true, 1
}

func runControl(args []string) (handled bool, exit int) {
	if len(args) == 0 {
		return false, 0
	}
	command := args[0]
	switch command {
	case "route-test", "record-test", "enable", "once", "ack", "status", "off":
	default:
		return false, 0
	}
	fs := flag.NewFlagSet("control", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("state", "", "dedicated new private state file")
	var session, channel, target, thread, account, challenge, receipt, key string
	var explicit bool
	switch command {
	case "route-test":
		fs.StringVar(&session, "session-key", "", "trusted host session")
		fs.StringVar(&channel, "channel", "", "trusted host channel")
		fs.StringVar(&target, "target", "", "trusted host destination")
		fs.StringVar(&thread, "thread", "", "trusted host topic")
		fs.StringVar(&account, "account", "", "trusted host account")
	case "record-test", "enable":
		fs.StringVar(&challenge, "challenge", "", "test challenge")
		if command == "record-test" {
			fs.StringVar(&receipt, "receipt", "", "actual host delivery receipt")
		} else {
			fs.BoolVar(&explicit, "confirm", false, "explicit user confirmation")
		}
	case "ack":
		fs.StringVar(&key, "key", "", "exact delivery_id from outbox")
		fs.StringVar(&receipt, "receipt", "", "actual host delivery receipt")
	}
	// Reject duplicate flags (including --confirm=false --confirm), not just
	// positional arguments and unknown options. Never print parser diagnostics.
	seen := map[string]bool{}
	for _, arg := range args[1:] {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name := strings.SplitN(strings.TrimLeft(arg, "-"), "=", 2)[0]
		if seen[name] {
			return controlError()
		}
		seen[name] = true
	}
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || *path == "" {
		return controlError()
	}
	store, err := notices.NewStateStore(*path)
	if err != nil {
		return controlError()
	}
	defer store.Close()
	state, err := store.Load()
	if err != nil {
		return controlError()
	}
	control := notices.ControlState{Phase: notices.OFF}
	if state.Control != nil {
		control = *state.Control
	}
	result := map[string]any{"success": true}
	save := false
	switch command {
	case "route-test":
		if control.Phase == notices.ACTIVE {
			return controlError()
		}
		route := notices.Route{SessionKey: session, Channel: channel, Target: target, ThreadID: thread, AccountID: account}
		// Old route ledgers cannot be silently rebound or dropped, even after off.
		for _, d := range state.Deliveries {
			if d.RouteKey != controlRouteKey(route) {
				return controlError()
			}
		}
		control, err = notices.NewControlTest(route)
		result = map[string]any{"success": true, "kind": "test", "route": route, "challenge": control.Challenge, "text": "通知路由测试，请确认这是预期的接收位置。"}
		save = true
	case "record-test":
		control, err = control.RecordTest(challenge, receipt)
		result["phase"], save = control.Phase, true
	case "enable":
		control, err = control.Confirm(challenge, explicit)
		result["phase"], save = control.Phase, true
	case "off":
		control = control.Off()
		result["phase"], save = control.Phase, true
	case "status":
		initialized, pending := 0, 0
		for _, ready := range state.Initialized {
			if ready {
				initialized++
			}
		}
		for _, d := range state.Deliveries {
			if d.State == notices.DeliveryPending {
				pending++
			}
		}
		result["phase"], result["initialized"], result["pending"] = control.Phase, initialized, pending
	case "ack":
		if control.Phase != notices.ACTIVE || control.Validate() != nil {
			return controlError()
		}
		state, err = notices.AckDelivery(state, key, controlRouteKey(control.Route), receipt)
		result["acknowledged"], save = err == nil, true
	case "once":
		if control.Phase != notices.ACTIVE || control.Validate() != nil {
			return controlError()
		}
		engine, factoryErr := controlEngineFactory(controlRouteKey(control.Route))
		if factoryErr != nil || engine == nil {
			return controlError()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		var deliveries []notices.OwnedDelivery
		if isolated, ok := engine.(interface {
			PollIsolated(context.Context, notices.SchemaEnvelope) (notices.SchemaEnvelope, []notices.OwnedDelivery, notices.PollHealth, error)
		}); ok {
			var health notices.PollHealth
			state, deliveries, health, err = isolated.PollIsolated(ctx, state)
			result["health"] = health
		} else {
			state, deliveries, err = engine.Poll(ctx, state)
		}
		outbox := make([]map[string]any, 0, len(deliveries))
		for _, d := range deliveries {
			outbox = append(outbox, map[string]any{"delivery_id": d.Key, "route_key": d.RouteKey, "source": d.Source, "event_key": d.EventKey, "text": d.Text})
		}
		result["route"], result["outbox"], save = control.Route, outbox, true
	}
	if err != nil {
		return controlError()
	}
	if save {
		state.Control = &control
		if store.Save(state) != nil {
			return controlError()
		}
	}
	// Durable commit always precedes test/outbox exposure. Receipt recording is
	// a separate invocation after the host's message tool succeeds.
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		return controlError()
	}
	return true, 0
}
