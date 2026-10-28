package notices

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"strconv"
	"time"
)

// NativeCaller is a capability boundary, not an HTTP/auth implementation. Args
// are final wire keys; an adapter must preserve the native error/policy checks.
type NativeCaller interface {
	Call(context.Context, string, map[string]any) (map[string]any, error)
}

const MaxFetchItems = 1000

var ErrFetchPayload = errors.New("invalid or oversized native fetch payload")
var ErrNativeCallerUnavailable = errors.New("native caller unavailable")

// Fetcher implements only bounded protocol reads, not a poller, persistence,
// notification cleaning, timestamp deduplication, or delivery acknowledgements.
type Fetcher struct{ Caller NativeCaller }

func (f Fetcher) call(ctx context.Context, tool string, args map[string]any) (map[string]any, error) {
	if f.Caller == nil {
		return nil, ErrNativeCallerUnavailable
	}
	root, err := f.Caller.Call(ctx, tool, args)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, ErrFetchPayload
	}
	if payload, ok := root["structuredContent"].(map[string]any); ok {
		if payload == nil {
			return nil, ErrFetchPayload
		}
		return payload, nil
	}
	return root, nil
}

// Present lists must be []any; observed protobuf-style empty omissions are accepted.
// Missing/null lists and malformed elements fail atomically (never partial).
func fetchMaps(payload map[string]any, key string) ([]map[string]any, error) {
	// Real authenticated responses use protobuf-style omission for empty lists.
	// A fully empty, already validated structuredContent map is a legitimate
	// empty system/DM/interaction result. Interaction pagination is explicit.
	if _, exists := payload[key]; !exists {
		if len(payload) == 0 {
			return []map[string]any{}, nil
		}
		if key == "notices" && len(payload) == 2 {
			cursor, cursorOK := payload["attachInfo"].(string)
			finished, finishedOK := payload["isFinish"].(bool)
			if cursorOK && cursor == "" && finishedOK && finished {
				return []map[string]any{}, nil
			}
		}
		return nil, ErrFetchPayload
	}
	list, ok := payload[key].([]any)
	if !ok || list == nil || len(list) > MaxFetchItems {
		return nil, ErrFetchPayload
	}
	out := make([]map[string]any, 0, len(list))
	for _, value := range list {
		item, ok := value.(map[string]any)
		if !ok || item == nil {
			return nil, ErrFetchPayload
		}
		out = append(out, item)
	}
	return out, nil
}

// ReadInteractions returns raw maps; identities and complete cleaning have not
// been recovered. No event body is logged or included in parser errors.
func (f Fetcher) ReadInteractions(ctx context.Context, guild string) ([]map[string]any, error) {
	if guild == "" {
		guild = "0"
	}
	payload, err := f.call(ctx, "get_interact_notice", map[string]any{"pageNum": 50, "guildId": guild})
	if err != nil {
		return nil, err
	}
	return fetchMaps(payload, "notices")
}
func (f Fetcher) ReadSystem(ctx context.Context) ([]map[string]any, error) {
	payload, err := f.call(ctx, "get_notice_list", map[string]any{"limit": 5})
	if err != nil {
		return nil, err
	}
	return fetchMaps(payload, "noticeList")
}

// The following DM/digest models are owned reconstructions, not vendor DTOs.
type DMKey struct{ GuildID, ChannelID string }
type DMSnapshotEntry struct {
	Key       DMKey
	Seq, Time string
}
type DMRange struct {
	Key              DMKey
	BeginSeq, EndSeq string
}

func decimal(s string) (*big.Int, bool) {
	if s == "" {
		return nil, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	return n, ok
}
func requiredDecimal(item map[string]any, key string) (string, error) {
	s, ok := item[key].(string)
	if !ok {
		return "", ErrFetchPayload
	}
	if _, ok := decimal(s); !ok {
		return "", ErrFetchPayload
	}
	return s, nil
}
func identity(item map[string]any, primary, fallback string) (string, error) {
	if value, exists := item[primary]; exists {
		s, ok := value.(string)
		if !ok {
			return "", ErrFetchPayload
		}
		if s != "" {
			return s, nil
		}
	}
	if value, exists := item[fallback]; exists {
		s, ok := value.(string)
		if !ok {
			return "", ErrFetchPayload
		}
		if s != "" {
			return s, nil
		}
	}
	return "", ErrFetchPayload
}
func (f Fetcher) ReadDMSnapshot(ctx context.Context) ([]DMSnapshotEntry, error) {
	payload, err := f.call(ctx, "query_normal_dm_list", map[string]any{})
	if err != nil {
		return nil, err
	}
	list, err := fetchMaps(payload, "rptMyDirectMessageInfo")
	if err != nil {
		return nil, err
	}
	out := make([]DMSnapshotEntry, 0, len(list))
	seen := make(map[DMKey]bool)
	for _, item := range list {
		guild, err := identity(item, "guildId", "guild_id")
		if err != nil {
			return nil, err
		}
		channel, err := identity(item, "channelId", "channel_id")
		if err != nil {
			return nil, err
		}
		latest, ok := item["lastestMsgSeq"].(map[string]any)
		if !ok {
			return nil, ErrFetchPayload
		}
		seq, err := requiredDecimal(latest, "uint64Seq")
		if err != nil {
			return nil, err
		}
		stamp, err := requiredDecimal(latest, "uint64Time")
		if err != nil {
			return nil, err
		}
		key := DMKey{guild, channel}
		if seen[key] {
			return nil, ErrFetchPayload
		}
		seen[key] = true
		out = append(out, DMSnapshotEntry{key, seq, stamp})
	}
	sortSnapshot(out)
	return out, nil
}
func keyLess(a, b DMKey) bool {
	if a.GuildID != b.GuildID {
		return a.GuildID < b.GuildID
	}
	return a.ChannelID < b.ChannelID
}
func sortSnapshot(entries []DMSnapshotEntry) {
	sort.Slice(entries, func(i, j int) bool { return keyLess(entries[i].Key, entries[j].Key) })
}
func snapshotIndex(entries []DMSnapshotEntry) (map[DMKey]DMSnapshotEntry, error) {
	if len(entries) > MaxFetchItems {
		return nil, ErrFetchPayload
	}
	out := make(map[DMKey]DMSnapshotEntry, len(entries))
	for _, e := range entries {
		if e.Key.GuildID == "" || e.Key.ChannelID == "" {
			return nil, ErrFetchPayload
		}
		if _, ok := decimal(e.Seq); !ok {
			return nil, ErrFetchPayload
		}
		if _, ok := decimal(e.Time); !ok {
			return nil, ErrFetchPayload
		}
		if _, ok := out[e.Key]; ok {
			return nil, ErrFetchPayload
		}
		out[e.Key] = e
	}
	return out, nil
}

// PlanDM deliberately differs from the recovered implementation: composite
// identity, numeric nonnegative arbitrary-precision sequence comparison, no
// signed-64 overflow, and no historical fetch for cold/new channels. New
// channels establish a baseline on commit; regressions retain the old cursor.
func PlanDM(current, previous []DMSnapshotEntry, initialized bool) ([]DMRange, error) {
	now, err := snapshotIndex(current)
	if err != nil {
		return nil, err
	}
	old, err := snapshotIndex(previous)
	if err != nil {
		return nil, err
	}
	out := make([]DMRange, 0)
	if !initialized {
		return out, nil
	}
	for key, entry := range now {
		before, found := old[key]
		if !found {
			continue
		}
		n, _ := decimal(entry.Seq)
		p, _ := decimal(before.Seq)
		if n.Cmp(p) <= 0 {
			continue
		}
		begin := new(big.Int).Add(p, big.NewInt(1)).String()
		out = append(out, DMRange{key, begin, entry.Seq})
	}
	sort.Slice(out, func(i, j int) bool { return keyLess(out[i].Key, out[j].Key) })
	return out, nil
}

// CommitDMSuccess preserves old entries on failed/unattempted detail reads,
// regressions, and temporary channel absence. New channels baseline without
// history by design, irrespective of succeeded. Caller persists initialization
// separately, and marks success only after the complete detail read is handled.
func CommitDMSuccess(current, previous []DMSnapshotEntry, succeeded map[DMKey]bool) ([]DMSnapshotEntry, error) {
	now, err := snapshotIndex(current)
	if err != nil {
		return nil, err
	}
	old, err := snapshotIndex(previous)
	if err != nil {
		return nil, err
	}
	for key, entry := range now {
		before, found := old[key]
		if !found {
			old[key] = entry
			continue
		}
		n, _ := decimal(entry.Seq)
		p, _ := decimal(before.Seq)
		if n.Cmp(p) == 0 || n.Cmp(p) > 0 && succeeded[key] {
			old[key] = entry
		}
	}
	if len(old) > MaxFetchItems {
		return nil, ErrFetchPayload
	}
	out := make([]DMSnapshotEntry, 0, len(old))
	for _, e := range old {
		out = append(out, e)
	}
	sortSnapshot(out)
	return out, nil
}

// ReadDMDetails returns all raw detail maps sorted by numeric msgSeq. It does
// not silently discard empty text/self messages, guess identities, or clean nicknames.
func (f Fetcher) ReadDMDetails(ctx context.Context, interval DMRange) ([]map[string]any, error) {
	begin, ok := decimal(interval.BeginSeq)
	if !ok {
		return nil, ErrFetchPayload
	}
	end, ok := decimal(interval.EndSeq)
	if !ok || begin.Cmp(end) > 0 || interval.Key.GuildID == "" || interval.Key.ChannelID == "" {
		return nil, ErrFetchPayload
	}
	payload, err := f.call(ctx, "query_guild_channel_msg", map[string]any{
		"guildChannelParam": map[string]any{"guildId": interval.Key.GuildID, "channelId": interval.Key.ChannelID, "beginSeq": interval.BeginSeq, "endSeq": interval.EndSeq},
		"directMessageFlag": 1,
	})
	if err != nil {
		return nil, err
	}
	messages, ok := payload["guildChannelMsg"].(map[string]any)
	if !ok {
		return nil, ErrFetchPayload
	}
	list, err := fetchMaps(messages, "msgs")
	if err != nil {
		return nil, err
	}
	type sequenced struct {
		item map[string]any
		seq  *big.Int
	}
	sorted := make([]sequenced, 0, len(list))
	for _, item := range list {
		seq, err := requiredDecimal(item, "msgSeq")
		if err != nil {
			return nil, err
		}
		n, _ := decimal(seq)
		if n.Cmp(begin) < 0 || n.Cmp(end) > 0 {
			return nil, ErrFetchPayload
		}
		sorted = append(sorted, sequenced{item, n})
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].seq.Cmp(sorted[j].seq) < 0 })
	out := make([]map[string]any, 0, len(sorted))
	for _, entry := range sorted {
		out = append(out, entry.item)
	}
	return out, nil
}

type DigestCategory int

const (
	DigestInteraction DigestCategory = iota
	DigestSystem
)

type DigestEntry struct {
	BusinessType string
	Category     DigestCategory
	TimestampMS  string
}
type DigestSnapshot struct {
	Entries           []DigestEntry
	NextCallTimestamp string
	Delay             time.Duration
}

// Category semantic labels are inferred; business names/category numbers are
// recovered. Digest timestamps are not notification deduplication watermarks.
func (f Fetcher) ReadDigest(ctx context.Context, now time.Time) (DigestSnapshot, error) {
	payload, err := f.call(ctx, "query_user_guild_digest", map[string]any{})
	if err != nil {
		return DigestSnapshot{}, err
	}
	list, err := fetchMaps(payload, "datas")
	if err != nil {
		return DigestSnapshot{}, err
	}
	next, err := requiredDecimal(payload, "nextCallTimeStamp")
	if err != nil {
		return DigestSnapshot{}, err
	}
	seconds, err := strconv.ParseInt(next, 10, 64)
	if err != nil {
		return DigestSnapshot{}, ErrFetchPayload
	}
	result := DigestSnapshot{Entries: make([]DigestEntry, 0), NextCallTimestamp: next}
	target := time.Unix(seconds, 0)
	if target.After(now) {
		result.Delay = target.Sub(now)
	}
	seen := make(map[string]bool)
	for _, item := range list {
		business, ok := item["businessType"].(string)
		if !ok || business == "" {
			return DigestSnapshot{}, ErrFetchPayload
		}
		var category DigestCategory
		switch business {
		case "BUSINESS_TYPE_MEMBER_INTERACTION_NOTIFY", "BUSINESS_TYPE_VISTOR_INTERACTION_NOTIFY":
			category = DigestInteraction
		case "BUSINESS_TYPE_MANAGE", "BUSINESS_TYPE_GUILD_ASSISTANT":
			category = DigestSystem
		default:
			continue
		}
		stamp, err := requiredDecimal(item, "latestTimeStampMs")
		if err != nil {
			return DigestSnapshot{}, err
		}
		if seen[business] {
			return DigestSnapshot{}, ErrFetchPayload
		}
		seen[business] = true
		result.Entries = append(result.Entries, DigestEntry{business, category, stamp})
	}
	return result, nil
}
