package notices

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Synthetic protocol integration, not proof of a real Tencent event or delivery.
// Covers owned HTTP validation -> real parsers -> engine -> durable store -> ack.
func TestOwnedNativeProtocolPipeline(t *testing.T) {
	phase, calls := 0, 0
	failSystem := false
	caller := newNativeHTTPCaller(func(context.Context) (string, error) { return "synthetic-token", nil }, nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		var rpc struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if json.NewDecoder(req.Body).Decode(&rpc) != nil {
			t.Fatal("request envelope")
		}
		payload := map[string]any{}
		switch rpc.Params.Name {
		case "get_interact_notice":
			payload = map[string]any{"attachInfo": "synthetic-opaque-cursor", "isFinish": true}
			if phase > 0 {
				payload = map[string]any{"notices": []any{map[string]any{"createTime": "100", "body": "synthetic-private-content"}}}
			}
		case "get_notice_list":
			if failSystem {
				return nil, ErrInvalid
			}
			if phase > 0 {
				payload = map[string]any{"noticeList": []any{map[string]any{"createTs": "101", "noticeId": "synthetic"}}}
			}
		case "query_normal_dm_list":
			if phase > 0 {
				payload = map[string]any{"rptMyDirectMessageInfo": []any{map[string]any{"guildId": "1", "channelId": "2", "lastestMsgSeq": map[string]any{"uint64Seq": "1", "uint64Time": "100"}}}}
			}
		default:
			t.Fatal("unexpected protocol capability")
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"_meta": map[string]any{"AdditionalFields": map[string]any{"retCode": 0}}, "structuredContent": payload}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	}))
	engine, err := NewEngine(Fetcher{Caller: caller}, "synthetic-route")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStateStore(filepath.Join(t.TempDir(), "new-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	initial, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	cold, pending, err := engine.Poll(context.Background(), initial)
	if err != nil || len(pending) != 0 {
		t.Fatal("cold initialization", err)
	}
	if err = store.Save(cold); err != nil {
		t.Fatal(err)
	}
	phase = 1
	failSystem = true
	unchanged, pending, err := engine.Poll(context.Background(), cold)
	if err == nil || len(pending) != 0 || !reflect.DeepEqual(unchanged, cold) {
		t.Fatal("failed poll advanced or emitted")
	}
	failSystem = false
	next, pending, err := engine.Poll(context.Background(), cold)
	if err != nil || len(pending) != 3 {
		t.Fatal("three-source pipeline", err, len(pending))
	}
	if err = store.Save(next); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), "synthetic-private-content") || strings.Contains(string(raw), "synthetic-token") {
		t.Fatal("private content persisted")
	}
	before := calls
	replayed, again, err := engine.Poll(context.Background(), saved)
	if err != nil || len(again) != 3 || calls != before {
		t.Fatal("durable replay performed network or lost pending")
	}
	for _, item := range again {
		replayed, err = engine.AckDelivery(replayed, item.Key, "synthetic-route", "synthetic-test-only-receipt")
		if err != nil {
			t.Fatal(err)
		}
		if err = store.Save(replayed); err != nil {
			t.Fatal(err)
		}
	}
	final, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, pending, err = engine.Poll(context.Background(), final)
	if err != nil || len(pending) != 0 {
		t.Fatal("acknowledged event repeated", err)
	}
}
