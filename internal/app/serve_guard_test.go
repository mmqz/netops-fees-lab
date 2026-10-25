package app

// serve_guard_test.go — the two issue #9 startup refusals: the unit tables
// (validateServeGuards + loopbackOnly) and the real-process exit-code
// proof (the naked external bind dies before the listener opens; the
// loopback default still boots).

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// waitBoot gives a serve process a short window to prove it did NOT exit
// (the listening line lands on stderr under slog); firing = still alive.
func waitBoot() <-chan time.Time { return time.After(1500 * time.Millisecond) }

// TestLoopbackOnly pins the default-safety predicate: every-interface
// binds and foreign addresses are NOT loopback-only; literal loopback IPs
// and "localhost" are; a malformed/bare host is treated conservatively.
func TestLoopbackOnly(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"localhost:8080", true},
		{"[::1]:8080", true},
		{":8080", false},
		{"0.0.0.0:8080", false},
		{"[::]:8080", false},
		{"192.168.1.10:8080", false},
		{"example.com:8080", false},
	}
	for _, c := range cases {
		if got := loopbackOnly(c.addr); got != c.want {
			t.Errorf("loopbackOnly(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// TestValidateServeGuards pins the two refusals and the three passes:
// naked external bind without tokens → refuse; identical tokens → refuse;
// loopback bind never needs a token; either single tier or both distinct
// tiers pass everywhere.
func TestValidateServeGuards(t *testing.T) {
	for _, addr := range []string{":8080", "0.0.0.0:8080", "[::]:8080"} {
		err := validateServeGuards(addr, "", "")
		if err == nil {
			t.Errorf("validateServeGuards(%q, no tokens) = nil, want the naked-bind refusal", addr)
			continue
		}
		if !strings.Contains(err.Error(), "拒绝启动") || !strings.Contains(err.Error(), "非回环") {
			t.Errorf("validateServeGuards(%q) message = %q, want the refusal wording", addr, err.Error())
		}
	}
	// Either tier alone satisfies the guard…
	if err := validateServeGuards("0.0.0.0:8080", "decl-tok", ""); err != nil {
		t.Errorf("external bind with declarer token refused: %v", err)
	}
	if err := validateServeGuards("0.0.0.0:8080", "", "adm-tok"); err != nil {
		t.Errorf("external bind with admin token refused: %v", err)
	}
	// …and a loopback bind never needs one.
	if err := validateServeGuards("127.0.0.1:8080", "", ""); err != nil {
		t.Errorf("loopback bind without tokens refused: %v", err)
	}

	// Identical tokens are refused regardless of the bind.
	err := validateServeGuards("127.0.0.1:8080", "same-tok", "same-tok")
	if err == nil || !strings.Contains(err.Error(), "不能相同") {
		t.Errorf("identical tokens: err = %v, want the distinct-tokens refusal", err)
	}
	// Distinct tokens pass everywhere.
	if err := validateServeGuards("0.0.0.0:8080", "decl-tok", "adm-tok"); err != nil {
		t.Errorf("external bind with both tiers refused: %v", err)
	}
}

// TestCLIServeDefaultSafeGuards proves the refusals at the REAL process
// boundary (exit code 2, usage_error envelope): the naked external bind
// dies before the listener ever opens, while the loopback default still
// boots (and is then killed).
func TestCLIServeDefaultSafeGuards(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	dir := testsupport.TempDir(t)
	db := dir + "/netops.db"

	// Naked all-interfaces bind → exit 2 with the refusal.
	out, err := exec.Command(e2eBin, "serve", "--addr", "0.0.0.0:8080", "--db", db).CombinedOutput()
	if err == nil {
		t.Fatalf("serve --addr 0.0.0.0:8080 without tokens started (it must refuse):\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("serve naked external bind: exit = %v, want 2 (usage error):\n%s", err, out)
	}
	if !strings.Contains(string(out), "拒绝启动") || !strings.Contains(string(out), "非回环") {
		t.Errorf("refusal message incomplete (want 拒绝启动 + 非回环):\n%s", out)
	}

	// Identical tokens → exit 2 with the distinct-tokens refusal.
	out, err = exec.Command(e2eBin, "serve", "--addr", "127.0.0.1:8080", "--db", db,
		"--auth-token", "dupe", "--admin-token", "dupe").CombinedOutput()
	if err == nil {
		t.Fatalf("serve with identical tokens started (it must refuse):\n%s", out)
	}
	if !strings.Contains(string(out), "不能相同") {
		t.Errorf("distinct-tokens message missing:\n%s", out)
	}

	// The loopback default still boots: start, wait for the listening
	// line, then kill (the guard did NOT fire).
	proc := exec.Command(e2eBin, "serve", "--db", db)
	if err := proc.Start(); err != nil {
		t.Fatalf("start loopback serve: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("loopback serve exited immediately (guard must not fire): %v", err)
	case <-waitBoot():
	}
	if err := proc.Process.Kill(); err != nil {
		t.Fatalf("kill loopback serve: %v", err)
	}
	<-done
}
