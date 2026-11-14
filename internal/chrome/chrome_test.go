package chrome

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
