package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/store"
)

// TestServeHelperProcess is the re-exec target for the
// replaceStaleServe tests — not a standalone test. With
// WVF_FAKE_LISTENER set it binds the addr, prints READY, and blocks
// until killed (as a stale `wvf serve` would be).
func TestServeHelperProcess(_ *testing.T) {
	addr := os.Getenv("WVF_FAKE_LISTENER")
	if addr == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		os.Exit(2)
	}
	defer func() { _ = ln.Close() }()
	fmt.Println("READY")
	select {} // killed by replaceStaleServe
}

// fakeWvfServe re-execs the test binary as a listener whose `ps
// -o command=` output literally begins "wvf serve" — argv[0] carries
// the space so the substring match in replaceStaleServe triggers,
// while -test.run keeps flag parsing intact.
func fakeWvfServe(t *testing.T, addr string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe) // #nosec G204 -- re-execs the test binary itself
	cmd.Args = []string{"wvf serve", "-test.run=^TestServeHelperProcess$"}
	cmd.Env = append(os.Environ(), "WVF_FAKE_LISTENER="+addr)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	go func() {
		if s := bufio.NewScanner(stdout); s.Scan() {
			ready <- s.Text()
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("fake wvf serve did not report READY")
	}
	return cmd
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

func haveProcTools() bool {
	return firstBin("/usr/sbin/lsof", "/usr/bin/lsof", "/bin/lsof") != "" &&
		firstBin("/bin/ps", "/usr/bin/ps") != ""
}

// The whole kill path for real: lsof finds the pid, ps reports
// "wvf serve", FindProcess+Kill frees the port.
func TestReplaceStaleServeKillsWVF(t *testing.T) {
	if !haveProcTools() {
		t.Skip("lsof/ps unavailable — cannot probe listeners")
	}
	addr := freeAddr(t)
	cmd := fakeWvfServe(t, addr)
	defer func() { _ = cmd.Process.Kill() }()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("fake serve not listening on %s: %v", addr, err)
	}
	_ = conn.Close()

	replaceStaleServe(addr)

	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case werr := <-wait:
		_ = werr // "signal: killed" — expected
	case <-time.After(5 * time.Second):
		t.Fatal("stale wvf serve was not killed")
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port still bound after stale kill: %v", err)
	}
	_ = ln.Close()
}

// The safety branch: a foreign listener on the port must survive —
// replaceStaleServe only kills processes whose command is `wvf serve`.
func TestReplaceStaleServeLeavesForeignListener(t *testing.T) {
	if !haveProcTools() {
		t.Skip("lsof/ps unavailable — cannot probe listeners")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	replaceStaleServe(ln.Addr().String())

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("foreign listener was killed — must be left alone")
	}
	_ = conn.Close()
}

func TestReplaceStaleServeEarlyReturns(t *testing.T) {
	replaceStaleServe("not-an-addr") // SplitHostPort error
	replaceStaleServe("127.0.0.1:")  // empty port
	if haveProcTools() {
		replaceStaleServe(freeAddr(t)) // free port → empty lsof output
	}
}

func TestFirstBin(t *testing.T) {
	if got := firstBin("/no/such/a", "/no/such/b"); got != "" {
		t.Fatalf("firstBin on missing paths = %q", got)
	}
	if got := firstBin("/no/such/a", "/bin/sh"); got != "/bin/sh" {
		t.Fatalf("firstBin picked %q, want /bin/sh", got)
	}
}

// A port held by a non-wvf listener: replaceStaleServe skips it and
// ListenAndServe surfaces the bind error through RunE.
func TestServeBindError(t *testing.T) {
	dir := chdirTemp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	yml := dir + "/wvf.yml"
	content := fmt.Sprintf("listen: %q\nstate_db: %q\n",
		ln.Addr().String(), dir+"/state.db")
	if err := os.WriteFile(yml, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := serveCmd()
	cmd.SetArgs([]string{"--config", yml})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want bind error for occupied port")
	}
}

// Full lifecycle: stale running job is marked failed at boot (with the
// notice), the server binds and serves, SIGINT shuts it down cleanly.
func TestServeLifecycle(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()

	st, err := store.Open(ctx, dir+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.GetOrCreateSite(ctx, "flow.test", "http://flow.test", 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertJob(ctx, "scan", site.ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.JobState(ctx, id, "running", ""); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	addr := freeAddr(t)
	yml := dir + "/wvf.yml"
	content := fmt.Sprintf("listen: %q\nstate_db: %q\n", addr, dir+"/state.db")
	if err := os.WriteFile(yml, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut := os.Stdout
	os.Stdout = outW
	defer func() { os.Stdout = oldOut }()

	cmd := serveCmd()
	cmd.SetArgs([]string{"--config", yml})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()

	// wait for the listener
	deadline := time.Now().Add(8 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		select {
		case err := <-done:
			t.Fatalf("serve exited early: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("serve did not start listening")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// NotifyContext intercepts SIGINT — serve should return nil.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve RunE: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGINT did not stop serve")
	}

	_ = outW.Close()
	out, err := io.ReadAll(outR)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "stale jobs failed") {
		t.Fatalf("missing stale-job notice in output: %q", out)
	}
	if !strings.Contains(string(out), "wvf listening") {
		t.Fatalf("missing listen banner in output: %q", out)
	}
}
