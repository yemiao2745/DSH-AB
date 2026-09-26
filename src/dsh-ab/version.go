package main

import (
	_ "embed"
	"fmt"
	"io"
	"strings"
)

// dshabVersion and dshVersion are baked in at build time by build/py/build.py:
//
//	-ldflags "-X main.dshabVersion=<dshab> -X main.dshVersion=<dsh>"
//
// src/dsh-ab/VERSION is the single source of truth for this program's own version,
// so a plain go build (go test, go vet) reports a real version instead of nothing.
// The dsh version has no such file on purpose: which dsh a build carries is a build
// parameter (one installer per upstream rc), never a constant of the source tree.
var (
	dshabVersion string
	dshVersion   string
)

//go:embed VERSION
var versionFile string

func effectiveDshabVersion() string {
	if v := strings.TrimSpace(dshabVersion); v != "" {
		return v
	}
	return strings.TrimSpace(versionFile)
}

// versionText is the one line every consumer parses: the installer name, the
// welcome pages and the verification scripts all name the same two versions.
//
// One shape, no branches: every build injects a dsh version. The in-place update pack
// carries the very same exe the installer does, so there is no longer a build that has no
// dsh to name - the empty-dsh branch this used to have existed only for the pack's own
// separately built exe (build/py/build.py), and it went away with that build.
func versionText(dshab, dsh string) string {
	return "DSH-AB " + strings.TrimSpace(dshab) + " (dsh " + strings.TrimSpace(dsh) + ")"
}

// handleVersionFlag answers --version and reports whether the program must stop.
// It runs before the single-instance mutex and before the tray starts, so asking a
// built exe which versions it carries never opens a window and never trips the
// "already running" popup.
func handleVersionFlag(args []string, out io.Writer) bool {
	if what, _ := classifyCommandLine(args); what == printVersion {
		fmt.Fprintln(out, versionText(effectiveDshabVersion(), dshVersion))
		return true
	}
	return false
}
