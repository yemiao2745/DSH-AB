package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestParseWebURL pins the exact line dsh prints at startup (measured from
// dsh 0.1.5-rc.2 with --no-open):
//
//	dsh web: http://127.0.0.1:3299/?token=6lV2Lq5EZhVs2eluq1-4d1LfVdTZlKHacL5khnyB7ik
func TestParseWebURL(t *testing.T) {
	line := "dsh web: http://127.0.0.1:3299/?token=6lV2Lq5EZhVs2eluq1-4d1LfVdTZlKHacL5khnyB7ik"
	got := parseWebURL(line)
	want := "http://127.0.0.1:3299/?token=6lV2Lq5EZhVs2eluq1-4d1LfVdTZlKHacL5khnyB7ik"
	if got != want {
		t.Fatalf("parseWebURL = %q, want %q", got, want)
	}
}

// TestParseWebURLTakesLocalNotLAN: the LAN candidate is printed in parentheses
// after the local URL; the browser must get the local one.
func TestParseWebURLTakesLocalNotLAN(t *testing.T) {
	line := "dsh web: http://127.0.0.1:3190/?token=abc (LAN: http://10.0.0.5:3190/?token=def)"
	if got := parseWebURL(line); got != "http://127.0.0.1:3190/?token=abc" {
		t.Fatalf("parseWebURL = %q, want the local URL", got)
	}
}

// TestParseWebURLCannotReturnABareURL: nothing that is not an http(s) address
// may ever be read out of dsh output.
func TestParseWebURLCannotReturnABareURL(t *testing.T) {
	for _, line := range []string{
		"dsh web: opening the default browser; pass --no-open to disable",
		"DSH-AB: refusing to install, target directory is not empty",
		"dsh web:",
		"",
	} {
		if got := parseWebURL(line); got != "" {
			t.Fatalf("parseWebURL(%q) = %q, want empty", line, got)
		}
	}
}

// TestRunnerRemembersChildURL: the address must survive from the child's output
// into the Runner, and the Runner must never invent one when none was printed.
func TestRunnerRemembersChildURL(t *testing.T) {
	r, pw := newPipeRunner(t)
	fmt.Fprintln(pw, "dsh web: http://127.0.0.1:3190/?token=abc (LAN: http://10.0.0.5:3190/?token=def)")
	fmt.Fprintln(pw, "dsh web: opening the default browser; pass --no-open to disable")
	pw.Close()

	got, ok := r.AwaitURL(5 * time.Second)
	if !ok {
		t.Fatal("AwaitURL reported no address, want the printed one")
	}
	if got != "http://127.0.0.1:3190/?token=abc" {
		t.Fatalf("captured URL = %q", got)
	}
	if r.URL() != got {
		t.Fatalf("URL() = %q, want %q", r.URL(), got)
	}
}

// TestRunnerReportsMissingURL: a child that never prints the line must yield no
// URL rather than a fallback, so callers can fail loudly instead of opening a
// page that is guaranteed to answer 401.
func TestRunnerReportsMissingURL(t *testing.T) {
	r, pw := newPipeRunner(t)
	fmt.Fprintln(pw, "dsh web: opening the default browser; pass --no-open to disable")
	pw.Close()

	if _, ok := r.AwaitURL(300 * time.Millisecond); ok {
		t.Fatal("AwaitURL reported an address although none was printed")
	}
	if r.URL() != "" {
		t.Fatalf("URL() = %q, want empty", r.URL())
	}
}

// TestBrowserURLIsAlwaysTheAddressDshGave (OBS-002): dsh hands out the page address
// together with its one-time token, and it only swaps that token for a cookie on "/".
// authenticatedURL therefore returns exactly what dsh announced - it never builds an
// address of its own, because every other address answers 401.
func TestBrowserURLIsAlwaysTheAddressDshGave(t *testing.T) {
	const announced = "http://127.0.0.1:3190/?token=abc"

	lg := NewLogger(t.TempDir(), LevelAuto, 20, 5)
	defer lg.Close()

	a := &App{cfg: DefaultConfig(), run: &Runner{authURL: announced}, lg: lg}
	got, err := a.authenticatedURL(0)
	if err != nil {
		t.Fatalf("authenticatedURL: %v", err)
	}
	if got != announced {
		t.Fatalf("authenticatedURL = %q, want the address dsh announced %q", got, announced)
	}
}

// TestBrowserURLErrorsWithoutToken: without a captured address the caller gets a
// diagnosable error, never a bare URL.
func TestBrowserURLErrorsWithoutToken(t *testing.T) {
	a := &App{cfg: DefaultConfig(), run: &Runner{}}
	got, err := a.authenticatedURL(200 * time.Millisecond)
	if err == nil {
		t.Fatalf("authenticatedURL returned %q for a child that printed no address", got)
	}
	if got != "" {
		t.Fatalf("authenticatedURL = %q, want empty on failure", got)
	}
	if !strings.Contains(err.Error(), "3090") {
		t.Fatalf("error must name the port so the user can diagnose it: %v", err)
	}
}

// newPipeRunner wires a Runner to a pipe the test writes into, which is exactly
// how the real child's output arrives.
func newPipeRunner(t *testing.T) (*Runner, *os.File) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	cmd := &exec.Cmd{}
	r := &Runner{cmd: cmd, urlReady: make(chan struct{}), tail: newLineTail(childTailLines)}
	go r.pump(pr, cmd, r.tail, slotA)
	return r, pw
}
