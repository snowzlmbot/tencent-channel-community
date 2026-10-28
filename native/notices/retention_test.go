package notices

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func retentionState(delivered, pending int) SchemaEnvelope {
	s := engineState()
	s.Initialized[Interaction] = true
	s.Baseline.Sources[string(Interaction)] = &SourceBaseline{Watermark: 10, AtWatermark: []string{"watermark-event"}}
	for i := 0; i < delivered+pending; i++ {
		key := fmt.Sprintf("ledger-%04d", i)
		d := DeliveryRecord{RouteKey: "opaque-route", Source: Interaction, EventKey: fmt.Sprintf("event-%04d", i), State: DeliveryDelivered, Receipt: "real-receipt", UpdatedAt: time.Unix(int64(i+1), 0).UTC()}
		if i >= delivered {
			d.State, d.Receipt = DeliveryPending, ""
		}
		s.Deliveries[key] = d
	}
	return s
}

func TestCompactDeliveredFullLedger(t *testing.T) {
	s := retentionState(MaxLedger, 0)
	before := roundTrip(t, s)
	out, err := CompactDelivered(s, "opaque-route")
	if err != nil || len(out.Deliveries) != MaxDeliveredRetained {
		t.Fatalf("compaction: records=%d err=%v", len(out.Deliveries), err)
	}
	for i := 0; i < MaxLedger; i++ {
		_, found := out.Deliveries[fmt.Sprintf("ledger-%04d", i)]
		if found != (i >= MaxLedger-MaxDeliveredRetained) {
			t.Fatalf("wrong retention for record %d", i)
		}
	}
	if !reflect.DeepEqual(s, before) {
		t.Fatal("input mutated")
	}
	out.Baseline.Sources[string(Interaction)].AtWatermark[0] = "changed"
	out.Initialized[Interaction] = false
	delete(out.Deliveries, "ledger-0999")
	if !reflect.DeepEqual(s, before) {
		t.Fatal("output aliases input")
	}
}

func TestCompactDeliveredPreservesPendingAndOtherFields(t *testing.T) {
	for _, pendingNewest := range []bool{false, true} {
		s := retentionState(800, 200)
		s.Control = &ControlState{Phase: OFF}
		s.Baseline.MyTinyID = "self"
		s.Baseline.NextRef = 7
		s.Baseline.LegacyIDs = []string{"legacy"}
		s.Baseline.Sources[string(Interaction)].RecentNotices = []RecentNotice{{Ref: 1, Summary: "unchanged"}}
		s.Baseline.Sources[string(Interaction)].OldIDs = []string{}
		s.Baseline.Sources[string(System)] = &SourceBaseline{AtWatermark: []string{}, RecentNotices: []RecentNotice{}, OldIDs: []string{}}
		for k, d := range s.Deliveries {
			if d.State == DeliveryPending && !pendingNewest {
				d.UpdatedAt = time.Unix(1, 0).UTC()
				s.Deliveries[k] = d
			}
		}
		before := roundTrip(t, s)
		before.Baseline.Sources[string(Interaction)].OldIDs = []string{}
		before.Baseline.Sources[string(System)] = &SourceBaseline{AtWatermark: []string{}, RecentNotices: []RecentNotice{}, OldIDs: []string{}}
		out, err := CompactDelivered(s, "opaque-route")
		if err != nil || len(out.Deliveries) != 200+MaxDeliveredRetained {
			t.Fatalf("pending compaction: %v", err)
		}
		for k, d := range s.Deliveries {
			if d.State == DeliveryPending && out.Deliveries[k] != d {
				t.Fatalf("pending record changed: %s", k)
			}
		}
		out.Deliveries = before.Deliveries
		if !reflect.DeepEqual(out, before) || !reflect.DeepEqual(s, before) {
			t.Fatal("non-ledger fields changed")
		}
	}
}

func TestCompactDeliveredDeterministicTies(t *testing.T) {
	s := retentionState(300, 0)
	for k, d := range s.Deliveries {
		d.UpdatedAt = time.Unix(1, 0).UTC()
		s.Deliveries[k] = d
	}
	var previous []byte
	for n := 0; n < 5; n++ {
		out, err := CompactDelivered(s, "opaque-route")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 300; i++ {
			_, found := out.Deliveries[fmt.Sprintf("ledger-%04d", i)]
			if found != (i >= 44) {
				t.Fatalf("tie ordering at %d", i)
			}
		}
		b, _ := json.Marshal(out)
		if n > 0 && string(b) != string(previous) {
			t.Fatal("nondeterministic retention")
		}
		previous = b
	}
}

func TestCompactDeliveredFailClosed(t *testing.T) {
	cases := map[string]func(*SchemaEnvelope){
		"mixed route": func(s *SchemaEnvelope) {
			d := s.Deliveries["ledger-0000"]
			d.RouteKey = "other"
			s.Deliveries["ledger-0000"] = d
		},
		"unknown source": func(s *SchemaEnvelope) {
			d := s.Deliveries["ledger-0000"]
			d.Source = Source("unknown")
			s.Deliveries["ledger-0000"] = d
		},
		"cold source":            func(s *SchemaEnvelope) { s.Initialized[Interaction] = false },
		"missing checkpoint":     func(s *SchemaEnvelope) { delete(s.Baseline.Sources, string(Interaction)) },
		"nil checkpoint":         func(s *SchemaEnvelope) { s.Baseline.Sources[string(Interaction)] = nil },
		"zero watermark":         func(s *SchemaEnvelope) { s.Baseline.Sources[string(Interaction)].Watermark = 0 },
		"negative watermark":     func(s *SchemaEnvelope) { s.Baseline.Sources[string(Interaction)].Watermark = -1 },
		"missing watermark keys": func(s *SchemaEnvelope) { s.Baseline.Sources[string(Interaction)].AtWatermark = nil },
		"invalid watermark key":  func(s *SchemaEnvelope) { s.Baseline.Sources[string(Interaction)].AtWatermark = []string{" "} },
		"duplicate watermark keys": func(s *SchemaEnvelope) {
			s.Baseline.Sources[string(Interaction)].AtWatermark = []string{"same", "same"}
		},
		"missing receipt": func(s *SchemaEnvelope) {
			d := s.Deliveries["ledger-0000"]
			d.Receipt = ""
			s.Deliveries["ledger-0000"] = d
		},
		"bad control":         func(s *SchemaEnvelope) { s.Control = &ControlState{Phase: ACTIVE} },
		"oversized pending":   func(s *SchemaEnvelope) { *s = retentionState(0, MaxLedger+1) },
		"oversized delivered": func(s *SchemaEnvelope) { *s = retentionState(MaxLedger+1, 0) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := retentionState(300, 1)
			change(&s)
			before := roundTrip(t, s)
			got, err := CompactDelivered(s, "opaque-route")
			if err == nil || !reflect.DeepEqual(got, before) || !reflect.DeepEqual(s, before) {
				t.Fatal("invalid input accepted or modified")
			}
		})
	}
	for _, route := range []string{"", " ", "other"} {
		if _, err := CompactDelivered(retentionState(1, 0), route); err == nil {
			t.Fatalf("route accepted: %q", route)
		}
	}
}

func TestCompactDeliveredSourceCheckpoints(t *testing.T) {
	for _, source := range []Source{System, DM} {
		s := retentionState(300, 0)
		s.Initialized[source] = true
		for k, d := range s.Deliveries {
			d.Source = source
			s.Deliveries[k] = d
		}
		if source == System {
			s.Baseline.Sources[string(System)] = &SourceBaseline{Watermark: 10, AtWatermark: []string{"system-watermark"}}
		} else {
			s.Baseline.DM = &DMBaseline{Channels: []DMChannelBaseline{{GuildID: "guild", ChannelID: "channel", LastSeq: "9007199254740994", LastTime: "10"}}}
		}
		if out, err := CompactDelivered(s, "opaque-route"); err != nil || len(out.Deliveries) != MaxDeliveredRetained {
			t.Fatalf("valid %s checkpoint: %v", source, err)
		}
		if source == DM {
			for _, bad := range []*DMBaseline{nil, {}, {Channels: []DMChannelBaseline{{GuildID: "g", ChannelID: "c", LastSeq: "bad", LastTime: "10"}}}} {
				s.Baseline.DM = bad
				if _, err := CompactDelivered(s, "opaque-route"); err == nil {
					t.Fatal("missing/invalid DM checkpoint accepted")
				}
			}
		}
	}
}

func TestCompactDeliveredBelowLimitAndPendingOnly(t *testing.T) {
	for _, counts := range [][2]int{{0, 0}, {0, MaxLedger}, {1, 0}, {MaxDeliveredRetained, 0}} {
		s := retentionState(counts[0], counts[1])
		if counts[0] == 0 {
			s.Initialized = map[Source]bool{}
			s.Baseline.Sources = map[string]*SourceBaseline{}
		}
		out, err := CompactDelivered(s, "opaque-route")
		if err != nil || !reflect.DeepEqual(out, s) {
			t.Fatalf("under-limit state changed: %v", err)
		}
	}
}
