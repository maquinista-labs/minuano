package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Task represents a work unit.
type Task struct {
	ID          string
	Title       string
	Body        string
	Status      string
	Priority    int
	Capability  *string
	ClaimedBy   *string
	ClaimedAt   *time.Time
	DoneAt      *time.Time
	CreatedAt   time.Time
	Attempt     int
	MaxAttempts int
	ProjectID   *string
	Metadata    json.RawMessage
}

// TaskContext represents a persistent context entry for a task.
type TaskContext struct {
	ID         int64
	TaskID     string
	AgentID    *string
	Kind       string
	Content    string
	SourceTask *string
	CreatedAt  time.Time
}

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

// TreeNode is a task with its children for tree rendering.
type TreeNode struct {
	Task     *Task
	Children []*TreeNode
}

// CreateTask inserts a new task.
func CreateTask(pool *pgxpool.Pool, id, title, body string, priority int, capability, projectID *string, metadata json.RawMessage) error {
	_, err := pool.Exec(context.Background(), `
		INSERT INTO tasks (id, title, body, priority, capability, project_id, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, title, body, priority, capability, projectID, metadata)
	if err != nil {
		return fmt.Errorf("creating task: %w", err)
	}
	return nil
}

// SetTaskStatus sets a task's status directly.
func SetTaskStatus(pool *pgxpool.Pool, id, status string) error {
	_, err := pool.Exec(context.Background(), `
		UPDATE tasks SET status = $2 WHERE id = $1
	`, id, status)
	if err != nil {
		return fmt.Errorf("setting task status: %w", err)
	}
	return nil
}

// AddDependency creates a dependency edge.
func AddDependency(pool *pgxpool.Pool, taskID, dependsOn string) error {
	_, err := pool.Exec(context.Background(), `
		INSERT INTO task_deps (task_id, depends_on) VALUES ($1, $2)
	`, taskID, dependsOn)
	if err != nil {
		return fmt.Errorf("adding dependency: %w", err)
	}
	return nil
}

// ResolvePartialID finds a single task ID matching the given prefix.
// Returns an error if zero or multiple tasks match.
func ResolvePartialID(pool *pgxpool.Pool, prefix string) (string, error) {
	rows, err := pool.Query(context.Background(), `
		SELECT id FROM tasks WHERE id LIKE $1 || '%' LIMIT 2
	`, prefix)
	if err != nil {
		return "", fmt.Errorf("resolving partial id: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("scanning id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterating ids: %w", err)
	}

	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no task found matching %q", prefix)
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("ambiguous prefix %q matches multiple tasks", prefix)
	}
}

// GetTask retrieves a task by exact or partial ID.
func GetTask(pool *pgxpool.Pool, id string) (*Task, error) {
	resolvedID, err := ResolvePartialID(pool, id)
	if err != nil {
		return nil, err
	}

	var t Task
	err = pool.QueryRow(context.Background(), `
		SELECT id, title, body, status, priority, capability, claimed_by, claimed_at,
		       done_at, created_at, attempt, max_attempts, project_id, metadata
		FROM tasks WHERE id = $1
	`, resolvedID).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Priority, &t.Capability,
		&t.ClaimedBy, &t.ClaimedAt, &t.DoneAt, &t.CreatedAt, &t.Attempt,
		&t.MaxAttempts, &t.ProjectID, &t.Metadata,
	)
	if err != nil {
		return nil, fmt.Errorf("getting task %s: %w", resolvedID, err)
	}
	return &t, nil
}

// ListTasks returns all tasks, optionally filtered by project.
func ListTasks(pool *pgxpool.Pool, projectID *string) ([]*Task, error) {
	var rows pgx.Rows
	var err error

	if projectID != nil {
		rows, err = pool.Query(context.Background(), `
			SELECT id, title, body, status, priority, capability, claimed_by, claimed_at,
			       done_at, created_at, attempt, max_attempts, project_id, metadata
			FROM tasks WHERE project_id = $1
			ORDER BY priority DESC, created_at ASC
		`, *projectID)
	} else {
		rows, err = pool.Query(context.Background(), `
			SELECT id, title, body, status, priority, capability, claimed_by, claimed_at,
			       done_at, created_at, attempt, max_attempts, project_id, metadata
			FROM tasks
			ORDER BY priority DESC, created_at ASC
		`)
	}
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	return scanTasks(rows)
}

// GetTaskWithContext retrieves a task and all its context entries.
func GetTaskWithContext(pool *pgxpool.Pool, id string) (*Task, []*TaskContext, error) {
	task, err := GetTask(pool, id)
	if err != nil {
		return nil, nil, err
	}

	rows, err := pool.Query(context.Background(), `
		SELECT id, task_id, agent_id, kind, content, source_task, created_at
		FROM task_context
		WHERE task_id = $1
		ORDER BY created_at ASC
	`, task.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("getting task context: %w", err)
	}
	defer rows.Close()

	var ctxs []*TaskContext
	for rows.Next() {
		var c TaskContext
		if err := rows.Scan(&c.ID, &c.TaskID, &c.AgentID, &c.Kind, &c.Content, &c.SourceTask, &c.CreatedAt); err != nil {
			return nil, nil, fmt.Errorf("scanning context: %w", err)
		}
		ctxs = append(ctxs, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating context: %w", err)
	}

	return task, ctxs, nil
}

// AtomicClaim atomically claims one ready task, injects inherited context, and updates the agent.
// Returns nil if no task is available.
func AtomicClaim(pool *pgxpool.Pool, agentID string, capability *string) (*Task, error) {
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning claim tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Claim one ready task.
	var t Task
	var cap interface{}
	if capability != nil {
		cap = *capability
	}

	err = tx.QueryRow(ctx, `
		UPDATE tasks
		SET    status     = 'claimed',
		       claimed_by = $1,
		       claimed_at = NOW(),
		       attempt    = attempt + 1
		WHERE  id = (
			SELECT id FROM tasks
			WHERE  status = 'ready'
			  AND  (capability IS NULL OR capability = $2)
			  AND  attempt < max_attempts
			ORDER  BY priority DESC, created_at ASC
			LIMIT  1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, title, body, status, priority, capability, claimed_by, claimed_at,
		          done_at, created_at, attempt, max_attempts, project_id, metadata
	`, agentID, cap).Scan(
		&t.ID, &t.Title, &t.Body, &t.Status, &t.Priority, &t.Capability,
		&t.ClaimedBy, &t.ClaimedAt, &t.DoneAt, &t.CreatedAt, &t.Attempt,
		&t.MaxAttempts, &t.ProjectID, &t.Metadata,
	)
	if err == pgx.ErrNoRows {
		return nil, nil // No task available.
	}
	if err != nil {
		return nil, fmt.Errorf("claiming task: %w", err)
	}

	// Inject inherited context from done dependencies.
	_, err = tx.Exec(ctx, `
		INSERT INTO task_context (task_id, agent_id, kind, content, source_task)
		SELECT $1, $2, 'inherited', tc.content, tc.task_id
		FROM   task_deps td
		JOIN   task_context tc ON tc.task_id = td.depends_on
		                      AND tc.kind IN ('result', 'observation', 'handoff', 'test_failure')
		JOIN   tasks dep       ON dep.id = td.depends_on
		                      AND dep.status = 'done'
		WHERE  td.task_id = $1
	`, t.ID, agentID)
	if err != nil {
		return nil, fmt.Errorf("injecting inherited context: %w", err)
	}

	// Update agent status.
	_, err = tx.Exec(ctx, `
		UPDATE agents
		SET    task_id   = $2,
		       status    = 'working',
		       last_seen = NOW()
		WHERE  id = $1
	`, agentID, t.ID)
	if err != nil {
		return nil, fmt.Errorf("updating agent: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing claim: %w", err)
	}

	return &t, nil
}

// MarkDone marks a task as done after tests pass, recording the result summary.
func MarkDone(pool *pgxpool.Pool, taskID, agentID, summary string) error {
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning done tx: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO task_context (task_id, agent_id, kind, content)
		VALUES ($1, $2, 'result', $3)
	`, taskID, agentID, summary)
	if err != nil {
		return fmt.Errorf("recording result: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE tasks
		SET    status     = 'done',
		       done_at    = NOW(),
		       claimed_by = NULL,
		       claimed_at = NULL
		WHERE  id         = $1
		  AND  claimed_by = $2
	`, taskID, agentID)
	if err != nil {
		return fmt.Errorf("marking done: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE agents SET task_id = NULL, status = 'idle', last_seen = NOW()
		WHERE id = $1
	`, agentID)
	if err != nil {
		return fmt.Errorf("releasing agent: %w", err)
	}

	return tx.Commit(ctx)
}

// RecordFailure records a test failure and resets the task to ready, or marks it failed
// if max attempts reached.
func RecordFailure(pool *pgxpool.Pool, taskID, agentID, output string) error {
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning failure tx: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO task_context (task_id, agent_id, kind, content)
		VALUES ($1, $2, 'test_failure', $3)
	`, taskID, agentID, output)
	if err != nil {
		return fmt.Errorf("recording failure: %w", err)
	}

	// Reset to ready if under max attempts.
	_, err = tx.Exec(ctx, `
		UPDATE tasks
		SET    status     = 'ready',
		       claimed_by = NULL,
		       claimed_at = NULL
		WHERE  id         = $1
		  AND  claimed_by = $2
		  AND  attempt    < max_attempts
	`, taskID, agentID)
	if err != nil {
		return fmt.Errorf("resetting task: %w", err)
	}

	// Mark failed if at max attempts.
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status = 'failed'
		WHERE id = $1 AND attempt >= max_attempts
	`, taskID)
	if err != nil {
		return fmt.Errorf("marking failed: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE agents SET task_id = NULL, status = 'idle', last_seen = NOW()
		WHERE id = $1
	`, agentID)
	if err != nil {
		return fmt.Errorf("releasing agent: %w", err)
	}

	return tx.Commit(ctx)
}

// ReclaimStale resets tasks that have been claimed for longer than the given minutes.
// Returns the number of reclaimed tasks.
func ReclaimStale(pool *pgxpool.Pool, minutes int) (int, error) {
	tag, err := pool.Exec(context.Background(), `
		UPDATE tasks
		SET    status     = 'ready',
		       claimed_by = NULL,
		       claimed_at = NULL
		WHERE  status     = 'claimed'
		  AND  claimed_at < NOW() - make_interval(mins => $1)
	`, minutes)
	if err != nil {
		return 0, fmt.Errorf("reclaiming stale tasks: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// AddObservation records an observation for a task.
func AddObservation(pool *pgxpool.Pool, taskID, agentID, content string) error {
	_, err := pool.Exec(context.Background(), `
		INSERT INTO task_context (task_id, agent_id, kind, content)
		VALUES ($1, $2, 'observation', $3)
	`, taskID, agentID, content)
	if err != nil {
		return fmt.Errorf("adding observation: %w", err)
	}
	return nil
}

// AddHandoff records a handoff note for a task.
func AddHandoff(pool *pgxpool.Pool, taskID, agentID, content string) error {
	_, err := pool.Exec(context.Background(), `
		INSERT INTO task_context (task_id, agent_id, kind, content)
		VALUES ($1, $2, 'handoff', $3)
	`, taskID, agentID, content)
	if err != nil {
		return fmt.Errorf("adding handoff: %w", err)
	}
	return nil
}

// GetDependencyTree builds a forest of TreeNodes for tree rendering.
func GetDependencyTree(pool *pgxpool.Pool, projectID *string) ([]*TreeNode, error) {
	tasks, err := ListTasks(pool, projectID)
	if err != nil {
		return nil, err
	}

	// Load all deps.
	rows, err := pool.Query(context.Background(), `SELECT task_id, depends_on FROM task_deps`)
	if err != nil {
		return nil, fmt.Errorf("loading deps: %w", err)
	}
	defer rows.Close()

	// Map of taskID -> list of dependency IDs (parents).
	parents := make(map[string][]string)
	children := make(map[string][]string)
	for rows.Next() {
		var taskID, depID string
		if err := rows.Scan(&taskID, &depID); err != nil {
			return nil, fmt.Errorf("scanning dep: %w", err)
		}
		parents[taskID] = append(parents[taskID], depID)
		children[depID] = append(children[depID], taskID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating deps: %w", err)
	}

	// Build node map.
	nodeMap := make(map[string]*TreeNode)
	for _, t := range tasks {
		nodeMap[t.ID] = &TreeNode{Task: t}
	}

	// Wire children.
	for _, t := range tasks {
		for _, childID := range children[t.ID] {
			if child, ok := nodeMap[childID]; ok {
				nodeMap[t.ID].Children = append(nodeMap[t.ID].Children, child)
			}
		}
	}

	// Roots are tasks with no parents.
	var roots []*TreeNode
	for _, t := range tasks {
		if len(parents[t.ID]) == 0 {
			roots = append(roots, nodeMap[t.ID])
		}
	}

	return roots, nil
}

// SearchContext performs full-text search across task context.
func SearchContext(pool *pgxpool.Pool, query string) ([]*TaskContext, error) {
	rows, err := pool.Query(context.Background(), `
		SELECT tc.id, tc.task_id, tc.agent_id, tc.kind, tc.content, tc.source_task, tc.created_at
		FROM task_context tc
		WHERE to_tsvector('english', tc.content) @@ plainto_tsquery('english', $1)
		ORDER BY ts_rank(to_tsvector('english', tc.content), plainto_tsquery('english', $1)) DESC
	`, query)
	if err != nil {
		return nil, fmt.Errorf("searching context: %w", err)
	}
	defer rows.Close()

	var results []*TaskContext
	for rows.Next() {
		var c TaskContext
		if err := rows.Scan(&c.ID, &c.TaskID, &c.AgentID, &c.Kind, &c.Content, &c.SourceTask, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning search result: %w", err)
		}
		results = append(results, &c)
	}
	return results, rows.Err()
}

// RegisterAgent inserts a new agent record.
func RegisterAgent(pool *pgxpool.Pool, id, tmuxSession, tmuxWindow string) error {
	_, err := pool.Exec(context.Background(), `
		INSERT INTO agents (id, tmux_session, tmux_window, last_seen)
		VALUES ($1, $2, $3, NOW())
	`, id, tmuxSession, tmuxWindow)
	if err != nil {
		return fmt.Errorf("registering agent: %w", err)
	}
	return nil
}

// ListAgents returns all registered agents.
func ListAgents(pool *pgxpool.Pool) ([]*Agent, error) {
	rows, err := pool.Query(context.Background(), `
		SELECT id, tmux_session, tmux_window, task_id, status, started_at, last_seen
		FROM agents
		ORDER BY started_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.TmuxSession, &a.TmuxWindow, &a.TaskID, &a.Status, &a.StartedAt, &a.LastSeen); err != nil {
			return nil, fmt.Errorf("scanning agent: %w", err)
		}
		agents = append(agents, &a)
	}
	return agents, rows.Err()
}

// UpdateAgentStatus updates an agent's status and last_seen.
func UpdateAgentStatus(pool *pgxpool.Pool, id, status string) error {
	_, err := pool.Exec(context.Background(), `
		UPDATE agents SET status = $2, last_seen = NOW() WHERE id = $1
	`, id, status)
	if err != nil {
		return fmt.Errorf("updating agent status: %w", err)
	}
	return nil
}

// DeleteAgent removes an agent and releases any claimed task.
func DeleteAgent(pool *pgxpool.Pool, id string) error {
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning delete tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Release any claimed task.
	_, err = tx.Exec(ctx, `
		UPDATE tasks
		SET    status     = 'ready',
		       claimed_by = NULL,
		       claimed_at = NULL
		WHERE  claimed_by = $1
		  AND  status     = 'claimed'
	`, id)
	if err != nil {
		return fmt.Errorf("releasing agent tasks: %w", err)
	}

	_, err = tx.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting agent: %w", err)
	}

	return tx.Commit(ctx)
}

// UpdateTask updates a task's title and body (for roda edit).
func UpdateTask(pool *pgxpool.Pool, id, title, body string) error {
	_, err := pool.Exec(context.Background(), `
		UPDATE tasks SET title = $2, body = $3 WHERE id = $1
	`, id, title, body)
	if err != nil {
		return fmt.Errorf("updating task: %w", err)
	}
	return nil
}

// HasUnmetDeps returns true if any of the task's dependencies are not yet done.
func HasUnmetDeps(pool *pgxpool.Pool, taskID string) (bool, error) {
	var count int
	err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM task_deps td
		JOIN tasks t ON t.id = td.depends_on
		WHERE td.task_id = $1 AND t.status != 'done'
	`, taskID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking unmet deps: %w", err)
	}
	return count > 0, nil
}

// GetAgentByTaskID finds the agent currently working on a given task.
func GetAgentByTaskID(pool *pgxpool.Pool, taskID string) (*Agent, error) {
	var a Agent
	err := pool.QueryRow(context.Background(), `
		SELECT id, tmux_session, tmux_window, task_id, status, started_at, last_seen
		FROM agents WHERE task_id = $1
	`, taskID).Scan(&a.ID, &a.TmuxSession, &a.TmuxWindow, &a.TaskID, &a.Status, &a.StartedAt, &a.LastSeen)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting agent by task: %w", err)
	}
	return &a, nil
}

// GetAgent retrieves a single agent by ID.
func GetAgent(pool *pgxpool.Pool, id string) (*Agent, error) {
	var a Agent
	err := pool.QueryRow(context.Background(), `
		SELECT id, tmux_session, tmux_window, task_id, status, started_at, last_seen
		FROM agents WHERE id = $1
	`, id).Scan(&a.ID, &a.TmuxSession, &a.TmuxWindow, &a.TaskID, &a.Status, &a.StartedAt, &a.LastSeen)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting agent: %w", err)
	}
	return &a, nil
}

func scanTasks(rows pgx.Rows) ([]*Task, error) {
	var tasks []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Body, &t.Status, &t.Priority, &t.Capability,
			&t.ClaimedBy, &t.ClaimedAt, &t.DoneAt, &t.CreatedAt, &t.Attempt,
			&t.MaxAttempts, &t.ProjectID, &t.Metadata,
		); err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}
