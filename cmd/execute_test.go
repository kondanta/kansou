package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kondanta/kansou/internal/config"
)

// helperEnv makes the test binary act as the kansou CLI: TestExecuteHelper
// sets os.Args from KANSOU_TEST_ARGS and calls Execute, which exits the
// process like the real binary does.
const helperEnv = "KANSOU_TEST_ARGS"

// TestExecuteHelper is not a test: it is the child-process entry point.
func TestExecuteHelper(t *testing.T) {
	args, ok := os.LookupEnv(helperEnv)
	if !ok {
		t.Skip("child-process helper")
	}
	os.Args = append([]string{"kansou"}, strings.Fields(args)...)
	Execute()
}

// kansouCmd prepares a child process running Execute with args. HOME and
// XDG_CONFIG_HOME point into a temp dir so the real user config is never read.
func kansouCmd(t *testing.T, args string, env ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	c := exec.Command(os.Args[0], "-test.run=^TestExecuteHelper$")
	c.Env = append(os.Environ(),
		helperEnv+"="+args,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "xdg"),
		"KANSOU_DB_TYPE=",
		"NO_COLOR=1",
	)
	c.Env = append(c.Env, env...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	return c, &stdout, &stderr
}

// runKansou runs the CLI to completion and returns stdout, stderr, exit code.
func runKansou(t *testing.T, args string, env ...string) (string, string, int) {
	t.Helper()
	c, stdout, stderr := kansouCmd(t, args, env...)
	err := c.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running child: %v", err)
	}
	return stdout.String(), stderr.String(), code
}

// writeConfig writes a valid config file (with the given deprecated
// [server] port, 0 for none) and returns its path.
func writeConfig(t *testing.T, serverPort int) string {
	t.Helper()
	cfg := seedTestConfig()
	cfg.Server.Port = serverPort
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExecute(t *testing.T) {
	good := writeConfig(t, 0)
	badPath := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(badPath, []byte("this is = = not toml"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		args       string
		env        []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"version flag", "--version", nil, 0, "kansou dev", ""},
		{"no args prints help", "", nil, 0, "Usage:", ""},
		{"config show reads --config", "--config " + good + " config show", nil, 0, "story", ""},
		{
			"unknown command fails",
			"--config " + good + " nope",
			nil,
			1,
			"",
			"error: unknown command",
		},
		{"invalid config fails", "--config " + badPath + " config show", nil, 1, "", "error:"},
		{
			"unknown db type fails", "--config " + good + " config show",
			[]string{"KANSOU_DB_TYPE=mongo"},
			1, "", "error:",
		},
		{"db command requires a database", "--config " + good + " db prune", nil, 1, "", "error:"},
		{"history requires a database", "--config " + good + " history", nil, 1, "", "error:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := runKansou(t, tt.args, tt.env...)
			if code != tt.wantCode {
				t.Fatalf(
					"exit code = %d, want %d\nstdout: %s\nstderr: %s",
					code,
					tt.wantCode,
					stdout,
					stderr,
				)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout missing %q:\n%s", tt.wantStdout, stdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
		})
	}
}

// TestExecute_SQLiteStoreIsOpenedAndClosed runs a real command against a
// SQLite database selected through the environment.
func TestExecute_SQLiteStore(t *testing.T) {
	good := writeConfig(t, 0)
	db := filepath.Join(t.TempDir(), "k.db")
	env := []string{"KANSOU_DB_TYPE=sqlite", "KANSOU_DB_PATH=" + db}

	stdout, stderr, code := runKansou(t, "--config "+good+" config dimension list", env...)
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "story") {
		t.Errorf("stdout = %q", stdout)
	}
	if _, err := os.Stat(db); err != nil {
		t.Errorf("database file was not created: %v", err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func TestServe_LiveConfigRejectsUnwritableLocation(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "missing-dir", "config.toml")
	_, stderr, code := runKansou(
		t,
		"--config "+cfg+" serve --live-config --port "+fmt.Sprint(freePort(t)),
	)
	if code != 1 || !strings.Contains(stderr, "--live-config") {
		t.Fatalf("exit %d, stderr %q; want exit 1 mentioning --live-config", code, stderr)
	}
}

func TestServe_PortInUseExitsNonZero(t *testing.T) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port

	_, stderr, code := runKansou(
		t,
		fmt.Sprintf("--config %s serve --port %d", writeConfig(t, 0), port),
	)
	if code != 1 || !strings.Contains(stderr, "server error") {
		t.Fatalf("exit %d, stderr %q; want exit 1 with a server error", code, stderr)
	}
}

// TestServe_StartsServesAndShutsDownOnSIGTERM runs the real serve command,
// checks it answers /health, and that SIGTERM shuts it down cleanly (exit 0).
// The deprecated [server] block must produce a warning, not an error.
func TestServe_StartsServesAndShutsDownOnSIGTERM(t *testing.T) {
	port := freePort(t)
	cfg := writeConfig(t, 1234)
	c, _, stderr := kansouCmd(t, fmt.Sprintf("--config %s serve --port %d", cfg, port),
		"KANSOU_CORS_ORIGINS=https://example.test")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	t.Cleanup(func() { _ = c.Process.Kill() })

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	deadline := time.Now().Add(15 * time.Second)
	var resp *http.Response
	for {
		var err error
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err = http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			break
		}
		select {
		case werr := <-done:
			t.Fatalf("server exited early: %v\nstderr: %s", werr, stderr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became healthy\nstderr: %s", stderr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health = %d", resp.StatusCode)
	}

	if err := c.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean exit after SIGTERM, got %v\nstderr: %s", err, stderr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down after SIGTERM")
	}
	if !strings.Contains(stderr.String(), "[server] in config.toml is deprecated") {
		t.Errorf("missing deprecation warning, stderr: %s", stderr)
	}
}
