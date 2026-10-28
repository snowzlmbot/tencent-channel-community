package notices

import "time"

// These DTOs reproduce confirmed persisted metadata, not the legacy implementation.
type SubscriptionState struct {
	Active    bool      `json:"active"`
	GuildID   string    `json:"guild_id,omitempty"`
	MyTinyID  string    `json:"my_tiny_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
type NativeRoute struct {
	Targets     []NativeRouteTarget `json:"targets,omitempty"`
	OpenClawBin string              `json:"openclaw_bin,omitempty"`
}
type NativeRouteTarget struct {
	SessionKey string `json:"session_key"`
}
type Baseline struct {
	Sources   map[string]*SourceBaseline `json:"sources"`
	MyTinyID  string                     `json:"my_tiny_id,omitempty"`
	NextRef   int                        `json:"next_ref,omitempty"`
	DM        *DMBaseline                `json:"dm,omitempty"`
	LegacyIDs []string                   `json:"ids,omitempty"`
}
type SourceBaseline struct {
	Watermark     int64          `json:"watermark"`
	AtWatermark   []string       `json:"at_watermark,omitempty"`
	RecentNotices []RecentNotice `json:"recent_notices,omitempty"`
	OldIDs        []string       `json:"ids,omitempty"`
}
type DMBaseline struct {
	Channels []DMChannelBaseline `json:"channels"`
}
type DMChannelBaseline struct {
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	LastSeq   string `json:"last_seq"`
	LastTime  string `json:"last_time,omitempty"`
}
type RecentNotice struct {
	Ref               int    `json:"ref"`
	Source            string `json:"source"`
	FeedID            string `json:"feed_id,omitempty"`
	GuildID           string `json:"guild_id,omitempty"`
	GuildName         string `json:"guild_name,omitempty"`
	ChannelID         string `json:"channel_id,omitempty"`
	PosterNick        string `json:"poster_nick,omitempty"`
	PosterID          string `json:"poster_id,omitempty"`
	Type              string `json:"type,omitempty"`
	Summary           string `json:"summary,omitempty"`
	FeedCreateTime    string `json:"feed_create_time,omitempty"`
	FeedAuthorID      string `json:"feed_author_id,omitempty"`
	Timestamp         int64  `json:"ts,omitempty"`
	NoticeID          string `json:"notice_id,omitempty"`
	NoticeActionList  string `json:"notice_action_list,omitempty"`
	CommentID         string `json:"comment_id,omitempty"`
	CommentAuthorID   string `json:"comment_author_id,omitempty"`
	CommentCreateTime string `json:"comment_create_time,omitempty"`
	ReplyID           string `json:"reply_id,omitempty"`
	FetchedAt         int64  `json:"fetched_at,omitempty"`
	Pushed            bool   `json:"pushed,omitempty"`
	PushedAt          int64  `json:"pushed_at,omitempty"`
}

const SchemaVersion = 1

type DeliveryState string

const (
	DeliveryPending   DeliveryState = "pending"
	DeliveryDelivered DeliveryState = "delivered"
)

// DeliveryRecord contains identifiers and acknowledgement metadata, never event bodies.
// RouteKey is caller-owned opaque identity; it is not inferred from a session key.
type DeliveryRecord struct {
	RouteKey  string        `json:"route_key"`
	Source    Source        `json:"source"`
	EventKey  string        `json:"event_key"`
	State     DeliveryState `json:"state"`
	Receipt   string        `json:"receipt,omitempty"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// SchemaEnvelope is NEW compatibility state. It is not a legacy state file and
// deliberately excludes Manager authorization, secrets and test challenges.
type SchemaEnvelope struct {
	Control     *ControlState             `json:"control,omitempty"`
	Version     int                       `json:"version"`
	Baseline    Baseline                  `json:"baseline"`
	Deliveries  map[string]DeliveryRecord `json:"deliveries"`
	Initialized map[Source]bool           `json:"initialized"`
}

func (s SchemaEnvelope) validate() error {
	if s.Control.Validate() != nil {
		return ErrInvalid
	}
	if s.Version != SchemaVersion || s.Deliveries == nil || s.Initialized == nil || s.Baseline.Sources == nil {
		return ErrInvalid
	}
	for source := range s.Initialized {
		if !source.valid() {
			return ErrInvalid
		}
	}
	for key, d := range s.Deliveries {
		if !valid(key) || !valid(d.RouteKey) || !valid(d.EventKey) || !d.Source.valid() || d.UpdatedAt.IsZero() {
			return ErrInvalid
		}
		if d.State != DeliveryPending && d.State != DeliveryDelivered {
			return ErrInvalid
		}
		if d.State == DeliveryDelivered && !valid(d.Receipt) || d.State == DeliveryPending && d.Receipt != "" {
			return ErrInvalid
		}
	}
	return nil
}
