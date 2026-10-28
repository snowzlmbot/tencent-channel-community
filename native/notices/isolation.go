package notices

import "context"

// PollHealth exposes only closed labels, never upstream error strings. Sources
// always contains all three sources. Replay means no sources were fetched.
type PollHealth struct {
	Status  string            `json:"status"`
	Sources map[Source]string `json:"sources"`
}

// isolatedCopy retains even explicit empty checkpoint slices while detaching
// every mutable field. A failed source must not acquire JSON normalization edits.
func isolatedCopy(s SchemaEnvelope) (SchemaEnvelope, error) {
	out, err := copyEnvelope(s)
	if err != nil {
		return s, ErrInvalid
	}
	cloneStrings := func(v []string) []string {
		if v == nil {
			return nil
		}
		return append(make([]string, 0, len(v)), v...)
	}
	out.Baseline.LegacyIDs = cloneStrings(s.Baseline.LegacyIDs)
	for key, original := range s.Baseline.Sources {
		if original == nil {
			continue
		}
		b := out.Baseline.Sources[key]
		b.AtWatermark = cloneStrings(original.AtWatermark)
		b.OldIDs = cloneStrings(original.OldIDs)
		if original.RecentNotices != nil {
			b.RecentNotices = append(make([]RecentNotice, 0, len(original.RecentNotices)), original.RecentNotices...)
		}
	}
	for key, record := range s.Deliveries {
		out.Deliveries[key] = record
	}
	if s.Control != nil {
		control := *s.Control
		out.Control = &control
	}
	return out, nil
}

// isolatedDMFetcher prevents another detail read after cancellation, even when
// an injected fetcher ignores its context. The whole DM candidate then rolls back.
type isolatedDMFetcher struct{ InjectedFetcher }

func (f isolatedDMFetcher) ReadDMDetails(ctx context.Context, interval DMRange) ([]map[string]any, error) {
	if ctx.Err() != nil {
		return nil, ErrInvalid
	}
	return f.InjectedFetcher.ReadDMDetails(ctx, interval)
}

// PollIsolated reconstructs a detached candidate, committing each successful
// source independently. A malformed/read/capacity failure discards that source's
// entire tentative update (including any earlier DM details), not other sources.
// Existing Poll deliberately retains its all-or-nothing compatibility behavior.
//
// The caller must atomically save the returned envelope ONCE before emitting any
// returned deliveries, then persist acknowledgements after actual receipts.
// Health is transient metadata and does not authorize sending or change control.
func (e *Engine) PollIsolated(ctx context.Context, s SchemaEnvelope) (SchemaEnvelope, []OwnedDelivery, PollHealth, error) {
	health := PollHealth{Status: "degraded", Sources: map[Source]string{
		Interaction: "deferred", System: "deferred", DM: "deferred",
	}}
	pending, err := e.CollectPending(s)
	if err != nil {
		return s, nil, health, ErrInvalid
	}
	if len(pending) != 0 {
		health.Status = "replay"
		return s, pending, health, nil
	}
	out, err := isolatedCopy(s)
	if err != nil {
		return s, nil, health, ErrInvalid
	}
	if len(out.Deliveries) > MaxDeliveredRetained {
		out, err = CompactDelivered(out, e.routeKey)
		if err != nil {
			// Missing retention provenance is a global safety failure, never
			// a source-local degradation that could permit checkpoint changes.
			return s, nil, health, ErrInvalid
		}
		// Compaction only removes records; preserve exact metadata of those
		// retained, including time representations and caller control.
		for key := range out.Deliveries {
			out.Deliveries[key] = s.Deliveries[key]
		}
		if s.Control != nil {
			control := *s.Control
			out.Control = &control
		}
	}
	successes := 0
	for _, source := range []Source{Interaction, System, DM} {
		if ctx.Err() != nil {
			break
		}
		candidate, copyErr := isolatedCopy(out)
		if copyErr != nil {
			return s, nil, health, ErrInvalid
		}
		switch source {
		case Interaction:
			var items []map[string]any
			items, err = e.fetch.ReadInteractions(ctx, "")
			if err == nil {
				err = e.pollSource(&candidate, source, items)
			}
		case System:
			var items []map[string]any
			items, err = e.fetch.ReadSystem(ctx)
			if err == nil {
				err = e.pollSource(&candidate, source, items)
			}
		case DM:
			var snapshot []DMSnapshotEntry
			snapshot, err = e.fetch.ReadDMSnapshot(ctx)
			if err == nil {
				if ctx.Err() != nil {
					err = ErrInvalid
				} else {
					dmEngine := *e
					dmEngine.fetch = isolatedDMFetcher{e.fetch}
					err = dmEngine.pollDM(ctx, &candidate, snapshot)
				}
			}
		}
		if err != nil {
			health.Sources[source] = "failed"
			continue
		}
		out = candidate
		health.Sources[source] = "ok"
		successes++
	}
	if successes == 0 {
		return s, nil, health, ErrInvalid
	}
	pending, err = e.CollectPending(out)
	if err != nil {
		return s, nil, health, ErrInvalid
	}
	if successes == 3 {
		health.Status = "healthy"
	}
	return out, pending, health, nil
}
