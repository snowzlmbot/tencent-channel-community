package notices

import (
	"context"
	"testing"
)

type fixture struct {
	route   Route
	receipt string
	fail    bool
	hook    func()
}

func (f *fixture) Resolve(context.Context, string) (Route, error) { return f.route, nil }
func (f *fixture) SendTest(context.Context, Route) (string, error) {
	if f.hook != nil {
		f.hook()
	}
	if f.fail {
		return "", ErrInvalid
	}
	return f.receipt, nil
}
func TestActivationFixtures(t *testing.T) {
	base := Route{"session", "telegram", "-100", "42", "account"}
	ctx := context.Background()
	for _, tc := range []struct {
		name, action string
		want         bool
	}{
		{"valid", "confirm", true}, {"explicit consent", "no", false}, {"session mismatch", "session", false}, {"missing topic", "topic", false}, {"control", "control", false}, {"blank account", "account", false}, {"empty receipt", "empty", false}, {"delivery failure", "fail", false}, {"stale destination", "stale", false}, {"changed confirmation", "mutate", false}, {"wrong receipt", "receipt", false}, {"off during delivery", "off", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fixture{route: base, receipt: "receipt"}
			m, _ := NewManager("session", f, f)
			switch tc.action {
			case "session":
				f.route.SessionKey = "other"
			case "topic":
				f.route.ThreadID = ""
			case "control":
				f.route.Target = "bad\n"
			case "account":
				f.route.AccountID = " "
			case "empty":
				f.receipt = ""
			case "fail":
				f.fail = true
			case "off":
				f.hook = m.Off
			}
			r, receipt, err := m.Test(ctx)
			if err == nil {
				if m.Phase() != TESTED {
					t.Fatal("not tested")
				}
				switch tc.action {
				case "stale":
					f.route.Target = "-200"
				case "mutate":
					r.AccountID = "other"
				case "receipt":
					receipt = "wrong"
				}
				err = m.Confirm(ctx, r, receipt, tc.action != "no")
			}
			if (err == nil) != tc.want {
				t.Fatalf("result %v", err)
			}
			if !tc.want {
				if m.Phase() == ACTIVE {
					t.Fatal("unauthorized activation")
				}
				return
			}
			if m.Confirm(ctx, r, receipt, true) == nil {
				t.Fatal("confirmation replay")
			}
			if _, err = m.AuthorizedRoute(ctx, "other"); err == nil {
				t.Fatal("cross session")
			}
			authorized, err := m.AuthorizedRoute(ctx, "session")
			if err != nil {
				t.Fatal(err)
			}
			authorized.Target = "changed"
			if again, err := m.AuthorizedRoute(ctx, "session"); err != nil || again != base {
				t.Fatal("authorization mutated")
			}
			f.route.AccountID = "new"
			if _, err = m.AuthorizedRoute(ctx, "session"); err == nil {
				t.Fatal("stale authorization")
			}
			f.route = base
			m.Off()
			if _, err = m.AuthorizedRoute(ctx, "session"); err == nil {
				t.Fatal("off authorized")
			}
		})
	}
}
func TestEventFixtures(t *testing.T) {
	tracker := NewTracker()
	for _, source := range []Source{Interaction, System, DM} {
		a := Event{source, "a", 10}
		b := Event{source, "b", 10}
		for _, step := range []struct {
			op string
			e  Event
			ok bool
		}{
			{"accept", a, true}, {"accept", a, true}, {"accept", b, false}, {"ack", b, false}, {"fail", a, false}, {"accept", a, true}, {"ack", a, true}, {"accept", a, false}, {"accept", Event{source, "older", 9}, false}, {"accept", b, true}, {"ack", b, true}, {"accept", a, false}, {"ack", b, false}, {"accept", Event{source, "new", 11}, true},
		} {
			var ok bool
			var err error
			switch step.op {
			case "accept":
				ok, err = tracker.Accept(step.e)
				ok = ok && err == nil
			case "ack":
				err = tracker.Ack(step.e, true)
				ok = err == nil
			case "fail":
				err = tracker.Ack(step.e, false)
				ok = err == nil
			}
			if ok != step.ok {
				t.Fatalf("%s %s: %v", source, step.op, err)
			}
			if step.op == "fail" && tracker.Snapshot()[source].Timestamp != 0 {
				t.Fatal("failure advanced watermark")
			}
		}
		snapshot := tracker.Snapshot()
		w := snapshot[source]
		w.IDs[0] = "tamper"
		w.Pending.ID = "tamper"
		if tracker.Snapshot()[source].IDs[0] != "a" || tracker.Snapshot()[source].Pending.ID != "new" {
			t.Fatal("snapshot alias")
		}
	}
	for _, e := range []Event{{"bad", "id", 1}, {DM, "", 1}, {DM, "bad\n", 1}, {DM, "id", 0}, {DM, "id", -1}} {
		if _, err := tracker.Accept(e); err == nil {
			t.Fatal("invalid accepted")
		}
	}
}
