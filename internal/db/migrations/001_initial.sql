-- Tasks: the work units
CREATE TABLE tasks (
  id           TEXT PRIMARY KEY,
  title        TEXT        NOT NULL,
  body         TEXT        NOT NULL DEFAULT '',
  status       TEXT        NOT NULL DEFAULT 'pending',
    -- pending | ready | claimed | done | failed
  priority     INTEGER     NOT NULL DEFAULT 5,
  capability   TEXT,                          -- NULL = any agent
  claimed_by   TEXT,
  claimed_at   TIMESTAMPTZ,
  done_at      TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  attempt      INTEGER     NOT NULL DEFAULT 0,
  max_attempts INTEGER     NOT NULL DEFAULT 3,
  project_id   TEXT,
  metadata     JSONB                          -- e.g. {"test_cmd": "go test ./..."}
);

CREATE INDEX idx_tasks_status   ON tasks(status, priority DESC, created_at ASC);
CREATE INDEX idx_tasks_project  ON tasks(project_id) WHERE project_id IS NOT NULL;

-- Dependencies: the DAG
CREATE TABLE task_deps (
  task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  depends_on TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, depends_on)
);

CREATE INDEX idx_deps_depends_on ON task_deps(depends_on);

-- Context: persistent agent memory
CREATE TABLE task_context (
  id          BIGSERIAL   PRIMARY KEY,
  task_id     TEXT        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  agent_id    TEXT,
  kind        TEXT        NOT NULL,
    -- observation | result | handoff | inherited | test_failure
  content     TEXT        NOT NULL,
  source_task TEXT        REFERENCES tasks(id),   -- set for kind=inherited
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_context_task ON task_context(task_id, created_at);
CREATE INDEX idx_context_fts  ON task_context
  USING gin(to_tsvector('english', content));

-- Agents: running instance registry
CREATE TABLE agents (
  id           TEXT        PRIMARY KEY,
  tmux_session TEXT        NOT NULL,
  tmux_window  TEXT        NOT NULL,
  task_id      TEXT        REFERENCES tasks(id),
  status       TEXT        NOT NULL DEFAULT 'idle',
    -- idle | working | dead
  started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen    TIMESTAMPTZ
);

-- Trigger: when a task is marked done, cascade readiness to dependents
CREATE OR REPLACE FUNCTION refresh_ready_tasks()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'done' THEN
    UPDATE tasks
    SET status = 'ready'
    WHERE status = 'pending'
      AND id IN (
        SELECT task_id FROM task_deps WHERE depends_on = NEW.id
      )
      AND id NOT IN (
        SELECT td.task_id
        FROM task_deps td
        JOIN tasks t ON t.id = td.depends_on
        WHERE t.status != 'done'
      );
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER on_task_done
AFTER UPDATE OF status ON tasks
FOR EACH ROW
WHEN (NEW.status = 'done')
EXECUTE FUNCTION refresh_ready_tasks();
