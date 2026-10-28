package notices

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type engineFixture struct {
	interactions, system, details []map[string]any
	snapshot                      []DMSnapshotEntry
	fail                          string
	calls                         int
	ranges                        []DMRange
}

func (f *engineFixture) hit(name string) error {
	f.calls++
	if f.fail == name {
		return errors.New("synthetic read failure")
	}
	return nil
}
func (f *engineFixture) ReadInteractions(context.Context, string) ([]map[string]any, error) {
	return f.interactions, f.hit("interaction")
}
func (f *engineFixture) ReadSystem(context.Context) ([]map[string]any, error) {
	return f.system, f.hit("system")
}
func (f *engineFixture) ReadDMSnapshot(context.Context) ([]DMSnapshotEntry, error) {
	return f.snapshot, f.hit("dm")
}
func (f *engineFixture) ReadDMDetails(_ context.Context, r DMRange) ([]map[string]any, error) {
	f.ranges = append(f.ranges, r)
	return f.details, f.hit("details")
}
func engineState() SchemaEnvelope {
	return SchemaEnvelope{Version: SchemaVersion, Baseline: Baseline{Sources: map[string]*SourceBaseline{}}, Deliveries: map[string]DeliveryRecord{}, Initialized: map[Source]bool{}}
}
func makeEngine(t *testing.T, f *engineFixture) *Engine {
	t.Helper()
	e, err := NewEngine(f, "opaque-route")
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func roundTrip(t *testing.T, s SchemaEnvelope) SchemaEnvelope {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var out SchemaEnvelope
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEngineDurableReplayAck(t *testing.T) {
	ctx := context.Background()
	f := &engineFixture{}
	e := makeEngine(t, f)
	s, _, err := e.Poll(ctx, engineState())
	if err != nil {
		t.Fatal(err)
	}
	f.interactions = []map[string]any{{"createTime": json.Number("9007199254740993"), "private": "secret-content"}}
	next, pending, err := e.Poll(ctx, s)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %v %v", pending, err)
	}
	if len(s.Deliveries) != 0 || s.Baseline.Sources["interaction"].Watermark != 0 {
		t.Fatal("input mutated")
	}
	saved := roundTrip(t, next)
	calls := f.calls
	f.fail = "interaction"
	restart := makeEngine(t, f)
	replayed, again, err := restart.Poll(ctx, saved)
	if err != nil || !reflect.DeepEqual(pending, again) || f.calls != calls {
		t.Fatal("replay must precede fetch")
	}
	b, _ := json.Marshal(replayed)
	if strings.Contains(string(b), "secret-content") || strings.Contains(string(b), "private") {
		t.Fatal("raw event persisted")
	}
	if pending[0].Text != "interaction更新" || len(pending[0].EventKey) != 64 {
		t.Fatal("non-generic delivery")
	}
	for _, args := range [][3]string{{pending[0].Key, "wrong-route", "receipt"}, {"wrong-key", "opaque-route", "receipt"}, {pending[0].Key, "opaque-route", ""}} {
		got, err := restart.AckDelivery(saved, args[0], args[1], args[2])
		if err == nil || !reflect.DeepEqual(got, saved) {
			t.Fatal("invalid ack accepted")
		}
	}
	acked, err := AckDelivery(saved, pending[0].Key, "opaque-route", "real-tool-receipt")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Deliveries[pending[0].Key].State != DeliveryPending {
		t.Fatal("ack mutated input")
	}
	acked = roundTrip(t, acked)
	if got, err := restart.CollectPending(acked); err != nil || len(got) != 0 {
		t.Fatal("acked record replayed")
	}
	f.fail = ""
	_, got, err := restart.Poll(ctx, acked)
	if err != nil || len(got) != 0 {
		t.Fatal("event retried after ack")
	}
	other, _ := NewEngine(f, "other-route")
	if _, _, err = other.Poll(ctx, saved); err == nil {
		t.Fatal("route reassignment accepted")
	}
}

func TestEngineIndependentColdSourcesAndDMComposite(t *testing.T) {
	ctx := context.Background()
	f := &engineFixture{interactions: []map[string]any{{"createTime": int64(10)}}, system: []map[string]any{{"createTs": "10"}}, snapshot: []DMSnapshotEntry{{DMKey{"guild", "a"}, "9007199254740993", "10"}, {DMKey{"guild", "b"}, "1", "10"}}}
	e := makeEngine(t, f)
	s, p, err := e.Poll(ctx, engineState())
	if err != nil || len(p) != 0 || len(f.ranges) != 0 {
		t.Fatal("cold history emitted", err)
	}
	// An independently cold system is silent while warm interaction and DM notify.
	delete(s.Initialized, System)
	f.interactions = append(f.interactions, map[string]any{"create_time": "11"})
	f.system = append(f.system, map[string]any{"createTs": "11"})
	f.snapshot = []DMSnapshotEntry{{DMKey{"guild", "a"}, "9007199254740994", "11"}, {DMKey{"guild", "b"}, "1", "10"}, {DMKey{"new", "a"}, "999", "11"}}
	f.details = []map[string]any{{"msgSeq": "9007199254740994", "msgTime": "11"}}
	next, p, err := e.Poll(ctx, s)
	if err != nil || len(p) != 3 {
		t.Fatalf("warm/cold sources %v %v", p, err)
	}
	if len(f.ranges) != 1 || f.ranges[0].Key != (DMKey{"guild", "a"}) || f.ranges[0].BeginSeq != "9007199254740994" {
		t.Fatal("composite/large seq planning")
	}
	if len(next.Baseline.DM.Channels) != 3 || next.Baseline.DM.Channels[0].LastSeq != "9007199254740994" {
		t.Fatal("DM cursor not committed")
	}
}

func TestEngineAtomicFailureAndRetry(t *testing.T) {
	ctx := context.Background()
	f := &engineFixture{snapshot: []DMSnapshotEntry{{DMKey{"g", "c"}, "1", "1"}}}
	e := makeEngine(t, f)
	s, _, err := e.Poll(ctx, engineState())
	if err != nil {
		t.Fatal(err)
	}
	f.interactions = []map[string]any{{"createTime": "2"}}
	f.snapshot[0].Seq = "2"
	f.snapshot[0].Time = "2"
	f.details = []map[string]any{{"msgSeq": "2", "msgTime": "2"}}
	before := roundTrip(t, s)
	for _, failure := range []string{"interaction", "system", "dm", "details"} {
		f.fail = failure
		got, p, err := e.Poll(ctx, s)
		if err == nil || len(p) != 0 || !reflect.DeepEqual(s, got) || !reflect.DeepEqual(s, before) {
			t.Fatalf("non-atomic %s", failure)
		}
	}
	f.fail = ""
	got, p, err := e.Poll(ctx, s)
	if err != nil || len(p) != 2 || got.Baseline.DM.Channels[0].LastSeq != "2" {
		t.Fatal("failed cursor not retried", err)
	}
}

func TestEngineTimestampStrictAndColdZero(t *testing.T) {
	for _, v := range []any{nil, 0, int64(-1), float64(12), "0", "-1", "+1", "1.5", " 1", json.Number("1e3"), uint64(1) << 63} {
		if _, ok := positiveStamp(v); ok {
			t.Fatalf("accepted %v", v)
		}
	}
	for _, v := range []any{int(1), uint64(3), json.Number("9007199254740993"), "4"} {
		if _, ok := positiveStamp(v); !ok {
			t.Fatalf("rejected %v", v)
		}
	}
	nested := map[string]any{"psvFeed": map[string]any{"create_time": "17"}, "createTime": "18"}
	if stamp, err := itemStamp(Interaction, nested); err != nil || stamp != 17 {
		t.Fatal("native timestamp precedence")
	}
	ctx := context.Background()
	f := &engineFixture{interactions: []map[string]any{{"createTime": "0"}}}
	e := makeEngine(t, f)
	s := engineState()
	got, p, err := e.Poll(ctx, s)
	if err == nil || len(p) != 0 || !reflect.DeepEqual(s, got) {
		t.Fatal("zero timestamp initialized source")
	}
	f.interactions = nil
	s, _, err = e.Poll(ctx, s)
	if err != nil || !s.Initialized[Interaction] || s.Baseline.Sources["interaction"].Watermark != 0 {
		t.Fatal("empty cold source not initialized")
	}
	f.interactions = []map[string]any{{"createTime": "1"}}
	_, p, err = e.Poll(ctx, s)
	if err != nil || len(p) != 1 {
		t.Fatal("zero watermark treated as cold")
	}
}

func TestEngineLedgerBoundAndMalformedDM(t *testing.T) {
	ctx := context.Background()
	f := &engineFixture{snapshot: []DMSnapshotEntry{{DMKey{"g", "c"}, "1", "1"}}}
	e := makeEngine(t, f)
	s, _, err := e.Poll(ctx, engineState())
	if err != nil {
		t.Fatal(err)
	}
	f.snapshot[0].Seq = "2"
	f.snapshot[0].Time = "2"
	for _, item := range []map[string]any{{"msgSeq": "0", "msgTime": "2"}, {"msgSeq": "2", "msgTime": "0"}, {"msgSeq": "3", "msgTime": "2"}} {
		f.details = []map[string]any{item}
		got, p, err := e.Poll(ctx, s)
		if err == nil || len(p) != 0 || !reflect.DeepEqual(got, s) {
			t.Fatal("invalid DM advanced")
		}
	}
	f.details = []map[string]any{{"msgSeq": "2", "msgTime": "2", "text": ""}}
	s, p, err := e.Poll(ctx, s)
	if err != nil || len(p) != 0 || s.Baseline.DM.Channels[0].LastSeq != "2" {
		t.Fatal("empty-text cursor handling")
	}
	for i := 0; i < MaxLedger; i++ {
		key, _ := opaque(i)
		s.Deliveries[key] = DeliveryRecord{RouteKey: "opaque-route", Source: System, EventKey: key, State: DeliveryDelivered, Receipt: "receipt", UpdatedAt: time.Unix(1, 0).UTC()}
	}
	f.system = []map[string]any{{"createTs": "3"}}
	got, p, err := e.Poll(ctx, s)
	if err == nil || len(p) != 0 || !reflect.DeepEqual(got, s) || len(s.Deliveries) != MaxLedger {
		t.Fatal("ledger silently evicted")
	}
}

func TestWarmNewDMChannelNotifiesWithoutHistory(t *testing.T) {
	ctx := context.Background()
	f := &engineFixture{}
	e := makeEngine(t, f)
	state, pending, err := e.Poll(ctx, engineState())
	if err != nil || len(pending) != 0 {
		t.Fatal("cold baseline failed", err)
	}
	f.snapshot = []DMSnapshotEntry{{DMKey{"new-guild", "new-channel"}, "1", "10"}}
	updated, pending, err := e.Poll(ctx, state)
	if err != nil || len(pending) != 1 || pending[0].Source != DM || len(f.ranges) != 0 {
		t.Fatal("first channel update silently lost or history fetched", err)
	}
	if len(state.Deliveries) != 0 {
		t.Fatal("input mutated")
	}
	acked, err := e.AckDelivery(updated, pending[0].Key, "opaque-route", "tool-receipt")
	if err != nil {
		t.Fatal(err)
	}
	_, again, err := e.Poll(ctx, acked)
	if err != nil || len(again) != 0 || len(f.ranges) != 0 {
		t.Fatal("new channel notice repeated", err)
	}
}
func TestColdDMChannelsRemainSilent(t *testing.T) {
	f := &engineFixture{snapshot: []DMSnapshotEntry{{DMKey{"old", "channel"}, "999", "10"}}}
	_, pending, err := makeEngine(t, f).Poll(context.Background(), engineState())
	if err != nil || len(pending) != 0 || len(f.ranges) != 0 {
		t.Fatal("cold history emitted", err)
	}
}
