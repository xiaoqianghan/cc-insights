package proxy

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StartWithIdleTimeout starts the HTTP server and arms the idle-shutdown timer.
// It blocks until the server shuts down.
func (s *Server) StartWithIdleTimeout() error {
	s.mu.Lock()
	s.idleTimer = time.AfterFunc(s.cfg.Proxy.IdleTimeout.Duration, func() {
		log.Println("idle timeout reached, shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Shutdown(ctx)
		RemovePIDFile()
	})
	s.mu.Unlock()

	return s.Start()
}

// resetIdleTimer resets the idle-shutdown timer back to the configured duration.
func (s *Server) resetIdleTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idleTimer != nil {
		s.idleTimer.Reset(s.cfg.Proxy.IdleTimeout.Duration)
	}
}

// PIDFilePath returns the path to the PID file (~/.claude/cc-insights/cci.pid).
func PIDFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".claude", "cc-insights", "cci.pid")
}

// WritePIDFile writes the current process PID and executable path to the PID file.
// Format: "PID\nexecutable_path"
func WritePIDFile() error {
	path := PIDFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create pid dir: %w", err)
	}
	exe, _ := os.Executable()
	content := fmt.Sprintf("%d\n%s", os.Getpid(), exe)
	return os.WriteFile(path, []byte(content), 0644)
}

// RemovePIDFile deletes the PID file.
func RemovePIDFile() {
	os.Remove(PIDFilePath())
}

// IsRunning checks whether a cci process is already running by reading the
// PID file, verifying the executable identity, and sending signal 0.
// If the PID file refers to a stale/foreign process, it is removed.
// Returns (alive, pid).
func IsRunning() (bool, int) {
	pidPath := PIDFilePath()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false, 0
	}

	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		os.Remove(pidPath)
		return false, 0
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidPath)
		return false, pid
	}

	// Signal 0 checks existence without actually signaling.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		os.Remove(pidPath)
		return false, pid
	}

	// Verify the process is actually cci. Compare by basename rather than
	// full path: the daemon may have been launched from a different install
	// location (e.g. /usr/local/bin/cci vs ~/.local/bin/cci) and both are
	// legitimate cci processes.
	if len(lines) > 1 {
		storedExe := strings.TrimSpace(lines[1])
		if storedExe != "" && filepath.Base(storedExe) != "cci" {
			// PID was reused by a different program — stale pidfile.
			os.Remove(pidPath)
			return false, pid
		}
	}

	return true, pid
}

// Daemonize re-executes the current binary in the background with a
// --daemonized flag. The parent waits briefly to confirm the child is
// listening, then exits.
func Daemonize() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Strip -d/--daemon from child args to prevent recursive daemonization.
	var filtered []string
	for _, a := range os.Args[1:] {
		if a == "-d" || a == "--daemon" {
			continue
		}
		filtered = append(filtered, a)
	}
	args := append(filtered, "--daemonized")
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Detach from parent process group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// Wait briefly for the child to start listening.
	ok := waitForPort(os.Args, 500*time.Millisecond)
	if !ok {
		log.Println("warning: could not confirm daemon is listening")
	}

	return nil
}

// waitForPort extracts the listen address from args/defaults and polls it.
func waitForPort(args []string, timeout time.Duration) bool {
	// Best-effort: try connecting to the listen address.
	// Default listen address matches config default if not parseable from args.
	addr := "127.0.0.1:4318"
	for i, a := range args {
		if (a == "--listen" || a == "-l") && i+1 < len(args) {
			addr = args[i+1]
			break
		}
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
