package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"tencent-channel-community/native/notices"
)

// This bounded probe is not an installer, daemon or subscription activation.
// It emits no raw notices, identifiers, credential values or response bodies.
func main() {
	if len(os.Args) != 2 || os.Args[1] != "probe" {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"success": false, "stage": "usage", "message": "use: tencent-channel-native probe"})
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	caller := notices.NewNativeHTTPCaller(notices.DefaultCredential)
	f := notices.Fetcher{Caller: caller}
	counts := map[string]int{}
	interactions, err := f.ReadInteractions(ctx, "")
	if err != nil {
		fail("interaction", err)
	}
	counts["interaction"] = len(interactions)
	system, err := f.ReadSystem(ctx)
	if err != nil {
		fail("system", err)
	}
	counts["system"] = len(system)
	dm, err := f.ReadDMSnapshot(ctx)
	if err != nil {
		fail("dm", err)
	}
	counts["dm_channels"] = len(dm)
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"success": true, "mode": "read_only_probe", "sources": counts, "subscription_enabled": false})
}
func fail(stage string, err error) {
	category := notices.NativeFailureStage(err)
	if errors.Is(err, notices.ErrFetchPayload) {
		category = "response_shape"
	}

	_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"success": false, "stage": stage, "category": category, "message": "native read probe failed; credential and response details withheld"})
	os.Exit(1)
}
