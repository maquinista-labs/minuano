# LISTEN/NOTIFY Plan: Event-Driven Coordination

Bring Minuano closer to Linda's blocking `in()` primitive by replacing polling
loops with Postgres LISTEN/NOTIFY. Propagate events through a new `minuano watch`
command to Tramuntana and Telegram topics.

Context: [AI Orchestration: Reinventing Linda](https://otavio.cat/posts/ai-orchestration-reinventing-linda/)

---

## Current state: polling everywhere

| Component | What it polls | Interval | Token cost |
|-----------|--------------|----------|------------|
| `minuano merge --watch` | `merge_queue` table | 5s | Zero (SQL) |
| `minuano agents --watch` | `agents` + `tasks` tables | 2s | Zero (SQL) |
| `minuano-claim` | One-shot query, no poll | — | Zero (SQL) |
| Tramuntana session monitor | JSONL file mtimes | 2s | Zero (file I/O) |
| Tramuntana status poller | tmux pane capture | 1s | Zero (tmux) |

No token waste — all polling is mechanical (SQL, file I/O, tmux). But it adds
latency (up to 5s for merges) and prevents true event-driven flows. The biggest
gap is that `/auto` mode in Tramuntana is fire-and-forget: it sends a prompt and
has no way to know when a task completes, what unblocked, or when to claim next.

---

## Linda primitive gap

| Linda | Current Minuano | With LISTEN/NOTIFY |
|-------|----------------|-------------------|
| `in(template)` blocks until match | Returns empty if nothing ready | Blocks until `task_ready` fires, then claims atomically |
| `eval(t)` completion propagates | Trigger sets dependents to `ready` (but nobody knows) | `task_ready` notification wakes listeners immediately |
| Event subscription | Not supported | `LISTEN` on typed channels |

---

## Phase 1: Postgres triggers (Minuano)

Add NOTIFY calls to existing triggers and create new ones. No schema changes
needed — just trigger modifications.

### 1.1 Task readiness notification

The `refresh_ready_tasks()` trigger already runs when a task is marked done and
sets dependents to `ready`. Add a NOTIFY:

```sql
-- Inside refresh_ready_tasks(), after UPDATE ... SET status = 'ready'
PERFORM pg_notify('task_ready', pending_task.id || '|' || COALESCE(pending_task.project_id, ''));
```

**Channel**: `task_ready`
**Payload**: `<task_id>|<project_id>`

Also fire on direct `INSERT INTO tasks ... status = 'ready'` (tasks with no
dependencies start as ready):

```sql
CREATE OR REPLACE FUNCTION notify_task_ready()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'ready' AND (OLD IS NULL OR OLD.status != 'ready') THEN
    PERFORM pg_notify('task_ready', NEW.id || '|' || COALESCE(NEW.project_id, ''));
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER task_ready_notify
  AFTER INSERT OR UPDATE OF status ON tasks
  FOR EACH ROW EXECUTE FUNCTION notify_task_ready();
```

### 1.2 Task done notification

```sql
CREATE OR REPLACE FUNCTION notify_task_done()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'done' AND OLD.status != 'done' THEN
    PERFORM pg_notify('task_done', NEW.id || '|' || COALESCE(NEW.project_id, '') || '|' || COALESCE(NEW.claimed_by, ''));
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER task_done_notify
  AFTER UPDATE OF status ON tasks
  FOR EACH ROW EXECUTE FUNCTION notify_task_done();
```

**Channel**: `task_done`
**Payload**: `<task_id>|<project_id>|<agent_id>`

### 1.3 Task failed notification

```sql
CREATE OR REPLACE FUNCTION notify_task_failed()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'failed' AND OLD.status != 'failed' THEN
    PERFORM pg_notify('task_failed', NEW.id || '|' || COALESCE(NEW.project_id, ''));
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER task_failed_notify
  AFTER UPDATE OF status ON tasks
  FOR EACH ROW EXECUTE FUNCTION notify_task_failed();
```

**Channel**: `task_failed`
**Payload**: `<task_id>|<project_id>`

### 1.4 Merge queue notification

```sql
CREATE OR REPLACE FUNCTION notify_merge_queue()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    PERFORM pg_notify('merge_ready', NEW.id::text || '|' || NEW.task_id);
  ELSIF NEW.status != OLD.status THEN
    PERFORM pg_notify('merge_' || NEW.status, NEW.id::text || '|' || NEW.task_id);
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER merge_queue_notify
  AFTER INSERT OR UPDATE OF status ON merge_queue
  FOR EACH ROW EXECUTE FUNCTION notify_merge_queue();
```

**Channels**: `merge_ready`, `merge_merged`, `merge_conflict`, `merge_failed`
**Payload**: `<queue_id>|<task_id>`

### Migration file

All triggers go in `003_listen_notify.sql`. Pure additive — no table changes,
fully backward compatible. Existing polling continues to work.

---

## Phase 2: `minuano watch` command

New streaming command that holds a LISTEN connection and emits JSON events to
stdout. This is the bridge between Postgres events and external consumers
(Tramuntana, scripts, dashboards).

### Interface

```bash
minuano watch [--project <id>] [--channels task_ready,task_done,merge_merged]
```

**Default channels**: `task_ready`, `task_done`, `task_failed`, `merge_ready`,
`merge_merged`, `merge_conflict`, `merge_failed`

### Output format

One JSON object per line (JSONL):

```jsonl
{"channel":"task_ready","task_id":"design-auth","project_id":"backend","ts":"2026-02-21T14:30:00Z"}
{"channel":"task_done","task_id":"design-auth","project_id":"backend","agent_id":"agent-1","ts":"2026-02-21T14:35:00Z"}
{"channel":"task_ready","task_id":"impl-endpoints","project_id":"backend","ts":"2026-02-21T14:35:00Z"}
{"channel":"merge_merged","queue_id":"42","task_id":"design-auth","ts":"2026-02-21T14:35:05Z"}
```

### Implementation

```go
// cmd/minuano/cmd_watch.go
func runWatch(cmd *cobra.Command, args []string) error {
    conn, _ := pgx.Connect(ctx, dbURL)
    defer conn.Close(ctx)

    for _, ch := range channels {
        conn.Exec(ctx, "LISTEN "+ch)
    }

    enc := json.NewEncoder(os.Stdout)
    for {
        notification, _ := conn.WaitForNotification(ctx)
        event := parsePayload(notification.Channel, notification.Payload)
        if projectFilter != "" && event.ProjectID != projectFilter {
            continue
        }
        enc.Encode(event)
    }
}
```

Key properties:
- Long-lived connection, blocks on `WaitForNotification` (no polling)
- Filters by project client-side (LISTEN is per-channel, not per-payload)
- JSONL output for easy piping and subprocess consumption
- Exits cleanly on SIGINT/context cancellation

### Update `minuano merge --watch`

Replace the 5s poll loop with LISTEN internally:

```go
// Instead of time.Sleep(5 * time.Second)
conn.Exec(ctx, "LISTEN merge_ready")
for {
    conn.WaitForNotification(ctx)
    processMergeQueue(ctx, db)
}
```

---

## Phase 3: Tramuntana integration

Tramuntana spawns `minuano watch` as a long-running subprocess and routes events
to Telegram topics.

### Architecture

```
Postgres ──NOTIFY──> minuano watch ──stdout/JSONL──> Tramuntana ──Telegram API──> Topics
```

This keeps the clean CLI boundary. Tramuntana doesn't need a direct Postgres
connection or knowledge of the schema. The existing CLI bridge (`exec.Command`)
stays for request/response operations (`status`, `show`, `prompt`).

### Watcher goroutine

```go
// internal/minuano/watcher.go
type Watcher struct {
    bridge  *Bridge
    cmd     *exec.Cmd
    events  chan Event
}

func (w *Watcher) Start(ctx context.Context, project string) error {
    args := []string{"watch", "--project", project}
    w.cmd = exec.CommandContext(ctx, w.bridge.bin, args...)
    stdout, _ := w.cmd.StdoutPipe()
    w.cmd.Start()

    go func() {
        scanner := bufio.NewScanner(stdout)
        for scanner.Scan() {
            var event Event
            json.Unmarshal(scanner.Bytes(), &event)
            w.events <- event
        }
    }()
    return nil
}
```

### Event routing to topics

The bot registers a handler that reads from the watcher's event channel and
routes to the appropriate topic based on project bindings:

```go
// internal/bot/events.go
func (b *Bot) handleMinuanoEvent(event minuano.Event) {
    // Find all topics bound to this project
    topics := b.state.TopicsForProject(event.ProjectID)

    for _, topic := range topics {
        switch event.Channel {
        case "task_done":
            b.sendToTopic(topic, formatTaskDone(event))
        case "task_ready":
            b.sendToTopic(topic, formatTaskReady(event))
        case "task_failed":
            b.sendToTopic(topic, formatTaskFailed(event))
        case "merge_merged":
            b.sendToTopic(topic, formatMergeDone(event))
        case "merge_conflict":
            b.handleMergeConflict(topic, event)
        }
    }
}
```

### Watcher lifecycle

- **Start**: When first `/project` binding is created, or on `tramuntana serve`
  startup if any project bindings exist in state
- **Restart**: If the subprocess dies, respawn with backoff
- **Stop**: When last project binding is removed, or on graceful shutdown
- **Multiple projects**: One `minuano watch` per project (filtered), or one
  global watcher that routes by project_id

One global watcher is simpler. Start it unconditionally on `tramuntana serve` if
`MINUANO_BIN` is configured. Filter events to topics with active project
bindings. Ignore events for unbound projects.

---

## Phase 4: Event-driven auto mode

The current `/auto` mode sends a loop prompt to Claude and hopes for the best.
With events, Tramuntana can orchestrate the loop itself:

### Current flow (fire-and-forget)

```
User: /auto
Bot:  sends loop prompt to Claude
      ... no visibility until Claude happens to write output ...
```

### New flow (event-driven)

```
User: /auto
Bot:  claims first task via bridge
      sends single-task prompt to Claude
      waits for task_done event from watcher
      ↓
Bot:  "Task design-auth completed. Tests passed."
      "Merged → main (a1b2c3d)"
      "2 tasks now ready: impl-endpoints, write-tests"
      claims next task
      sends single-task prompt
      waits for task_done event
      ↓
Bot:  "Task impl-endpoints completed."
      ...
      ↓
Bot:  "Queue empty. All 5 tasks completed."
```

### Implementation

```go
// internal/bot/auto.go
func (b *Bot) runAutoMode(ctx context.Context, topic TopicInfo, project string) {
    for {
        // Claim next task (existing bridge call)
        tasks, _ := b.bridge.Status(project)
        ready := filterReady(tasks)
        if len(ready) == 0 {
            b.sendToTopic(topic, "Queue empty. Auto mode finished.")
            return
        }

        taskID := ready[0].ID
        prompt, _ := b.bridge.PromptSingle(taskID)
        b.sendPromptToWindow(topic, prompt)
        b.sendToTopic(topic, fmt.Sprintf("Claimed: %s", taskID))

        // Wait for completion event (blocking, no polling)
        select {
        case event := <-b.taskDoneForTopic(topic):
            b.sendToTopic(topic, formatCompletion(event))
        case event := <-b.taskFailedForTopic(topic):
            b.sendToTopic(topic, formatFailure(event))
            // Optionally retry or stop
        case <-ctx.Done():
            return
        }
    }
}
```

The key change: `select` on event channels replaces hope. Each step is visible
in the Telegram topic. The user can `/esc` to cancel, or watch the autonomous
loop progress in real time.

### Auto mode with worktrees

Combine with `/pickw` for full isolation:

```
User: /auto --worktrees
Bot:  creates worktree for first task
      sends prompt in new topic
      waits for task_done
      ↓
Bot:  "Task done. Merging..."
      waits for merge_merged
      ↓
Bot:  "Merged → main. Cleaning up worktree."
      claims next task, creates new worktree
      ...
```

Each task gets its own topic, worktree, and branch. Merge happens automatically.
Conflicts create merge topics (existing `/merge` flow). All driven by events.

---

## Telegram UX: what the user sees

### Passive notifications (any project-bound topic)

When tasks change state, all topics bound to that project get updates:

```
Bot: ✓ Task design-auth completed by agent-1
Bot: → 2 tasks now ready: impl-endpoints, write-tests
Bot: ✓ Merged minuano/backend-design-auth → main (a1b2c3d)
Bot: ✗ Task write-tests failed (attempt 2/3): test timeout
```

These appear even if the user isn't running `/auto` — pure observability. Any
topic bound via `/project backend` sees backend events.

### Active auto mode (driving topic)

The topic that ran `/auto` gets the same notifications plus orchestration:

```
Bot: Auto mode started for project backend (3 ready tasks)
Bot: Claimed: design-auth (priority 8)
     [Claude works in this topic's window]
Bot: ✓ Task design-auth completed. Tests passed.
Bot: ✓ Merged → main (a1b2c3d)
Bot: Claimed: impl-endpoints (priority 7)
     [Claude works]
Bot: ✓ Task impl-endpoints completed.
Bot: → Queue empty. Auto mode finished. 5/5 tasks done.
```

### Interactive control during auto mode

The user can interact during auto mode without breaking the loop:

- Send a message → forwarded to Claude as usual (interrupt/guide)
- `/esc` → interrupt current work, auto mode pauses
- `/tasks` → show current queue state
- `/stop` (new command) → cancel auto mode, leave current task claimed

### Merge conflict notification

```
Bot: ⚠ Merge conflict on minuano/backend-impl-endpoints
     Conflicted files: internal/auth/handler.go, internal/auth/middleware.go
     Created topic: "merge-impl-endpoints [backend]"
     [Claude resolving conflicts in new topic]
Bot: ✓ Merge conflict resolved → main (f8g9h0i)
```

---

## Implementation order

| Phase | Scope | Effort | Depends on |
|-------|-------|--------|------------|
| 1 | Postgres triggers (`003_listen_notify.sql`) | Hours | Nothing |
| 2a | `minuano watch` command | 1 day | Phase 1 |
| 2b | Update `minuano merge --watch` to use LISTEN | Hours | Phase 1 |
| 3a | Tramuntana watcher goroutine | 1 day | Phase 2a |
| 3b | Event routing to topics (passive notifications) | 1 day | Phase 3a |
| 4a | Event-driven `/auto` mode | 1-2 days | Phase 3b |
| 4b | Auto mode + worktrees integration | 1 day | Phase 4a |

Phases 1 and 2b are fully backward compatible — existing polling continues to
work, just faster. Phase 2a is additive. Phases 3-4 are Tramuntana-only changes.

### What stays as polling

- **Tramuntana session monitor** (JSONL files) — no Postgres event for "Claude
  wrote output". Would need inotify or Claude Code hook changes. Not worth it;
  2s polling is fine for file I/O.
- **Tramuntana status poller** (tmux pane) — terminal state isn't in Postgres.
  1s polling is fine.
- **`minuano agents --watch`** (TUI) — pure display, low priority. Could use
  LISTEN but 2s refresh is acceptable.

---

## What this achieves relative to Linda

After all phases:

| Linda primitive | Implementation |
|-----------------|---------------|
| `out(t)` — put | `minuano add` → INSERT + NOTIFY `task_ready` |
| `in(template)` — blocking take | `LISTEN task_ready` + `AtomicClaim` (true blocking `in()`) |
| `rd(template)` — non-blocking read | `minuano show` / `minuano status` (unchanged) |
| `eval(t)` — live tuple → data | Agent works → `minuano-done` → NOTIFY `task_done` (completion propagates instantly) |
| Event subscription | `minuano watch` (spatial + temporal decoupling via typed channels) |

The remaining gap vs pure Linda: no arbitrary pattern matching on tuple fields.
But `project + capability + priority` covers the real use cases. The coordination
semantics — atomic take, blocking wait, event propagation — are complete.
