package agent

import (
	"testing"
)

func TestAgentStruct(t *testing.T) {
	// Verify Agent struct fields can be constructed.
	a := Agent{
		ID:          "test-agent",
		TmuxSession: "minuano",
		TmuxWindow:  "test-agent",
		Status:      "idle",
	}

	if a.ID != "test-agent" {
		t.Errorf("expected ID 'test-agent', got %q", a.ID)
	}
	if a.Status != "idle" {
		t.Errorf("expected status 'idle', got %q", a.Status)
	}
	if a.TaskID != nil {
		t.Error("expected nil TaskID for new agent")
	}
}

func TestAgentPackageExports(t *testing.T) {
	// Verify that the package exports the expected functions.
	// These are compile-time checks — if they compile, the functions exist.
	var _ func(*interface{}, string, string, string, map[string]string) (*Agent, error)
	_ = Spawn  // has correct signature at call sites
	_ = Kill   // func(*pgxpool.Pool, string, string) error
	_ = KillAll
	_ = Heartbeat
	_ = List

	t.Log("all expected functions are exported from the agent package")
}
