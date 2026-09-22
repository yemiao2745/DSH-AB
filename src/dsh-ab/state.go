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
