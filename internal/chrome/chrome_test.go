package chrome

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFindExplicitMissing(t *testing.T) {
	if _, _, err := Find("/nonexistent/wvf-browser-xyz"); err == nil {
		t.Fatal("missing explicit path must error")
	}
}

func TestFindExplicitScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake is POSIX-only")
	}
	// an executable that answers --version like a browser would
	p := filepath.Join(t.TempDir(), "fakechrome")
	// #nosec G306 -- fixture must be executable for Find/probeVersion to run it
	if err := os.WriteFile(p,
		[]byte("#!/bin/sh\necho 'Chromium 123.4.5.6 fake'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, ver, err := Find(p)
	if err != nil {
		t.Fatal(err)
	}
	if path != p {
		t.Fatalf("path: %q", path)
	}
	if ver != "Chromium 123.4.5.6 fake" {
		t.Fatalf("probeVersion: %q", ver)
	}
}

func TestFindExplicitNonExecutable(t *testing.T) {
	// exists but cannot answer --version → path found, version "unknown"
	p := filepath.Join(t.TempDir(), "notexec")
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, ver, err := Find(p)
	if err != nil {
		t.Fatal(err)
	}
	if path != p || ver != "unknown" {
		t.Fatalf("want (%q, unknown), got (%q, %q)", p, path, ver)
	}
}

func TestFindSkipsBrokenCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake is POSIX-only")
	}
	// a stub that exists and is executable but dies on --version —
	// the snap-placeholder case; Find must skip to a working browser.
	stub := filepath.Join(t.TempDir(), "stubchrome")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil { // #nosec G306 -- fixture must exec
		t.Fatal(err)
	}
	old := candidates
	candidates = append([]string{stub}, candidates...)
	defer func() { candidates = old }()
	path, ver, err := Find("")
	if err != nil {
		t.Skipf("no working chrome on this host: %v", err)
	}
	if path == stub {
		t.Fatal("Find picked the broken stub")
	}
	if ver == "unknown" {
		t.Fatalf("Find returned a binary that can't report a version: %q", path)
	}
}

func TestFindDiscovery(t *testing.T) {
	// environment-dependent: either a browser is found (path must exist)
	// or the error names how to fix it. Both are valid outcomes.
	path, ver, err := Find("")
	if err != nil {
		if ver != "" {
			t.Fatalf("error with a version? %q", ver)
		}
		return
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Fatalf("Find returned missing binary %q", path)
	}
}

func TestLaunchBadBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake is POSIX-only")
	}
	stub := filepath.Join(t.TempDir(), "badchrome")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil { // #nosec G306 -- fixture must exec
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, stop, err := Launch(ctx, stub)
	if err == nil {
		stop()
		t.Fatal("expected launch failure")
	}
	if !strings.Contains(err.Error(), "launch") {
		t.Errorf("error shape: %v", err)
	}
}

// Managed-cache hit: $HOME/.cache/wvf/chrome-headless-shell holding a
// working binary must win — Find returns it without touching
// candidates or PATH.
func TestFindManagedCacheHit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake is POSIX-only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".cache", "wvf", headlessShell)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, headlessShell)
	err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'HeadlessShell 999.0 test'\n"), 0o755) // #nosec G306 -- fixture must exec
	if err != nil {
		t.Fatal(err)
	}
	path, ver, err := Find("")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if path != fake {
		t.Fatalf("want managed %s, got %s", fake, path)
	}
	if !strings.Contains(ver, "HeadlessShell") {
		t.Fatalf("version: %q", ver)
	}
}

// A working candidate at the front of the list wins — covers the
// probeVersion-success return inside the candidates loop.
func TestFindCandidateHit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake is POSIX-only")
	}
	t.Setenv("HOME", t.TempDir()) // no managed cache hit
	fake := filepath.Join(t.TempDir(), "fakechrome")
	err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'Chromium 999.0 fake'\n"), 0o755) // #nosec G306 -- fixture must exec
	if err != nil {
		t.Fatal(err)
	}
	old := candidates
	candidates = append([]string{fake}, candidates...)
	defer func() { candidates = old }()
	path, ver, err := Find("")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if path != fake || !strings.Contains(ver, "Chromium 999") {
		t.Fatalf("want fake candidate, got %s %q", path, ver)
	}
}

// PATH hit: with no explicit path, no managed cache, and no existing
// candidates, LookPath(pathNames) must find a working binary on PATH.
func TestFindPathHit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake is POSIX-only")
	}
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	fake := filepath.Join(bin, "chromium")
	err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'Chromium 998.0 path'\n"), 0o755) // #nosec G306 -- fixture must exec
	if err != nil {
		t.Fatal(err)
	}
	old := candidates
	candidates = []string{filepath.Join(t.TempDir(), "nonexistent")}
	defer func() { candidates = old }()
	t.Setenv("PATH", bin)
	path, ver, err := Find("")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if path != fake || !strings.Contains(ver, "Chromium 998") {
		t.Fatalf("want PATH hit %s, got %s %q", fake, path, ver)
	}
}
