//go:build darwin || linux

package notices

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// StateStore is preparatory persistence only: no migration, polling, authorization
// or automatic marker advancement. Callers must supply a dedicated NEW file.
// Parent directories must remain under trusted ownership during operations;
// path checks reject symlinks but do not sandbox hostile concurrent renames.
type StateStore struct {
	mu   sync.Mutex
	path string
	dir  *os.File
	lock *os.File
}

func privateOpen(path string, flags int) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, ErrInvalid
	}
	f := os.NewFile(uintptr(fd), "state")
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Nlink != 1 {
		f.Close()
		return nil, ErrInvalid
	}
	return f, nil
}

// checkParents never chmods existing directories. Only missing ones get 0700.
func checkParents(dir string, create bool) error {
	current := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(dir, current), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		st, err := os.Lstat(current)
		if os.IsNotExist(err) && create {
			if err = os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
				return ErrInvalid
			}
			st, err = os.Lstat(current)
		}
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
	}
	return nil
}

func NewStateStore(path string) (*StateStore, error) {
	if !valid(path) {
		return nil, ErrInvalid
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrInvalid
	}
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".qqcli" {
			return nil, ErrInvalid
		}
	}
	parent := filepath.Dir(path)
	if checkParents(parent, true) != nil {
		return nil, ErrInvalid
	}
	fd, err := syscall.Open(parent, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrInvalid
	}
	dir := os.NewFile(uintptr(fd), "state-directory")
	lock, err := privateOpen(path+".lock", syscall.O_RDWR|syscall.O_CREAT)
	if err != nil {
		dir.Close()
		return nil, ErrInvalid
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		lock.Close()
		dir.Close()
		return nil, ErrInvalid
	}
	s := &StateStore{path: path, dir: dir, lock: lock}
	if s.check() != nil {
		s.Close()
		return nil, ErrInvalid
	}
	return s, nil
}

func (s *StateStore) check() error {
	if s.lock == nil || checkParents(filepath.Dir(s.path), false) != nil {
		return ErrInvalid
	}
	pinnedLock, e1 := s.lock.Stat()
	currentLock, e2 := os.Lstat(s.path + ".lock")
	if e1 != nil || e2 != nil || !currentLock.Mode().IsRegular() || currentLock.Mode().Perm() != 0600 || !os.SameFile(pinnedLock, currentLock) {
		return ErrInvalid
	}
	pinned, e1 := s.dir.Stat()
	current, e2 := os.Lstat(filepath.Dir(s.path))
	if e1 != nil || e2 != nil || !os.SameFile(pinned, current) {
		return ErrInvalid
	}
	st, err := os.Lstat(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Nlink != 1 {
		return ErrInvalid
	}
	return nil
}

func (s *StateStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	e1, e2 := s.lock.Close(), s.dir.Close()
	s.lock, s.dir = nil, nil
	if e1 != nil || e2 != nil {
		return ErrInvalid
	}
	return nil
}

// Load rejects unknown fields recursively, trailing documents and oversized input.
// Missing new state is an explicit empty v1 state; empty/corrupt files fail closed.
func (s *StateStore) Load() (SchemaEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out SchemaEnvelope
	if s.check() != nil {
		return out, ErrInvalid
	}
	if _, err := os.Lstat(s.path); os.IsNotExist(err) {
		return SchemaEnvelope{Version: SchemaVersion, Baseline: Baseline{Sources: map[string]*SourceBaseline{}}, Deliveries: map[string]DeliveryRecord{}, Initialized: map[Source]bool{}}, nil
	}
	f, err := privateOpen(s.path, syscall.O_RDONLY)
	if err != nil {
		return out, ErrInvalid
	}
	defer f.Close()
	const maxState = 8 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxState+1))
	if err != nil || len(data) > maxState {
		return out, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF || out.validate() != nil {
		return SchemaEnvelope{}, ErrInvalid
	}
	return out, nil
}

// Save persists exactly the caller's snapshot, including explicit acknowledgement
// decisions. An error after rename may mean committed data: reload before retrying.
func (s *StateStore) Save(state SchemaEnvelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.check() != nil || state.validate() != nil {
		return ErrInvalid
	}
	data, err := json.Marshal(state)
	if err != nil || len(data) > 8<<20 {
		return ErrInvalid
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".notice-state-*")
	if err != nil {
		return ErrInvalid
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if temp.Chmod(0600) != nil {
		return ErrInvalid
	}
	if _, err = temp.Write(data); err != nil {
		return ErrInvalid
	}
	if temp.Sync() != nil || temp.Close() != nil || s.check() != nil {
		return ErrInvalid
	}
	if os.Rename(temp.Name(), s.path) != nil || s.dir.Sync() != nil {
		return ErrInvalid
	}
	return nil
}
