package notices

import "sort"

// MaxDeliveredRetained bounds acknowledgement history independently of pending work.
const MaxDeliveredRetained = 256

// CompactDelivered returns a detached candidate retaining every pending record and
// the newest 256 delivered records, ordered by UpdatedAt then ledger key (larger
// keys win ties). It performs no I/O and does not change checkpoints or control.
//
// PRECONDITION: state must originate from this reconstructed Engine's atomic Poll
// snapshots, durably saved BEFORE sending, then acknowledged only after a real
// receipt. Poll commits source watermarks/AtWatermark and DM cursors together with
// pending records. Those checkpoints, not delivered history, suppress old source
// items, including items at the current watermark. Pending records are NEVER
// evicted; a send with a lost acknowledgement remains an at-least-once replay.
//
// The checks below reject missing checkpoints, not forged provenance. EventKey is
// opaque: this schema cannot prove a particular record's timestamp/DM cursor is
// covered. Do not use this function to compact arbitrary imported/legacy state or
// state whose checkpoint was independently reset. Such migration needs per-record
// source timestamp (or composite DM channel/sequence) metadata and coverage checks.
func CompactDelivered(s SchemaEnvelope, routeKey string) (SchemaEnvelope, error) {
	if !valid(routeKey) || s.validate() != nil || len(s.Deliveries) > MaxLedger {
		return s, ErrInvalid
	}
	keys := make([]string, 0, len(s.Deliveries))
	checked := make(map[Source]bool)
	for key, d := range s.Deliveries {
		if d.RouteKey != routeKey {
			return s, ErrInvalid
		}
		if d.State != DeliveryDelivered {
			continue
		}
		if !checked[d.Source] {
			if !retentionCheckpoint(s, d.Source) {
				return s, ErrInvalid
			}
			checked[d.Source] = true
		}
		keys = append(keys, key)
	}
	out, err := copyEnvelope(s)
	if err != nil {
		return s, err
	}
	// copyEnvelope's JSON copy canonicalizes empty omitempty slices to nil.
	// Restore their representation as well as their values: retention must not
	// rewrite baseline metadata, even when a caller uses explicit empty slices.
	if s.Baseline.LegacyIDs != nil && out.Baseline.LegacyIDs == nil {
		out.Baseline.LegacyIDs = []string{}
	}
	for source, original := range s.Baseline.Sources {
		if original == nil {
			continue
		}
		cloned := out.Baseline.Sources[source]
		if original.AtWatermark != nil && cloned.AtWatermark == nil {
			cloned.AtWatermark = []string{}
		}
		if original.OldIDs != nil && cloned.OldIDs == nil {
			cloned.OldIDs = []string{}
		}
		if original.RecentNotices != nil && cloned.RecentNotices == nil {
			cloned.RecentNotices = []RecentNotice{}
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := s.Deliveries[keys[i]], s.Deliveries[keys[j]]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		return keys[i] > keys[j]
	})
	for i := MaxDeliveredRetained; i < len(keys); i++ {
		delete(out.Deliveries, keys[i])
	}
	return out, nil
}

func retentionCheckpoint(s SchemaEnvelope, source Source) bool {
	if !s.Initialized[source] {
		return false
	}
	switch source {
	case Interaction, System:
		b := s.Baseline.Sources[string(source)]
		if b == nil || b.Watermark <= 0 || len(b.AtWatermark) == 0 || len(b.AtWatermark) > MaxFetchItems {
			return false
		}
		seen := make(map[string]bool, len(b.AtWatermark))
		for _, key := range b.AtWatermark {
			if !valid(key) || seen[key] {
				return false
			}
			seen[key] = true
		}
		return true
	case DM:
		if s.Baseline.DM == nil || len(s.Baseline.DM.Channels) == 0 {
			return false
		}
		entries := make([]DMSnapshotEntry, 0, len(s.Baseline.DM.Channels))
		for _, c := range s.Baseline.DM.Channels {
			if !valid(c.GuildID) || !valid(c.ChannelID) {
				return false
			}
			entries = append(entries, DMSnapshotEntry{Key: DMKey{c.GuildID, c.ChannelID}, Seq: c.LastSeq, Time: c.LastTime})
		}
		_, err := snapshotIndex(entries)
		return err == nil
	default:
		return false
	}
}
