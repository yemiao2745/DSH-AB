package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	slotA = "slot-a"
	slotB = "slot-b"

	phaseNone    = "none"
	phasePending = "pending"
)

func validSlot(s string) bool { return s == slotA || s == slotB }

// otherSlot is the slot a rollback would move to.
func otherSlot(active string) string {
	if active == slotA {
		return slotB
	}
	return slotA
}

// Registration is one registered slot change. It is written to disk verbatim:
// the two state files are the single source of truth. The target slot is the
// whole registration - the two-state tray switch never asks what put it there.
type Registration struct {
	TargetSlot string `json:"target_slot"`
}

type activeFile struct {
	Slot string `json:"slot"`
}

// State owns state/active.json and state/pending.json.
type State struct {
	mu      sync.Mutex
	dir     string
	active  string
	Pending *Registration

	// Warnings collects recoverable problems (unreadable file, bad JSON) for
	// the caller to log. A damaged file never stops the program.
	Warnings []string
}

// LoadState reads the two state files, creating state/ and a fresh active.json
// on first run. Corrupt files fall back to their default and are reported
// through Warnings.
func LoadState(dir string) (*State, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &State{dir: dir, active: slotA}

	var af activeFile
	if ok, err := readJSON(filepath.Join(dir, "active.json"), &af); err != nil {
		s.Warnings = append(s.Warnings, fmt.Sprintf("active.json 无法解析（%v），本次按 %s 处理", err, slotA))
	} else if ok && validSlot(af.Slot) {
		s.active = af.Slot
	} else if ok {
		s.Warnings = append(s.Warnings, fmt.Sprintf("active.json 内容无效（%q），本次按 %s 处理", af.Slot, slotA))
	}

	var pending Registration
	if ok, err := readJSON(filepath.Join(dir, "pending.json"), &pending); err != nil {
		s.Warnings = append(s.Warnings, fmt.Sprintf("pending.json 无法解析（%v），已忽略该登记", err))
	} else if ok {
		if validSlot(pending.TargetSlot) {
			s.Pending = &pending
		} else {
			s.Warnings = append(s.Warnings, fmt.Sprintf("pending.json 目标槽无效（%q），已忽略该登记", pending.TargetSlot))
		}
	}

	// An archive written by the older three-state build means nothing now that
	// undo goes straight back to the default state, so it is dropped instead of
	// lingering in state/ (user 2026-09-19).
	if err := removeIfPresent(filepath.Join(dir, "undone.json")); err != nil {
		return nil, err
	}

	if err := s.save(); err != nil {
		return s, err
	}
	return s, nil
}

func (s *State) ActiveSlot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Reload re-reads both state files and adopts what is on disk as authoritative,
// reporting whether anything really changed.
//
// It exists because a registration written by the maintenance agent was invisible:
// State was read once at startup, so the menu kept saying 槽位切换, the status popup
// kept saying 未登记, and 重启 read a nil in-memory Pending and silently did not switch
// at all. Reload is called from the tray's 2 second refresh loop.
//
// Reload never writes the files back: writing them back is exactly what would erase a
// registration somebody else just wrote. A file that is missing or unreadable keeps the
// last good in-memory value and is reported through Warnings instead of clearing state;
// a missing pending.json is the normal "nothing is registered", so that one clears.
func (s *State) Reload() (changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var af activeFile
	switch ok, err := readJSON(filepath.Join(s.dir, "active.json"), &af); {
	case err != nil:
		s.warnLocked(fmt.Sprintf("active.json 无法解析（%v），继续按内存里的 %s 处理", err, s.active))
	case !ok:
		s.warnLocked(fmt.Sprintf("active.json 不见了，继续按内存里的 %s 处理", s.active))
	case !validSlot(af.Slot):
		s.warnLocked(fmt.Sprintf("active.json 内容无效（%q），继续按内存里的 %s 处理", af.Slot, s.active))
	case af.Slot != s.active:
		s.active, changed = af.Slot, true
	}

	var pending Registration
	switch ok, err := readJSON(filepath.Join(s.dir, "pending.json"), &pending); {
	case err != nil:
		s.warnLocked(fmt.Sprintf("pending.json 无法解析（%v），保留内存里的登记", err))
	case !ok:
		if s.Pending != nil {
			s.Pending, changed = nil, true
		}
	case !validSlot(pending.TargetSlot):
		s.warnLocked(fmt.Sprintf("pending.json 目标槽无效（%q），保留内存里的登记", pending.TargetSlot))
	case s.Pending == nil || s.Pending.TargetSlot != pending.TargetSlot:
		p := pending
		s.Pending, changed = &p, true
	}
	return changed
}

// warnLocked appends one warning, but never the same one twice in a row: Reload runs
// every two seconds, and a file that stays broken must not grow Warnings forever or
// fill the log with one identical line per tick.
func (s *State) warnLocked(msg string) {
	if n := len(s.Warnings); n > 0 && s.Warnings[n-1] == msg {
		return
	}
	s.Warnings = append(s.Warnings, msg)
}

// RollbackPhase is what menu item 4 shows: not registered / waiting to take
// effect. Those two are the whole state machine (user 2026-09-19).
func (s *State) RollbackPhase() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Pending != nil {
		return phasePending
	}
	return phaseNone
}

// Register records a slot change; only the tray's 重启 applies it. Registering
// never stops a process and never touches the other slot.
func (s *State) Register(target string) error {
	if !validSlot(target) {
		return fmt.Errorf("目标槽无效: %q", target)
	}
	s.mu.Lock()
	s.Pending = &Registration{TargetSlot: target}
	err := s.saveLocked()
	s.mu.Unlock()
	return err
}

// Undo clears the registration: the menu goes straight back to its default
// state, with nothing archived (user 2026-09-19).
func (s *State) Undo() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Pending == nil {
		return fmt.Errorf("当前没有待生效的槽位切换")
	}
	s.Pending = nil
	return s.saveLocked()
}

// ConsumePending makes the registration effective: the target becomes the
// active slot and the registration is cleared. Only the tray's 重启 calls it.
func (s *State) ConsumePending() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Pending == nil {
		return "", false
	}
	target := s.Pending.TargetSlot
	s.active = target
	s.Pending = nil
	if err := s.saveLocked(); err != nil {
		return "", false
	}
	return target, true
}

func (s *State) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// saveLocked writes active.json always, and removes pending.json when there is
// no registration, so no stale registration can survive.
func (s *State) saveLocked() error {
	if err := writeJSON(filepath.Join(s.dir, "active.json"), activeFile{Slot: s.active}); err != nil {
		return err
	}
	pendingPath := filepath.Join(s.dir, "pending.json")
	if s.Pending == nil {
		return removeIfPresent(pendingPath)
	}
	return writeJSON(pendingPath, *s.Pending)
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// readJSON reports (found, error). A missing file is not an error.
func readJSON(path string, dst any) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, err
	}
	return true, nil
}

// writeJSON is atomic: a half-written state file would be worse than a stale one.
func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
