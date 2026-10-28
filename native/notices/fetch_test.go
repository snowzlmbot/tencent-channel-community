package notices

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fixtureCaller struct {
	tool     string
	args     map[string]any
	response map[string]any
	err      error
	calls    int
}

func (c *fixtureCaller) Call(_ context.Context, tool string, args map[string]any) (map[string]any, error) {
	c.tool, c.args = tool, args
	c.calls++
	return c.response, c.err
}
func snapshotFixture(guild, channel, seq string) map[string]any {
	return map[string]any{"guildId": guild, "channelId": channel, "lastestMsgSeq": map[string]any{"uint64Seq": seq, "uint64Time": "1700000000"}}
}
func ownedEntry(guild, channel, seq string) DMSnapshotEntry {
	return DMSnapshotEntry{DMKey{guild, channel}, seq, "1700000000"}
}
func TestFetchExactCalls(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, tool    string
		args, payload map[string]any
		read          func(Fetcher) error
	}{
		{"interaction default", "get_interact_notice", map[string]any{"pageNum": 50, "guildId": "0"}, map[string]any{"notices": []any{}}, func(f Fetcher) error { _, e := f.ReadInteractions(ctx, ""); return e }},
		{"interaction guild", "get_interact_notice", map[string]any{"pageNum": 50, "guildId": "42"}, map[string]any{"notices": []any{}}, func(f Fetcher) error { _, e := f.ReadInteractions(ctx, "42"); return e }},
		{"system", "get_notice_list", map[string]any{"limit": 5}, map[string]any{"noticeList": []any{}}, func(f Fetcher) error { _, e := f.ReadSystem(ctx); return e }},
		{"snapshot", "query_normal_dm_list", map[string]any{}, map[string]any{"rptMyDirectMessageInfo": []any{}}, func(f Fetcher) error { _, e := f.ReadDMSnapshot(ctx); return e }},
		{"details", "query_guild_channel_msg", map[string]any{"guildChannelParam": map[string]any{"guildId": "g", "channelId": "c", "beginSeq": "18446744073709551616", "endSeq": "18446744073709551617"}, "directMessageFlag": 1}, map[string]any{"guildChannelMsg": map[string]any{"msgs": []any{}}}, func(f Fetcher) error {
			_, e := f.ReadDMDetails(ctx, DMRange{DMKey{"g", "c"}, "18446744073709551616", "18446744073709551617"})
			return e
		}},
		{"digest", "query_user_guild_digest", map[string]any{}, map[string]any{"datas": []any{}, "nextCallTimeStamp": "0"}, func(f Fetcher) error { _, e := f.ReadDigest(ctx, time.Unix(100, 0)); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &fixtureCaller{response: map[string]any{"structuredContent": tc.payload}}
			if err := tc.read(Fetcher{c}); err != nil {
				t.Fatal(err)
			}
			if c.tool != tc.tool || !reflect.DeepEqual(c.args, tc.args) || c.calls != 1 {
				t.Fatalf("unexpected protocol call: %s %#v", c.tool, c.args)
			}
		})
	}
}
func TestFetchEnvelopeAndErrors(t *testing.T) {
	ctx := context.Background()
	for _, wrapper := range []any{nil, "not a map", false} {
		c := &fixtureCaller{response: map[string]any{"structuredContent": wrapper, "notices": []any{map[string]any{"opaque": true}}}}
		list, err := (Fetcher{c}).ReadInteractions(ctx, "")
		if err != nil || len(list) != 1 {
			t.Fatal("root fallback failed")
		}
	}
	c := &fixtureCaller{response: map[string]any{"structuredContent": map[string]any{"unexpected": true}, "notices": []any{}}}
	if _, err := (Fetcher{c}).ReadInteractions(ctx, ""); !errors.Is(err, ErrFetchPayload) {
		t.Fatal("must not fallback from malformed structured payload")
	}
	if _, err := (Fetcher{}).ReadSystem(ctx); !errors.Is(err, ErrNativeCallerUnavailable) {
		t.Fatal("missing caller")
	}
	sentinel := errors.New("native failure")
	c = &fixtureCaller{err: sentinel}
	if _, err := (Fetcher{c}).ReadSystem(ctx); !errors.Is(err, sentinel) {
		t.Fatal("caller error lost")
	}
	c = &fixtureCaller{}
	if _, err := (Fetcher{c}).ReadSystem(ctx); !errors.Is(err, ErrFetchPayload) {
		t.Fatal("nil response")
	}
}
func TestRequiredListsAtomicAndBounded(t *testing.T) {
	oversized := make([]any, MaxFetchItems+1)
	for i := range oversized {
		oversized[i] = map[string]any{}
	}
	for _, value := range []any{nil, "body must not leak", map[string]any{}, []map[string]any{}, []any{nil}, []any{map[string]any{}, "private body"}, oversized} {
		c := &fixtureCaller{response: map[string]any{"noticeList": value}}
		list, err := (Fetcher{c}).ReadSystem(context.Background())
		if !errors.Is(err, ErrFetchPayload) || list != nil {
			t.Fatal("invalid list accepted or partial returned")
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatal("payload in error")
		}
	}
	if got, err := fetchMaps(map[string]any{}, "noticeList"); err != nil || len(got) != 0 {
		t.Fatal("observed empty protobuf map rejected")
	}
	max := make([]any, MaxFetchItems)
	for i := range max {
		max[i] = map[string]any{}
	}
	if list, err := fetchMaps(map[string]any{"notices": max}, "notices"); err != nil || len(list) != MaxFetchItems {
		t.Fatal("valid bound rejected")
	}
}
func TestSnapshotStrictStringsAndAliases(t *testing.T) {
	ctx := context.Background()
	item := snapshotFixture("g", "c", "18446744073709551615")
	item["guild_id"], item["channel_id"] = item["guildId"], item["channelId"]
	delete(item, "guildId")
	delete(item, "channelId")
	c := &fixtureCaller{response: map[string]any{"rptMyDirectMessageInfo": []any{item}}}
	got, err := (Fetcher{c}).ReadDMSnapshot(ctx)
	if err != nil || len(got) != 1 || got[0].Seq != "18446744073709551615" || got[0].Key != (DMKey{"g", "c"}) {
		t.Fatal("snapshot precision/identity lost")
	}
	invalid := []map[string]any{}
	for _, bad := range []any{float64(12), 12, "-1", "+1", "1.0", " 1", ""} {
		v := snapshotFixture("g", "c", "1")
		v["lastestMsgSeq"].(map[string]any)["uint64Seq"] = bad
		invalid = append(invalid, v)
	}
	for _, field := range []string{"guildId", "channelId", "lastestMsgSeq"} {
		v := snapshotFixture("g", "c", "1")
		delete(v, field)
		invalid = append(invalid, v)
	}
	v := snapshotFixture("g", "c", "1")
	v["guildId"] = float64(1)
	invalid = append(invalid, v)
	v = snapshotFixture("g", "c", "1")
	v["lastestMsgSeq"].(map[string]any)["uint64Time"] = float64(1)
	invalid = append(invalid, v)
	for _, v := range invalid {
		c.response = map[string]any{"rptMyDirectMessageInfo": []any{snapshotFixture("valid", "valid", "1"), v}}
		if got, err := (Fetcher{c}).ReadDMSnapshot(ctx); err == nil || got != nil {
			t.Fatal("malformed snapshot not atomic")
		}
	}
	c.response = map[string]any{"rptMyDirectMessageInfo": []any{item, item}}
	if _, err := (Fetcher{c}).ReadDMSnapshot(ctx); err == nil {
		t.Fatal("duplicate channel accepted")
	}
}
func TestDMPlanAndCommit(t *testing.T) {
	previous := []DMSnapshotEntry{ownedEntry("g", "a", "18446744073709551615"), ownedEntry("g", "b", "9"), ownedEntry("missing", "c", "4")}
	current := []DMSnapshotEntry{ownedEntry("g", "a", "18446744073709551617"), ownedEntry("g", "b", "10"), ownedEntry("new", "c", "99")}
	cold, err := PlanDM(current, nil, false)
	if err != nil || len(cold) != 0 {
		t.Fatal("cold start fetched history")
	}
	plan, err := PlanDM(current, previous, true)
	want := []DMRange{{DMKey{"g", "a"}, "18446744073709551616", "18446744073709551617"}, {DMKey{"g", "b"}, "10", "10"}}
	if err != nil || !reflect.DeepEqual(plan, want) {
		t.Fatal("composite key/overflow/sorting/new-channel policy failed")
	}
	committed, err := CommitDMSuccess(current, previous, map[DMKey]bool{{"g", "b"}: true})
	if err != nil {
		t.Fatal(err)
	}
	index, err := snapshotIndex(committed)
	if err != nil {
		t.Fatal(err)
	}
	if index[DMKey{"g", "a"}].Seq != "18446744073709551615" || index[DMKey{"g", "b"}].Seq != "10" || index[DMKey{"new", "c"}].Seq != "99" || index[DMKey{"missing", "c"}].Seq != "4" {
		t.Fatal("failed cursor advanced or absent/new baseline lost")
	}
	retry, err := PlanDM(current, committed, true)
	if err != nil || len(retry) != 1 || retry[0].Key != (DMKey{"g", "a"}) {
		t.Fatal("failed detail not retryable")
	}
	regress := []DMSnapshotEntry{ownedEntry("g", "a", "1")}
	committed, err = CommitDMSuccess(regress, previous, map[DMKey]bool{{"g", "a"}: true})
	if err != nil {
		t.Fatal(err)
	}
	index, _ = snapshotIndex(committed)
	if index[DMKey{"g", "a"}].Seq != "18446744073709551615" {
		t.Fatal("regression advanced")
	}
	equal := []DMSnapshotEntry{ownedEntry("g", "b", "009")}
	if p, e := PlanDM(equal, previous, true); e != nil || len(p) != 0 {
		t.Fatal("decimal comparison was lexical")
	}
	if _, err := PlanDM([]DMSnapshotEntry{ownedEntry("g", "c", "-1")}, nil, false); err == nil {
		t.Fatal("cold malformed cursor accepted")
	}
	if _, err := CommitDMSuccess(current, append(previous, previous[0]), nil); err == nil {
		t.Fatal("duplicate old cursor accepted")
	}
	// Composite structs cannot collide through separators in string IDs.
	p, e := PlanDM([]DMSnapshotEntry{ownedEntry("a:b", "c", "2"), ownedEntry("a", "b:c", "3")}, []DMSnapshotEntry{ownedEntry("a:b", "c", "1"), ownedEntry("a", "b:c", "1")}, true)
	if e != nil || len(p) != 2 {
		t.Fatal("composite key collision")
	}
}
func TestDMDetailsSortAndRejectWithoutPartial(t *testing.T) {
	c := &fixtureCaller{}
	interval := DMRange{DMKey{"g", "c"}, "1", "18446744073709551617"}
	c.response = map[string]any{"guildChannelMsg": map[string]any{"msgs": []any{map[string]any{"msgSeq": "18446744073709551617", "text": ""}, map[string]any{"msgSeq": "10"}, map[string]any{"msgSeq": "2"}}}}
	got, err := (Fetcher{c}).ReadDMDetails(context.Background(), interval)
	if err != nil || len(got) != 3 || got[0]["msgSeq"] != "2" || got[1]["msgSeq"] != "10" {
		t.Fatal("details not numeric sorted or silently discarded")
	}
	for _, bad := range []any{float64(2), "0", "18446744073709551618", "-2", nil} {
		c.response = map[string]any{"guildChannelMsg": map[string]any{"msgs": []any{map[string]any{"msgSeq": "2"}, map[string]any{"msgSeq": bad}}}}
		if got, err := (Fetcher{c}).ReadDMDetails(context.Background(), interval); err == nil || got != nil {
			t.Fatal("invalid details returned partial")
		}
	}
	calls := c.calls
	if _, err := (Fetcher{c}).ReadDMDetails(context.Background(), DMRange{DMKey{"g", "c"}, "2", "1"}); err == nil || calls != c.calls {
		t.Fatal("invalid range reached caller")
	}
}
func TestDigestCategoriesAndSeconds(t *testing.T) {
	names := []string{"BUSINESS_TYPE_MEMBER_INTERACTION_NOTIFY", "BUSINESS_TYPE_VISTOR_INTERACTION_NOTIFY", "BUSINESS_TYPE_MANAGE", "BUSINESS_TYPE_GUILD_ASSISTANT"}
	items := []any{}
	for _, name := range names {
		items = append(items, map[string]any{"businessType": name, "latestTimeStampMs": "18446744073709551615"})
	}
	items = append(items, map[string]any{"businessType": "BUSINESS_TYPE_VISITOR_INTERACTION_NOTIFY", "latestTimeStampMs": false}) // unknown is ignored, not corrected
	c := &fixtureCaller{response: map[string]any{"datas": items, "nextCallTimeStamp": "110"}}
	got, err := (Fetcher{c}).ReadDigest(context.Background(), time.Unix(100, 0))
	if err != nil || len(got.Entries) != 4 || got.Delay != 10*time.Second || got.Entries[0].Category != DigestInteraction || got.Entries[2].Category != DigestSystem {
		t.Fatal("digest interpretation failed")
	}
	for _, bad := range []any{float64(110), "bad", "-1", "9223372036854775808", nil} {
		c.response["nextCallTimeStamp"] = bad
		if _, err := (Fetcher{c}).ReadDigest(context.Background(), time.Unix(100, 0)); err == nil {
			t.Fatal("invalid delay accepted")
		}
	}
	c.response["nextCallTimeStamp"] = "90"
	if got, err := (Fetcher{c}).ReadDigest(context.Background(), time.Unix(100, 0)); err != nil || got.Delay != 0 {
		t.Fatal("past delay not clamped")
	}
	c.response["datas"] = append(items, items[0])
	if _, err := (Fetcher{c}).ReadDigest(context.Background(), time.Unix(100, 0)); err == nil {
		t.Fatal("duplicate business silently overwritten")
	}
	for _, bad := range []any{float64(1), "", nil} {
		c.response["datas"] = []any{map[string]any{"businessType": names[0], "latestTimeStampMs": bad}}
		if _, err := (Fetcher{c}).ReadDigest(context.Background(), time.Unix(100, 0)); err == nil {
			t.Fatal("invalid known digest timestamp accepted")
		}
	}
}

func TestObservedAuthenticatedEmptyEnvelopes(t *testing.T) {
	for _, key := range []string{"notices", "noticeList", "rptMyDirectMessageInfo"} {
		got, err := fetchMaps(map[string]any{}, key)
		if err != nil || len(got) != 0 {
			t.Fatal("empty protobuf map rejected")
		}
	}
	got, err := fetchMaps(map[string]any{"attachInfo": "synthetic-opaque-cursor", "isFinish": true}, "notices")
	if err != nil || len(got) != 0 {
		t.Fatal("finished interaction envelope rejected")
	}
	for _, p := range []map[string]any{{"isFinish": false, "attachInfo": ""}, {"unrecognized": "ignored"}, {"noticeList": nil}} {
		if _, err := fetchMaps(p, "noticeList"); err == nil {
			t.Fatal("malformed envelope accepted")
		}
	}
}
