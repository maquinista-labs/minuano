package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// SessionExists checks if a tmux session exists.
func SessionExists(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

// EnsureSession creates a tmux session if it doesn't exist.
func EnsureSession(name string) error {
	if SessionExists(name) {
		return nil
	}
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating session %s: %s: %w", name, string(out), err)
	}
	return nil
}

// WindowExists checks if a window exists in the given session.
func WindowExists(session, window string) bool {
	target := session + ":" + window
	return exec.Command("tmux", "select-window", "-t", target).Run() == nil
}

// NewWindow creates a new window in the given session with environment variables.
func NewWindow(session, window string, env map[string]string) error {
	args := []string{"new-window", "-t", session, "-n", window}
	cmd := exec.Command("tmux", args...)

	// Build environment: inherit current env + add overrides.
	cmdEnv := os.Environ()
	for k, v := range env {
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	cmd.Env = cmdEnv

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating window %s:%s: %s: %w", session, window, string(out), err)
	}

	// Set environment variables inside the tmux window.
	for k, v := range env {
		SendKeys(session, window, fmt.Sprintf("export %s=%q", k, v))
	}

	return nil
}

// SendKeys sends keystrokes to a tmux window.
func SendKeys(session, window, keys string) error {
	target := session + ":" + window
	cmd := exec.Command("tmux", "send-keys", "-t", target, keys, "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("send-keys to %s: %s: %w", target, string(out), err)
	}
	return nil
}

// CapturePane captures the last N lines from a tmux pane.
func CapturePane(session, window string, lines int) (string, error) {
	target := session + ":" + window
	start := fmt.Sprintf("-%d", lines)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", start)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("capturing pane %s: %w", target, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// KillWindow kills a tmux window.
func KillWindow(session, window string) error {
	target := session + ":" + window
	cmd := exec.Command("tmux", "kill-window", "-t", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("killing window %s: %s: %w", target, string(out), err)
	}
	return nil
}

// AttachSession attaches to a tmux session, optionally selecting a window.
// This replaces the current process.
func AttachSession(session, window string) error {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	args := []string{"tmux", "attach-session", "-t", session}
	if window != "" {
		// Select the window first then attach.
		target := session + ":" + window
		exec.Command("tmux", "select-window", "-t", target).Run()
	}

	return syscall.Exec(tmuxBin, args, os.Environ())
}

// SwitchWindow switches to a window when already inside tmux.
func SwitchWindow(session, window string) error {
	target := session + ":" + window
	cmd := exec.Command("tmux", "select-window", "-t", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("switching to window %s: %s: %w", target, string(out), err)
	}
	return nil
}

// InsideTmux returns true if the current process is running inside tmux.
func InsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// AttachOrSwitch attaches to a session/window or switches if already in tmux.
func AttachOrSwitch(session, window string) error {
	if InsideTmux() {
		return SwitchWindow(session, window)
	}
	return AttachSession(session, window)
}
