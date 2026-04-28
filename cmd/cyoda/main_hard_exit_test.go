//go:build !windows

package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestShutdown_SecondSignal_ForcesHardExit pins the second-signal escape
// hatch (#10 follow-up): the first SIGINT/SIGTERM cancels rootCtx and
// triggers graceful drain, but if the operator presses Ctrl+C again
// because the drain is hanging, the process must hard-exit with code 2.
//
// This is the safety valve for stuck in-flight RPCs or slow-closing
// storage pools. signal.NotifyContext on its own only cancels on the
// first signal — every subsequent signal is a no-op, leaving the
// operator with no recourse short of SIGKILL.
//
// The test starts the cyoda binary, waits for it to bind, sends two
// SIGTERMs, and asserts (a) exit code 2 and (b) the
// "hard exit forced by second signal" warn log line in stderr.
func TestShutdown_SecondSignal_ForcesHardExit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess shutdown test in -short mode")
	}

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "cyoda-test")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build cyoda: %v", err)
	}

	httpPort := freePortIO(t)
	grpcPort := freePortIO(t)
	adminPort := freePortIO(t)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"CYODA_HTTP_PORT="+strconv.Itoa(httpPort),
		"CYODA_GRPC_PORT="+strconv.Itoa(grpcPort),
		"CYODA_ADMIN_PORT="+strconv.Itoa(adminPort),
		"CYODA_ADMIN_BIND_ADDRESS=127.0.0.1",
		"CYODA_SUPPRESS_BANNER=true",
		"CYODA_LOG_LEVEL=info",
		"CYODA_OTEL_ENABLED=false",
		"CYODA_IAM_MODE=mock",
	)
	// Isolate the child in its own process group so signals stay scoped.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Logging goes to stdout (see internal/logging.Init); capture both
	// streams so the warn-log assertion below can find the line.
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
		}
	}()

	// Wait until admin listener responds.
	deadline := time.Now().Add(15 * time.Second)
	adminAddr := "127.0.0.1:" + strconv.Itoa(adminPort)
	for {
		c, err := net.DialTimeout("tcp", adminAddr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child did not start admin listener: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Pin an in-flight HTTP request on the admin server so the graceful
	// drain has something to wait on. Without this, an idle child exits
	// in ~1ms — far faster than we can deliver a second signal — and the
	// hard-exit branch is never exercised. The request line is sent but
	// the headers are never terminated, so http.Server.Shutdown counts
	// the connection as active and blocks for the full drain budget.
	httpAddr := "127.0.0.1:" + strconv.Itoa(httpPort)
	hold, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatalf("hold-open dial: %v", err)
	}
	defer hold.Close()
	if _, err := hold.Write([]byte("GET /livez HTTP/1.1\r\nHost: x\r\n")); err != nil {
		t.Fatalf("hold-open write: %v", err)
	}

	// First SIGTERM: cancels rootCtx and starts the graceful drain.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send first SIGTERM: %v", err)
	}
	// Brief pause so the first-signal path is observably engaged before
	// the second arrives — this exercises the second-signal handler
	// rather than a coalesced double-fire.
	time.Sleep(200 * time.Millisecond)

	// Second SIGTERM: must force os.Exit(2).
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send second SIGTERM: %v", err)
	}

	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()

	select {
	case err := <-exitCh:
		if err == nil {
			t.Fatalf("child exited 0; expected non-zero exit code 2")
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("child exited with non-ExitError %T: %v", err, err)
		}
		ws, ok := exitErr.Sys().(syscall.WaitStatus)
		if !ok {
			t.Fatalf("WaitStatus unavailable on this platform: %T", exitErr.Sys())
		}
		if ws.ExitStatus() != 2 {
			t.Errorf("exit code %d; want 2 (forced by second signal)", ws.ExitStatus())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Signal(syscall.SIGKILL)
		t.Fatal("child did not exit within 20s of second SIGTERM — hard-exit not wired")
	}

	if got := combined.String(); !strings.Contains(got, "hard exit forced by second signal") {
		t.Errorf("expected 'hard exit forced by second signal' warn log; got:\n%s", got)
	}
}
