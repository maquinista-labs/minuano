package tmux

import (
	"os"
	"testing"
)

func TestInsideTmux(t *testing.T) {
	// Save and restore.
	orig := os.Getenv("TMUX")
	defer os.Setenv("TMUX", orig)

	os.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
	if !InsideTmux() {
		t.Error("InsideTmux() should return true when TMUX env is set")
	}

	os.Unsetenv("TMUX")
	if InsideTmux() {
		t.Error("InsideTmux() should return false when TMUX env is not set")
	}
}

func TestSessionExists_NoServer(t *testing.T) {
	// When no tmux server is running (or a bad session name), SessionExists should return false.
	exists := SessionExists("nonexistent-test-session-xyz")
	if exists {
		t.Error("SessionExists should return false for nonexistent session")
	}
}

func TestWindowExists_NoServer(t *testing.T) {
	exists := WindowExists("nonexistent-test-session-xyz", "nonexistent-window")
	if exists {
		t.Error("WindowExists should return false for nonexistent session/window")
	}
}
