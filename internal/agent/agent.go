package agent

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/otavio/minuano/internal/db"
	"github.com/otavio/minuano/internal/tmux"
)

// Agent represents a running agent instance.
type Agent struct {
	ID          string
	TmuxSession string
	TmuxWindow  string
	TaskID      *string
	Status      string
	StartedAt   time.Time
	LastSeen    *time.Time
}

// Spawn registers an agent in the DB, creates a tmux window, and sends the bootstrap command.
// It returns immediately without waiting for the agent to claim a task.
func Spawn(pool *pgxpool.Pool, tmuxSession, agentID, claudeMDPath string, env map[string]string) (*Agent, error) {
	// Register in DB.
	if err := db.RegisterAgent(pool, agentID, tmuxSession, agentID); err != nil {
		return nil, fmt.Errorf("registering agent: %w", err)
	}

	// Create tmux window.
	if err := tmux.NewWindow(tmuxSession, agentID, env); err != nil {
		// Clean up DB on failure.
		db.DeleteAgent(pool, agentID)
		return nil, fmt.Errorf("creating tmux window: %w", err)
	}

	// Resolve scripts directory path (relative to CLAUDE.md).
	scriptsDir := filepath.Join(filepath.Dir(claudeMDPath), "..", "scripts")
	absScripts, _ := filepath.Abs(scriptsDir)

	// Send bootstrap commands.
	bootstrap := []string{
		fmt.Sprintf("export AGENT_ID=%q", agentID),
		fmt.Sprintf("export DATABASE_URL=%q", env["DATABASE_URL"]),
		fmt.Sprintf("export PATH=\"$PATH:%s\"", absScripts),
		fmt.Sprintf("claude --dangerously-skip-permissions -p \"$(cat %s)\"", claudeMDPath),
	}

	for _, cmd := range bootstrap {
		if err := tmux.SendKeys(tmuxSession, agentID, cmd); err != nil {
			return nil, fmt.Errorf("sending bootstrap command: %w", err)
		}
	}

	now := time.Now()
	return &Agent{
		ID:          agentID,
		TmuxSession: tmuxSession,
		TmuxWindow:  agentID,
		Status:      "idle",
		StartedAt:   now,
		LastSeen:    &now,
	}, nil
}

// Kill terminates an agent: kills the tmux window, releases claimed tasks, removes from DB.
func Kill(pool *pgxpool.Pool, tmuxSession, agentID string) error {
	// Kill tmux window (ignore error if already gone).
	tmux.KillWindow(tmuxSession, agentID)

	// Delete from DB (also releases claimed tasks).
	if err := db.DeleteAgent(pool, agentID); err != nil {
		return fmt.Errorf("deleting agent from DB: %w", err)
	}

	return nil
}

// KillAll terminates all registered agents.
func KillAll(pool *pgxpool.Pool, tmuxSession string) error {
	agents, err := db.ListAgents(pool)
	if err != nil {
		return fmt.Errorf("listing agents: %w", err)
	}

	for _, a := range agents {
		if err := Kill(pool, tmuxSession, a.ID); err != nil {
			// Log but continue killing others.
			fmt.Printf("warning: failed to kill agent %s: %v\n", a.ID, err)
		}
	}
	return nil
}

// Heartbeat updates an agent's last_seen and status.
func Heartbeat(pool *pgxpool.Pool, agentID, status string) error {
	return db.UpdateAgentStatus(pool, agentID, status)
}

// List returns all registered agents with their task assignments.
func List(pool *pgxpool.Pool) ([]*db.Agent, error) {
	return db.ListAgents(pool)
}
