package notices

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestRecoveredSchemaExact(t *testing.T) {
	// Types, field order and every tag independently transcribed from metadata.
	cases := []struct {
		value  any
		fields [][3]string
	}{
		{SubscriptionState{}, [][3]string{{"Active", "bool", "active"}, {"GuildID", "string", "guild_id,omitempty"}, {"MyTinyID", "string", "my_tiny_id,omitempty"}, {"CreatedAt", "time.Time", "created_at"}}},
		{NativeRoute{}, [][3]string{{"Targets", "[]notices.NativeRouteTarget", "targets,omitempty"}, {"OpenClawBin", "string", "openclaw_bin,omitempty"}}},
		{NativeRouteTarget{}, [][3]string{{"SessionKey", "string", "session_key"}}},
		{Baseline{}, [][3]string{{"Sources", "map[string]*notices.SourceBaseline", "sources"}, {"MyTinyID", "string", "my_tiny_id,omitempty"}, {"NextRef", "int", "next_ref,omitempty"}, {"DM", "*notices.DMBaseline", "dm,omitempty"}, {"LegacyIDs", "[]string", "ids,omitempty"}}},
		{SourceBaseline{}, [][3]string{{"Watermark", "int64", "watermark"}, {"AtWatermark", "[]string", "at_watermark,omitempty"}, {"RecentNotices", "[]notices.RecentNotice", "recent_notices,omitempty"}, {"OldIDs", "[]string", "ids,omitempty"}}},
		{DMBaseline{}, [][3]string{{"Channels", "[]notices.DMChannelBaseline", "channels"}}},
		{DMChannelBaseline{}, [][3]string{{"GuildID", "string", "guild_id"}, {"ChannelID", "string", "channel_id"}, {"LastSeq", "string", "last_seq"}, {"LastTime", "string", "last_time,omitempty"}}},
		{RecentNotice{}, [][3]string{
			{"Ref", "int", "ref"}, {"Source", "string", "source"}, {"FeedID", "string", "feed_id,omitempty"}, {"GuildID", "string", "guild_id,omitempty"}, {"GuildName", "string", "guild_name,omitempty"}, {"ChannelID", "string", "channel_id,omitempty"}, {"PosterNick", "string", "poster_nick,omitempty"}, {"PosterID", "string", "poster_id,omitempty"}, {"Type", "string", "type,omitempty"}, {"Summary", "string", "summary,omitempty"}, {"FeedCreateTime", "string", "feed_create_time,omitempty"}, {"FeedAuthorID", "string", "feed_author_id,omitempty"}, {"Timestamp", "int64", "ts,omitempty"}, {"NoticeID", "string", "notice_id,omitempty"}, {"NoticeActionList", "string", "notice_action_list,omitempty"}, {"CommentID", "string", "comment_id,omitempty"}, {"CommentAuthorID", "string", "comment_author_id,omitempty"}, {"CommentCreateTime", "string", "comment_create_time,omitempty"}, {"ReplyID", "string", "reply_id,omitempty"}, {"FetchedAt", "int64", "fetched_at,omitempty"}, {"Pushed", "bool", "pushed,omitempty"}, {"PushedAt", "int64", "pushed_at,omitempty"}}},
	}
	for _, c := range cases {
		typ := reflect.TypeOf(c.value)
		if typ.NumField() != len(c.fields) {
			t.Fatalf("%s field count", typ.Name())
		}
		for i, want := range c.fields {
			f := typ.Field(i)
			got := [3]string{f.Name, f.Type.String(), f.Tag.Get("json")}
			if got != want {
				t.Fatalf("%s field %d: got %v want %v", typ.Name(), i, got, want)
			}
		}
	}
}

func TestLegacyDTOFixtureRoundTrip(t *testing.T) {
	fixture := `{"sources":{"interact":{"watermark":42,"at_watermark":["synthetic-event"],"recent_notices":[{"ref":999,"source":"interact","notice_action_list":"[{\"synthetic\":true}]","pushed":true,"pushed_at":45}],"ids":["synthetic-old"]}},"my_tiny_id":"synthetic-self","next_ref":999,"dm":{"channels":[{"guild_id":"synthetic-guild","channel_id":"synthetic-channel","last_seq":"18446744073709551616000","last_time":"synthetic-time"}]},"ids":["synthetic-legacy"]}`
	var baseline Baseline
	if err := json.Unmarshal([]byte(fixture), &baseline); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	json.Unmarshal([]byte(fixture), &want)
	json.Unmarshal(data, &got)
	if !reflect.DeepEqual(want, got) {
		t.Fatal("legacy fixture shape changed")
	}
	if baseline.DM.Channels[0].LastSeq != "18446744073709551616000" {
		t.Fatal("sequence precision lost")
	}
	if json.Unmarshal([]byte(`{"sources":{"interact":{"recent_notices":[{"notice_action_list":[]}]}}}`), &baseline) == nil {
		t.Fatal("JSON string silently became array")
	}
}

func TestEnvelopeValidation(t *testing.T) {
	state := syntheticState()
	if state.validate() != nil {
		t.Fatal("valid fixture rejected")
	}
	cases := []DeliveryRecord{
		{RouteKey: "", Source: Interaction, EventKey: "synthetic", State: DeliveryPending, UpdatedAt: time.Now()},
		{RouteKey: "synthetic", Source: Source("invalid"), EventKey: "synthetic", State: DeliveryPending, UpdatedAt: time.Now()},
		{RouteKey: "synthetic", Source: Interaction, EventKey: "synthetic", State: DeliveryDelivered, UpdatedAt: time.Now()},
		{RouteKey: "synthetic", Source: Interaction, EventKey: "synthetic", State: DeliveryPending, Receipt: "synthetic", UpdatedAt: time.Now()},
		{RouteKey: "synthetic", Source: Interaction, EventKey: "synthetic", State: "unknown", UpdatedAt: time.Now()},
	}
	for _, d := range cases {
		state.Deliveries["synthetic-ledger"] = d
		if state.validate() != ErrInvalid {
			t.Fatal("invalid ledger accepted")
		}
	}
	state = syntheticState()
	state.Version = 2
	if state.validate() != ErrInvalid {
		t.Fatal("unknown version accepted")
	}
	state = syntheticState()
	state.Initialized[Source("unknown")] = true
	if state.validate() != ErrInvalid {
		t.Fatal("unknown initialization source accepted")
	}
}
