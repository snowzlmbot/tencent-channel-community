// Package notices is an independently authored compatibility core, not recovered CLI source.
// It performs no I/O; adapters must implement the resolver and delivery contracts.
package notices

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode"
)

var ErrInvalid = errors.New("invalid input or transition")

func valid(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}

type Route struct{ SessionKey, Channel, Target, ThreadID, AccountID string }

func (r Route) Validate() error {
	for _, s := range []string{r.SessionKey, r.Channel, r.Target, r.AccountID} {
		if !valid(s) {
			return ErrInvalid
		}
	}
	if r.ThreadID != "" && !valid(r.ThreadID) {
		return ErrInvalid
	}
	// Telegram group destinations require an explicit topic, never a guessed one.
	if strings.EqualFold(r.Channel, "telegram") && strings.HasPrefix(r.Target, "-") && r.ThreadID == "" {
		return ErrInvalid
	}
	return nil
}

type Resolver interface {
	Resolve(context.Context, string) (Route, error)
}
type Delivery interface {
	SendTest(context.Context, Route) (string, error)
}
type Phase string

const (
	OFF    Phase = "OFF"
	TESTED Phase = "TESTED"
	ACTIVE Phase = "ACTIVE"
)

type Manager struct {
	mu       sync.Mutex
	key      string
	resolver Resolver
	delivery Delivery
	revision uint64
	phase    Phase
	route    Route
	receipt  string
}

func NewManager(key string, resolver Resolver, delivery Delivery) (*Manager, error) {
	if !valid(key) || resolver == nil || delivery == nil {
		return nil, ErrInvalid
	}
	return &Manager{key: key, resolver: resolver, delivery: delivery, phase: OFF}, nil
}
func (m *Manager) Off() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revision++
	m.phase = OFF
	m.route = Route{}
	m.receipt = ""
}
func (m *Manager) Phase() Phase { m.mu.Lock(); defer m.mu.Unlock(); return m.phase }
func (m *Manager) resolve(ctx context.Context) (Route, error) {
	r, err := m.resolver.Resolve(ctx, m.key)
	if err != nil {
		return Route{}, err
	}
	if r.Validate() != nil || r.SessionKey != m.key {
		return Route{}, ErrInvalid
	}
	return r, nil
}

// Test invalidates prior authorization before invoking any external callback.
func (m *Manager) Test(ctx context.Context) (Route, string, error) {
	m.mu.Lock()
	m.revision++
	rev := m.revision
	m.phase = OFF
	m.route = Route{}
	m.receipt = ""
	m.mu.Unlock()
	r, err := m.resolve(ctx)
	if err != nil {
		return Route{}, "", err
	}
	receipt, err := m.delivery.SendTest(ctx, r)
	if err != nil {
		return Route{}, "", err
	}
	if !valid(receipt) {
		return Route{}, "", ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revision != rev {
		return Route{}, "", ErrInvalid
	}
	m.route = r
	m.receipt = receipt
	m.phase = TESTED
	return r, receipt, nil
}

// Confirm is single-use and re-resolves the destination to reject stale routes.
func (m *Manager) Confirm(ctx context.Context, r Route, receipt string, userConfirmed bool) error {
	m.mu.Lock()
	if !userConfirmed || r.Validate() != nil || m.phase != TESTED || m.route != r || m.receipt != receipt {
		m.mu.Unlock()
		return ErrInvalid
	}
	rev := m.revision
	m.mu.Unlock()
	current, err := m.resolve(ctx)
	if err != nil {
		return err
	}
	if current != r {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revision != rev || m.phase != TESTED {
		return ErrInvalid
	}
	m.phase = ACTIVE
	m.revision++
	return nil
}

// AuthorizedRoute returns a value copy, never a mutable authorization reference.
func (m *Manager) AuthorizedRoute(ctx context.Context, key string) (Route, error) {
	m.mu.Lock()
	r, rev, phase := m.route, m.revision, m.phase
	m.mu.Unlock()
	if key != m.key || phase != ACTIVE {
		return Route{}, ErrInvalid
	}
	current, err := m.resolve(ctx)
	if err != nil {
		return Route{}, err
	}
	if current != r {
		return Route{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revision != rev || m.phase != ACTIVE {
		return Route{}, ErrInvalid
	}
	return r, nil
}

type Source string

const (
	Interaction Source = "interaction"
	System      Source = "system"
	DM          Source = "dm"
)

func (s Source) valid() bool { return s == Interaction || s == System || s == DM }

type Event struct {
	Source    Source
	ID        string
	Timestamp int64
}

func (e Event) valid() bool { return e.Source.valid() && valid(e.ID) && e.Timestamp > 0 }

// Watermark retains every acknowledged ID at its latest second, not event bodies.
type Watermark struct {
	Timestamp int64
	IDs       []string
	Pending   *Event
}
type DurableState map[Source]Watermark
type Tracker struct {
	mu    sync.Mutex
	state DurableState
}

func clone(s DurableState) DurableState {
	out := make(DurableState, len(s))
	for source, w := range s {
		w.IDs = append([]string(nil), w.IDs...)
		if w.Pending != nil {
			e := *w.Pending
			w.Pending = &e
		}
		out[source] = w
	}
	return out
}
func seen(w Watermark, e Event) bool {
	if e.Timestamp < w.Timestamp {
		return true
	}
	if e.Timestamp == w.Timestamp {
		for _, id := range w.IDs {
			if id == e.ID {
				return true
			}
		}
	}
	return false
}

// NewTracker starts empty; DurableState snapshots can be persisted by a future adapter.
func NewTracker() *Tracker                { return &Tracker{state: make(DurableState)} }
func (t *Tracker) Snapshot() DurableState { t.mu.Lock(); defer t.mu.Unlock(); return clone(t.state) }

// Accept reserves one event per source. The identical pending event may be retried.
func (t *Tracker) Accept(e Event) (bool, error) {
	if !e.valid() {
		return false, ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.state[e.Source]
	if w.Pending != nil {
		if *w.Pending == e {
			return true, nil
		}
		return false, ErrInvalid
	}
	if seen(w, e) {
		return false, nil
	}
	copy := e
	w.Pending = &copy
	t.state[e.Source] = w
	return true, nil
}

// Ack advances only an exact pending event after successful delivery. Failure keeps it retryable.
func (t *Tracker) Ack(e Event, delivered bool) error {
	if !e.valid() {
		return ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.state[e.Source]
	if w.Pending == nil || *w.Pending != e || !delivered {
		return ErrInvalid
	}
	if e.Timestamp > w.Timestamp {
		w.Timestamp = e.Timestamp
		w.IDs = nil
	}
	w.IDs = append(w.IDs, e.ID)
	w.Pending = nil
	t.state[e.Source] = w
	return nil
}
