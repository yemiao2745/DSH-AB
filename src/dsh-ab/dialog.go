package main

import (
	"sync"
	"time"
)

// Dialog button and icon flags. messageBox itself lives in dialog_popup.go.
const (
	mbOK          = 0x00000000
	mbYesNo       = 0x00000004
	mbIconError   = 0x00000010
	mbIconWarning = 0x00000030
	mbIconInfo    = 0x00000040
	idYes         = 6
)

// alert is an information popup. User-initiated actions are never throttled.
func alert(title, text string) {
	messageBox(title, text, mbOK|mbIconInfo)
}

// alertErr is the same popup with the error icon.
func alertErr(title, text string) {
	messageBox(title, text, mbOK|mbIconError)
}

// confirm asks a yes/no question and reports whether the user agreed.
func confirm(title, text string) bool {
	return messageBox(title, text, mbYesNo|mbIconWarning) == idYes
}

// throttle suppresses a repeating fault popup inside a fixed window, so a dead
// process cannot pop up on every health tick.
type throttle struct {
	mu     sync.Mutex
	window time.Duration
	last   map[string]time.Time
}

func newThrottle(window time.Duration) *throttle {
	return &throttle{window: window, last: map[string]time.Time{}}
}

// Allow reports whether this category may be shown now, and records the time
// when it may. A non-positive window disables throttling.
func (t *throttle) Allow(category string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.window <= 0 {
		return true
	}
	now := time.Now()
	if last, ok := t.last[category]; ok && now.Sub(last) < t.window {
		return false
	}
	t.last[category] = now
	return true
}
