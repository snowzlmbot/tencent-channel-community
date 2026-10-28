package notices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"time"
)

// InjectedFetcher is the only read capability; Engine performs no network or disk I/O.
type InjectedFetcher interface {
	ReadInteractions(context.Context, string) ([]map[string]any, error)
	ReadSystem(context.Context) ([]map[string]any, error)
	ReadDMSnapshot(context.Context) ([]DMSnapshotEntry, error)
	ReadDMDetails(context.Context, DMRange) ([]map[string]any, error)
}

const MaxLedger = 1000

type OwnedDelivery struct {
	Key, RouteKey  string
	Source         Source
	EventKey, Text string
}
type Engine struct {
	fetch    InjectedFetcher
	routeKey string
}

// NewEngine accepts exactly one caller-confirmed opaque route, never a session.
// Authorization remains the caller's responsibility (Manager); this is not a daemon.
func NewEngine(fetch InjectedFetcher, routeKey string) (*Engine, error) {
	if fetch == nil || !valid(routeKey) {
		return nil, ErrInvalid
	}
	return &Engine{fetch: fetch, routeKey: routeKey}, nil
}

func copyEnvelope(s SchemaEnvelope) (SchemaEnvelope, error) {
	var out SchemaEnvelope
	if s.validate() != nil || len(s.Deliveries) > MaxLedger {
		return out, ErrInvalid
	}
	b, err := json.Marshal(s)
	if err != nil {
		return out, ErrInvalid
	}
	if json.Unmarshal(b, &out) != nil {
		return out, ErrInvalid
	}
	return out, nil
}
func genericText(s Source) string {
	switch s {
	case Interaction:
		return "interaction更新"
	case System:
		return "system更新"
	case DM:
		return "私信更新"
	}
	return ""
}
func opaque(parts ...any) (string, error) {
	b, err := json.Marshal(parts)
	if err != nil {
		return "", ErrFetchPayload
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// CollectPending replays durable generic records, not any original message data.
// Mixed-route state is rejected, not silently reassigned to the current route.
func (e *Engine) CollectPending(s SchemaEnvelope) ([]OwnedDelivery, error) {
	if s.validate() != nil || len(s.Deliveries) > MaxLedger {
		return nil, ErrInvalid
	}
	out := make([]OwnedDelivery, 0)
	for k, d := range s.Deliveries {
		if d.RouteKey != e.routeKey {
			return nil, ErrInvalid
		}
		if d.State == DeliveryPending {
			out = append(out, OwnedDelivery{k, d.RouteKey, d.Source, d.EventKey, genericText(d.Source)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// AckDelivery accepts only an exact pending ledger key/route and an actual nonempty
// delivery receipt supplied by the caller. It never treats server reads as receipts.
func AckDelivery(s SchemaEnvelope, key, routeKey, receipt string) (SchemaEnvelope, error) {
	d, ok := s.Deliveries[key]
	if !ok || d.State != DeliveryPending || d.RouteKey != routeKey || !valid(receipt) {
		return s, ErrInvalid
	}
	out, err := copyEnvelope(s)
	if err != nil {
		return s, err
	}
	d.State = DeliveryDelivered
	d.Receipt = receipt
	d.UpdatedAt = time.Now().UTC()
	out.Deliveries[key] = d
	return out, nil
}
func (e *Engine) AckDelivery(s SchemaEnvelope, key, routeKey, receipt string) (SchemaEnvelope, error) {
	if routeKey != e.routeKey {
		return s, ErrInvalid
	}
	return AckDelivery(s, key, routeKey, receipt)
}

// positiveStamp rejects floats (even integral floats), fractions, signs, zero,
// overflow and lossy float conversions. JSON callers must decode with UseNumber.
func positiveStamp(v any) (int64, bool) {
	var text string
	switch n := v.(type) {
	case string:
		text = n
	case json.Number:
		text = n.String()
	default:
		if v == nil {
			return 0, false
		}
		r := reflect.ValueOf(v)
		switch r.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			text = strconv.FormatInt(r.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			text = strconv.FormatUint(r.Uint(), 10)
		default:
			return 0, false
		}
	}
	n, ok := decimal(text)
	if !ok || !n.IsInt64() || n.Sign() <= 0 {
		return 0, false
	}
	return n.Int64(), true
}
func itemStamp(s Source, item map[string]any) (int64, error) {
	candidates := []any{}
	if s == Interaction {
		if feed, ok := item["psvFeed"].(map[string]any); ok {
			candidates = append(candidates, feed["createTime"], feed["create_time"])
		}
		candidates = append(candidates, item["createTime"], item["create_time"])
	} else {
		candidates = append(candidates, item["createTs"], item["create_time"])
	}
	for _, v := range candidates {
		if n, ok := positiveStamp(v); ok {
			return n, nil
		}
	}
	return 0, ErrFetchPayload
}
func (e *Engine) queue(s *SchemaEnvelope, source Source, event string) error {
	key, _ := opaque(e.routeKey, source, event)
	if _, ok := s.Deliveries[key]; ok {
		return nil
	}
	// No silent eviction: a full durable ledger stops advancement until an explicit
	// archival/migration policy is provided. Both pending and delivered are bounded.
	if len(s.Deliveries) >= MaxLedger {
		return ErrFetchPayload
	}
	s.Deliveries[key] = DeliveryRecord{RouteKey: e.routeKey, Source: source, EventKey: event, State: DeliveryPending, UpdatedAt: time.Now().UTC()}
	return nil
}
func (e *Engine) pollSource(s *SchemaEnvelope, source Source, items []map[string]any) error {
	if len(items) > MaxFetchItems {
		return ErrFetchPayload
	}
	type entry struct {
		stamp int64
		key   string
	}
	events := make([]entry, 0, len(items))
	for _, item := range items {
		if item == nil {
			return ErrFetchPayload
		}
		stamp, err := itemStamp(source, item)
		if err != nil {
			return err
		}
		key, err := opaque(source, stamp, item)
		if err != nil {
			return err
		}
		events = append(events, entry{stamp, key})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].stamp != events[j].stamp {
			return events[i].stamp < events[j].stamp
		}
		return events[i].key < events[j].key
	})
	b := s.Baseline.Sources[string(source)]
	if b == nil {
		b = &SourceBaseline{}
		s.Baseline.Sources[string(source)] = b
	}
	ids := make(map[string]bool)
	for _, id := range b.AtWatermark {
		ids[id] = true
	}
	for _, ev := range events {
		if ev.stamp < b.Watermark || ev.stamp == b.Watermark && ids[ev.key] {
			continue
		}
		if s.Initialized[source] {
			if err := e.queue(s, source, ev.key); err != nil {
				return err
			}
		}
		if ev.stamp > b.Watermark {
			b.Watermark = ev.stamp
			ids = make(map[string]bool)
		}
		ids[ev.key] = true
	}
	b.AtWatermark = nil
	for id := range ids {
		b.AtWatermark = append(b.AtWatermark, id)
	}
	sort.Strings(b.AtWatermark)
	if len(b.AtWatermark) > MaxFetchItems {
		return ErrFetchPayload
	}
	s.Initialized[source] = true
	return nil
}

func (e *Engine) pollDM(ctx context.Context, s *SchemaEnvelope, current []DMSnapshotEntry) error {
	previous := []DMSnapshotEntry{}
	if s.Baseline.DM != nil {
		for _, b := range s.Baseline.DM.Channels {
			previous = append(previous, DMSnapshotEntry{DMKey{b.GuildID, b.ChannelID}, b.LastSeq, b.LastTime})
		}
	}
	ranges, err := PlanDM(current, previous, s.Initialized[DM])
	if err != nil {
		return err
	}
	succeeded := make(map[DMKey]bool)
	total := 0
	for _, interval := range ranges {
		items, err := e.fetch.ReadDMDetails(ctx, interval)
		if err != nil {
			return err
		}
		total += len(items)
		if total > MaxFetchItems {
			return ErrFetchPayload
		}
		begin, _ := decimal(interval.BeginSeq)
		end, _ := decimal(interval.EndSeq)
		for _, item := range items {
			seq, err := requiredDecimal(item, "msgSeq")
			if err != nil {
				return err
			}
			n, _ := decimal(seq)
			_, ok := positiveStamp(item["msgTime"])
			if !ok || n.Sign() <= 0 || n.Cmp(begin) < 0 || n.Cmp(end) > 0 {
				return ErrFetchPayload
			}
			// Explicit empty text is skipped; absent text/sender is sufficient for a
			// generic notice, unlike recovered native cards. Never persist either field.
			if text, ok := item["text"].(string); ok && text == "" {
				continue
			}
			sender, _ := item["fromTinyId"].(string)
			if sender == "" {
				sender, _ = item["from_tiny_id"].(string)
			}
			if valid(s.Baseline.MyTinyID) && valid(sender) && sender == s.Baseline.MyTinyID {
				continue
			}
			key, err := opaque(DM, interval.Key.GuildID, interval.Key.ChannelID, n.String())
			if err != nil {
				return err
			}
			if err = e.queue(s, DM, key); err != nil {
				return err
			}
		}
		succeeded[interval.Key] = true
	}
	committed, err := CommitDMSuccess(current, previous, succeeded)
	if err != nil {
		return err
	}
	s.Baseline.DM = &DMBaseline{Channels: make([]DMChannelBaseline, 0, len(committed))}
	for _, entry := range committed {
		s.Baseline.DM.Channels = append(s.Baseline.DM.Channels, DMChannelBaseline{entry.Key.GuildID, entry.Key.ChannelID, entry.Seq, entry.Time})
	}
	s.Initialized[DM] = true
	return nil
}

// Poll returns an atomic candidate snapshot. Caller MUST durably Save(newstate)
// before emitting pending, then Save(AckDelivery(...)) after a real receipt.
// Crash replay is at-least-once: a send followed by a lost ack may be repeated.
// Any read/parse/capacity failure returns the unchanged original and no deliveries.
// Cold sources (including empty sources) initialize independently and silently.
// Native-parity divergence: generic text, hashed map identities instead of fully
// recovered cleaning/cards; native 50/5 read bursts can miss overflow between
// polls. This bounded core is not a production-complete native replacement.
func (e *Engine) Poll(ctx context.Context, s SchemaEnvelope) (SchemaEnvelope, []OwnedDelivery, error) {
	pending, err := e.CollectPending(s)
	if err != nil {
		return s, nil, err
	}
	if len(pending) > 0 {
		return s, pending, nil
	}
	out, err := copyEnvelope(s)
	if err != nil {
		return s, nil, err
	}
	interactions, err := e.fetch.ReadInteractions(ctx, "")
	if err != nil {
		return s, nil, err
	}
	system, err := e.fetch.ReadSystem(ctx)
	if err != nil {
		return s, nil, err
	}
	dm, err := e.fetch.ReadDMSnapshot(ctx)
	if err != nil {
		return s, nil, err
	}
	if err = e.pollSource(&out, Interaction, interactions); err != nil {
		return s, nil, err
	}
	if err = e.pollSource(&out, System, system); err != nil {
		return s, nil, err
	}
	if err = e.pollDM(ctx, &out, dm); err != nil {
		return s, nil, err
	}
	pending, err = e.CollectPending(out)
	if err != nil {
		return s, nil, err
	}
	return out, pending, nil
}
