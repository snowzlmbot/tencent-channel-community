package notices

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func isolatedWarm(t *testing.T, f *engineFixture) (*Engine, SchemaEnvelope) {
	t.Helper()
	e := makeEngine(t, f)
	s, pending, health, err := e.PollIsolated(context.Background(), engineState())
	if err != nil || len(pending) != 0 || health.Status != "healthy" {
		t.Fatalf("cold initialization: %v %v %v", pending, health, err)
	}
	return e, s
}

func isolatedAck(t *testing.T, e *Engine, s SchemaEnvelope, pending []OwnedDelivery) SchemaEnvelope {
	t.Helper()
	for _, d := range pending {
		var err error
		s, err = e.AckDelivery(s, d.Key, d.RouteKey, "fixture-receipt")
		if err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func assertIsolatedHealth(t *testing.T, got PollHealth, status, interaction, system, dm string) {
	t.Helper()
	want := PollHealth{Status: status, Sources: map[Source]string{Interaction: interaction, System: system, DM: dm}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("health = %#v; want %#v", got, want)
	}
}

func TestIsolatedSystemFailureAndRecovery(t *testing.T) {
	f := &engineFixture{snapshot: []DMSnapshotEntry{{DMKey{"g", "c"}, "1", "1"}}}
	e, s := isolatedWarm(t, f)
	before, _ := isolatedCopy(s)
	f.fail = "system"
	f.interactions = []map[string]any{{"createTime": "2"}}
	f.system = []map[string]any{{"createTs": "2"}}
	f.snapshot = []DMSnapshotEntry{{DMKey{"g", "c"}, "2", "2"}}
	f.details = []map[string]any{{"msgSeq": "2", "msgTime": "2"}}
	next, pending, health, err := e.PollIsolated(context.Background(), s)
	if err != nil || len(pending) != 2 || !reflect.DeepEqual(s, before) {
		t.Fatalf("healthy sources blocked or input changed: %v %v", pending, err)
	}
	assertIsolatedHealth(t, health, "degraded", "ok", "failed", "ok")
	if !reflect.DeepEqual(next.Baseline.Sources["system"], s.Baseline.Sources["system"]) || next.Baseline.DM.Channels[0].LastSeq != "2" {
		t.Fatal("failed source advanced or healthy DM lost")
	}
	next = isolatedAck(t, e, next, pending)
	f.fail = ""
	recovered, pending, health, err := e.PollIsolated(context.Background(), next)
	if err != nil || len(pending) != 1 || pending[0].Source != System || recovered.Baseline.Sources["system"].Watermark != 2 {
		t.Fatal("failed system did not catch up", err)
	}
	assertIsolatedHealth(t, health, "healthy", "ok", "ok", "ok")
}

func TestIsolatedMalformedSourceKeepsWholeCheckpoint(t *testing.T) {
	f := &engineFixture{interactions: []map[string]any{{"createTime": "1"}}}
	e, s := isolatedWarm(t, f)
	s.Baseline.Sources["interaction"].OldIDs = []string{}
	s.Baseline.Sources["interaction"].RecentNotices = []RecentNotice{{Ref: 7, Summary: "retained metadata"}}
	s.Baseline.Sources["interaction"].AtWatermark = append(s.Baseline.Sources["interaction"].AtWatermark, "retained-boundary")
	before, _ := isolatedCopy(s)
	f.interactions = []map[string]any{{"createTime": "2"}, {"createTime": "bad"}}
	f.system = []map[string]any{{"createTs": "2"}}
	next, pending, health, err := e.PollIsolated(context.Background(), s)
	if err != nil || len(pending) != 1 || pending[0].Source != System || !reflect.DeepEqual(s, before) {
		t.Fatal("malformed isolation/input", err)
	}
	assertIsolatedHealth(t, health, "degraded", "failed", "ok", "ok")
	if !reflect.DeepEqual(next.Baseline.Sources["interaction"], s.Baseline.Sources["interaction"]) || next.Initialized[Interaction] != s.Initialized[Interaction] {
		t.Fatal("failed checkpoint rewritten")
	}
}

func TestIsolatedColdFlagsRecoverIndependently(t *testing.T) {
	f := &engineFixture{fail: "system", interactions: []map[string]any{{"createTime": "10"}}, system: []map[string]any{{"createTs": "10"}}}
	e := makeEngine(t, f)
	s, pending, health, err := e.PollIsolated(context.Background(), engineState())
	if err != nil || len(pending) != 0 || s.Initialized[System] || !s.Initialized[Interaction] || !s.Initialized[DM] || s.Baseline.Sources["system"] != nil {
		t.Fatal("cold flags crossed source boundary", err)
	}
	assertIsolatedHealth(t, health, "degraded", "ok", "failed", "ok")
	f.fail = ""
	f.interactions = append(f.interactions, map[string]any{"createTime": "11"})
	next, pending, health, err := e.PollIsolated(context.Background(), s)
	if err != nil || len(pending) != 1 || pending[0].Source != Interaction || !next.Initialized[System] {
		t.Fatal("cold recovery emitted historical system data", err)
	}
	assertIsolatedHealth(t, health, "healthy", "ok", "ok", "ok")
}

type isolatedFetcher struct {
	*engineFixture
	allFail     bool
	detailCalls int
	cancelOn    string
	cancel      context.CancelFunc
}

func (f *isolatedFetcher) ReadInteractions(ctx context.Context, token string) ([]map[string]any, error) {
	if f.allFail {
		f.calls++
		return nil, errors.New("private interaction failure")
	}
	items, err := f.engineFixture.ReadInteractions(ctx, token)
	if f.cancelOn == "interaction" {
		f.cancel()
	}
	return items, err
}
func (f *isolatedFetcher) ReadSystem(ctx context.Context) ([]map[string]any, error) {
	if f.allFail {
		f.calls++
		return nil, errors.New("private system failure")
	}
	return f.engineFixture.ReadSystem(ctx)
}
func (f *isolatedFetcher) ReadDMSnapshot(ctx context.Context) ([]DMSnapshotEntry, error) {
	if f.allFail {
		f.calls++
		return nil, errors.New("private DM failure")
	}
	return f.engineFixture.ReadDMSnapshot(ctx)
}
func (f *isolatedFetcher) ReadDMDetails(_ context.Context, r DMRange) ([]map[string]any, error) {
	f.detailCalls++
	f.calls++
	if f.detailCalls == 2 {
		return nil, errors.New("private second channel failure")
	}
	return []map[string]any{{"msgSeq": r.EndSeq, "msgTime": "2"}}, nil
}

func TestIsolatedPartialDMDetailsRollBackAllDMWork(t *testing.T) {
	base := &engineFixture{snapshot: []DMSnapshotEntry{{DMKey{"g", "a"}, "1", "1"}, {DMKey{"g", "b"}, "1", "1"}}}
	f := &isolatedFetcher{engineFixture: base}
	e, _ := NewEngine(f, "opaque-route")
	s, _, _, err := e.PollIsolated(context.Background(), engineState())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := isolatedCopy(s)
	base.snapshot = []DMSnapshotEntry{{DMKey{"g", "a"}, "2", "2"}, {DMKey{"g", "b"}, "2", "2"}, {DMKey{"g", "new"}, "4", "2"}}
	base.interactions = []map[string]any{{"createTime": "2"}}
	next, pending, health, err := e.PollIsolated(context.Background(), s)
	if err != nil || f.detailCalls != 2 || len(pending) != 1 || pending[0].Source != Interaction || !reflect.DeepEqual(s, before) {
		t.Fatal("partial DM isolation", err)
	}
	assertIsolatedHealth(t, health, "degraded", "ok", "ok", "failed")
	if !reflect.DeepEqual(next.Baseline.DM, s.Baseline.DM) || next.Initialized[DM] != s.Initialized[DM] {
		t.Fatal("partial DM cursor committed")
	}
	for _, d := range next.Deliveries {
		if d.Source == DM {
			t.Fatal("partial DM or new-channel delivery escaped rollback")
		}
	}
	next = isolatedAck(t, e, next, pending)
	_, pending, health, err = e.PollIsolated(context.Background(), next)
	if err != nil || len(pending) != 3 {
		t.Fatal("DM recovery failed to catch up both old and new channels", err)
	}
	assertIsolatedHealth(t, health, "healthy", "ok", "ok", "ok")
}

func TestIsolatedReplayPrecedesFetchAndCancellation(t *testing.T) {
	f := &engineFixture{}
	e, s := isolatedWarm(t, f)
	f.system = []map[string]any{{"createTs": "1"}}
	s, want, _, err := e.PollIsolated(context.Background(), s)
	if err != nil || len(want) != 1 {
		t.Fatal("create pending", err)
	}
	calls := f.calls
	f.fail = "interaction"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	next, got, health, err := e.PollIsolated(ctx, s)
	if err != nil || !reflect.DeepEqual(next, s) || !reflect.DeepEqual(got, want) || f.calls != calls {
		t.Fatal("pending replay fetched or changed state", err)
	}
	assertIsolatedHealth(t, health, "replay", "deferred", "deferred", "deferred")
}

func TestIsolatedAllFailuresReturnOriginalGenericError(t *testing.T) {
	f := &isolatedFetcher{engineFixture: &engineFixture{}, allFail: true}
	e, _ := NewEngine(f, "opaque-route")
	s := engineState()
	next, pending, health, err := e.PollIsolated(context.Background(), s)
	if err != ErrInvalid || pending != nil || !reflect.DeepEqual(next, s) || f.calls != 3 {
		t.Fatal("all failures changed state or exposed upstream error", err)
	}
	assertIsolatedHealth(t, health, "degraded", "failed", "failed", "failed")
}

func TestIsolatedContextStopsRemainingSources(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &isolatedFetcher{engineFixture: &engineFixture{}, cancel: cancel}
			if partial {
				f.cancelOn = "interaction"
			} else {
				cancel()
			}
			e, _ := NewEngine(f, "opaque-route")
			s := engineState()
			next, pending, health, err := e.PollIsolated(ctx, s)
			if len(pending) != 0 {
				t.Fatal("cold context emitted history")
			}
			if partial {
				if err != nil || f.calls != 1 || !next.Initialized[Interaction] || next.Initialized[System] || next.Initialized[DM] {
					t.Fatal("successful source lost or deferred source fetched", err)
				}
				assertIsolatedHealth(t, health, "degraded", "ok", "deferred", "deferred")
			} else {
				if err != ErrInvalid || f.calls != 0 || !reflect.DeepEqual(next, s) {
					t.Fatal("cancelled poll changed state or fetched", err)
				}
				assertIsolatedHealth(t, health, "degraded", "deferred", "deferred", "deferred")
			}
		})
	}
}

func TestIsolatedControlRouteAndInvalidInput(t *testing.T) {
	f := &engineFixture{}
	e := makeEngine(t, f)
	s := engineState()
	s.Control = &ControlState{Phase: OFF}
	next, _, _, err := e.PollIsolated(context.Background(), s)
	if err != nil || !reflect.DeepEqual(next.Control, s.Control) || next.Control == s.Control {
		t.Fatal("control not preserved/detached", err)
	}
	key, _ := opaque("foreign")
	s.Deliveries[key] = DeliveryRecord{RouteKey: "foreign-route", Source: System, EventKey: key, State: DeliveryPending, UpdatedAt: time.Unix(1, 0)}
	calls := f.calls
	got, pending, _, err := e.PollIsolated(context.Background(), s)
	if err != ErrInvalid || len(pending) != 0 || !reflect.DeepEqual(got, s) || f.calls != calls {
		t.Fatal("foreign route accepted")
	}
	s = engineState()
	s.Version++
	got, pending, _, err = e.PollIsolated(context.Background(), s)
	if err != ErrInvalid || len(pending) != 0 || !reflect.DeepEqual(got, s) || f.calls != calls {
		t.Fatal("invalid state fetched")
	}
}

func isolatedFillDelivered(s *SchemaEnvelope, count int) {
	for i := 0; i < count; i++ {
		key, _ := opaque("isolated-receipt", i)
		s.Deliveries[key] = DeliveryRecord{RouteKey: "opaque-route", Source: System, EventKey: key, State: DeliveryDelivered, Receipt: "receipt", UpdatedAt: time.Unix(int64(i+1), 0)}
	}
}

func TestIsolatedCompactionAndFatalProvenance(t *testing.T) {
	f := &engineFixture{system: []map[string]any{{"createTs": "1"}}}
	e, s := isolatedWarm(t, f)
	isolatedFillDelivered(&s, MaxLedger)
	f.fail = "interaction"
	f.system = append(f.system, map[string]any{"createTs": "2"})
	next, pending, health, err := e.PollIsolated(context.Background(), s)
	if err != nil || len(pending) != 1 || len(next.Deliveries) != MaxDeliveredRetained+1 || len(s.Deliveries) != MaxLedger {
		t.Fatal("compaction/partial progress failed", err)
	}
	assertIsolatedHealth(t, health, "degraded", "failed", "ok", "ok")
	delete(s.Initialized, System)
	calls := f.calls
	got, pending, _, err := e.PollIsolated(context.Background(), s)
	if err != ErrInvalid || pending != nil || !reflect.DeepEqual(got, s) || f.calls != calls {
		t.Fatal("retention provenance failure was not globally fatal")
	}
}

func TestIsolatedGlobalCapacityRollsBackOnlyExhaustingSource(t *testing.T) {
	f := &engineFixture{system: []map[string]any{{"createTs": "1"}}}
	e, s := isolatedWarm(t, f)
	isolatedFillDelivered(&s, MaxDeliveredRetained)
	for i := 1; i <= MaxLedger-MaxDeliveredRetained; i++ {
		f.interactions = append(f.interactions, map[string]any{"createTime": fmt.Sprint(i)})
	}
	f.system = append(f.system, map[string]any{"createTs": "2"})
	next, pending, health, err := e.PollIsolated(context.Background(), s)
	if err != nil || len(next.Deliveries) != MaxLedger || len(pending) != MaxLedger-MaxDeliveredRetained || !reflect.DeepEqual(next.Baseline.Sources["system"], s.Baseline.Sources["system"]) {
		t.Fatal("global capacity not enforced or source rollback failed", err)
	}
	assertIsolatedHealth(t, health, "degraded", "ok", "failed", "ok")
	if len(s.Deliveries) != MaxDeliveredRetained {
		t.Fatal("capacity poll mutated input")
	}
}

func TestIsolatedExpiredDeadlineDoesNotFetch(t *testing.T) {
	f := &engineFixture{}
	e := makeEngine(t, f)
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	s := engineState()
	next, pending, health, err := e.PollIsolated(ctx, s)
	if err != ErrInvalid || len(pending) != 0 || f.calls != 0 || !reflect.DeepEqual(next, s) {
		t.Fatal("expired deadline fetched or changed state")
	}
	assertIsolatedHealth(t, health, "degraded", "deferred", "deferred", "deferred")
}

func TestIsolatedAllFailuresDiscardCompaction(t *testing.T) {
	base := &engineFixture{system: []map[string]any{{"createTs": "1"}}}
	_, s := isolatedWarm(t, base)
	isolatedFillDelivered(&s, MaxLedger)
	f := &isolatedFetcher{engineFixture: base, allFail: true}
	e, _ := NewEngine(f, "opaque-route")
	next, pending, health, err := e.PollIsolated(context.Background(), s)
	if err != ErrInvalid || pending != nil || !reflect.DeepEqual(next, s) || len(s.Deliveries) != MaxLedger {
		t.Fatal("all failures leaked compacted candidate")
	}
	assertIsolatedHealth(t, health, "degraded", "failed", "failed", "failed")
}
