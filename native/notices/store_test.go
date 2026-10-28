//go:build darwin || linux

package notices

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func syntheticState() SchemaEnvelope {
	return SchemaEnvelope{Version: SchemaVersion,
		Baseline:    Baseline{Sources: map[string]*SourceBaseline{"interact": {Watermark: 12, AtWatermark: []string{"synthetic-event"}}}},
		Deliveries:  map[string]DeliveryRecord{"synthetic-ledger": {RouteKey: "synthetic-route", Source: Interaction, EventKey: "synthetic-event", State: DeliveryPending, UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}},
		Initialized: map[Source]bool{Interaction: true}}
}
func dedicatedPath(t *testing.T) string {
	t.Helper()
	// macOS temporary roots may themselves be symlink aliases. The store must
	// reject such aliases; fixtures explicitly select their canonical directory.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "private", "state.json")
}
func openFixture(t *testing.T, path string) *StateStore {
	t.Helper()
	s, err := NewStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func assertMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != mode {
		t.Fatal("unexpected permissions")
	}
}

func TestStoreRoundTripPermissionsAndLock(t *testing.T) {
	path := dedicatedPath(t)
	s := openFixture(t, path)
	assertMode(t, filepath.Dir(path), 0700)
	assertMode(t, path+".lock", 0600)
	if other, err := NewStateStore(path); err != ErrInvalid {
		if other != nil {
			other.Close()
		}
		t.Fatal("exclusive lock not enforced")
	}
	empty, err := s.Load()
	if err != nil || empty.Version != 1 || len(empty.Deliveries) != 0 {
		t.Fatal("missing state initialization")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Load created state")
	}
	want := syntheticState()
	if s.Save(want) != nil {
		t.Fatal("save failed")
	}
	assertMode(t, path, 0600)
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("roundtrip mismatch")
	}
	if s.Close() != nil {
		t.Fatal("close")
	}
	if _, err = s.Load(); err != ErrInvalid {
		t.Fatal("closed store accepted")
	}
	reopened := openFixture(t, path)
	got, err = reopened.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("restart recovery mismatch")
	}
}

func TestStoreRejectsCorruption(t *testing.T) {
	fixtures := []string{"", " ", "{", "null", "{}", `{"version":1}`, `{"version":1,"baseline":{"sources":{}},"deliveries":null,"initialized":{}}`, `{"version":2}`, `{"version":1,"unknown":true}`, `{"version":1} {}`, `{"version":1,"baseline":{"sources":{"interact":{"unknown":1}}}}`, `{"version":"1"}`, `{"version":1,"deliveries":{"synthetic":{"body":"forbidden"}}}`}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			path := dedicatedPath(t)
			s := openFixture(t, path)
			if os.WriteFile(path, []byte(fixture), 0600) != nil {
				t.Fatal("fixture write")
			}
			if _, err := s.Load(); err != ErrInvalid {
				t.Fatal("corruption accepted")
			}
		})
	}
	path := dedicatedPath(t)
	s := openFixture(t, path)
	if os.WriteFile(path, bytes.Repeat([]byte(" "), (8<<20)+1), 0600) != nil {
		t.Fatal("fixture write")
	}
	if _, err := s.Load(); err != ErrInvalid {
		t.Fatal("oversize accepted")
	}
}

func TestStoreRefusesSymlinksAndUnsafeFiles(t *testing.T) {
	for _, target := range []string{"state", "lock", "parent"} {
		t.Run(target, func(t *testing.T) {
			path := dedicatedPath(t)
			if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(filepath.Dir(path), "synthetic-target")
			if target == "parent" {
				if os.Mkdir(destination, 0700) != nil {
					t.Fatal("mkdir")
				}
				path = filepath.Join(filepath.Dir(path), "alias", "state.json")
				if os.Symlink(destination, filepath.Dir(path)) != nil {
					t.Fatal("symlink")
				}
			} else {
				if os.WriteFile(destination, []byte("synthetic-unchanged"), 0600) != nil {
					t.Fatal("fixture write")
				}
				link := path
				if target == "lock" {
					link += ".lock"
				}
				if os.Symlink(destination, link) != nil {
					t.Fatal("symlink")
				}
			}
			if s, err := NewStateStore(path); err != ErrInvalid {
				if s != nil {
					s.Close()
				}
				t.Fatal("symlink accepted")
			}
			if target != "parent" {
				data, _ := os.ReadFile(destination)
				if string(data) != "synthetic-unchanged" {
					t.Fatal("target changed")
				}
			}
		})
	}
	for _, path := range []string{"", " ", filepath.Join(t.TempDir(), ".qqcli", "state.json")} {
		if s, err := NewStateStore(path); err != ErrInvalid {
			if s != nil {
				s.Close()
			}
			t.Fatal("invalid/live path accepted")
		}
	}
	path := dedicatedPath(t)
	s := openFixture(t, path)
	if s.Save(syntheticState()) != nil {
		t.Fatal("save")
	}
	if os.Chmod(path, 0644) != nil {
		t.Fatal("chmod")
	}
	if _, err := s.Load(); err != ErrInvalid {
		t.Fatal("public state accepted")
	}
	if s.Save(syntheticState()) != ErrInvalid {
		t.Fatal("public state overwritten")
	}
	assertMode(t, path, 0644)
}

func TestSaveAtomicSnapshotAndNoImplicitAdvance(t *testing.T) {
	path := dedicatedPath(t)
	s := openFixture(t, path)
	state := syntheticState()
	if s.Save(state) != nil {
		t.Fatal("save")
	}
	before, _ := os.ReadFile(path)
	invalid := syntheticState()
	invalid.Version = 99
	if s.Save(invalid) != ErrInvalid {
		t.Fatal("invalid save accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed save changed committed snapshot")
	}
	// Concurrent independent readers must see complete old or new JSON, never a
	// partially truncated file. This is not a power-loss/crash-injection proof.
	var wg sync.WaitGroup
	done := make(chan struct{})
	failures := make(chan bool, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := os.ReadFile(path)
			var snapshot SchemaEnvelope
			if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot.validate() != nil {
				select {
				case failures <- true:
				default:
				}
				return
			}
		}
	}()
	for i := 0; i < 20; i++ {
		d := state.Deliveries["synthetic-ledger"]
		if i%2 == 0 {
			d.State = DeliveryDelivered
			d.Receipt = "synthetic-receipt"
		} else {
			d.State = DeliveryPending
			d.Receipt = ""
		}
		state.Deliveries["synthetic-ledger"] = d
		if s.Save(state) != nil {
			close(done)
			wg.Wait()
			t.Fatal("save")
		}
	}
	close(done)
	wg.Wait()
	select {
	case <-failures:
		t.Fatal("non-atomic snapshot observed")
	default:
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, state) {
		t.Fatal("caller snapshot mutated")
	}
	if got.Baseline.Sources["interact"].Watermark != 12 {
		t.Fatal("implicit marker advance")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".notice-state-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary file leak")
	}
}

func TestExistingDirectoryModePreserved(t *testing.T) {
	path := dedicatedPath(t)
	if os.Mkdir(filepath.Dir(path), 0750) != nil || os.Chmod(filepath.Dir(path), 0750) != nil {
		t.Fatal("mkdir")
	}
	openFixture(t, path)
	assertMode(t, filepath.Dir(path), 0750)
}
