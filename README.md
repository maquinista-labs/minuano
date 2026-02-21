# Minuano

> Minuano is a wind that cleans the sky and the pocket

A Go CLI that coordinates [Claude Code](https://docs.anthropic.com/en/docs/claude-code) agents via tmux, using PostgreSQL as the coordination substrate.

Agents atomically claim tasks from a shared queue, inherit context from dependency tasks, run tests locally as the completion gate, and write persistent observations back to the database across context resets. No orchestrator, no background token burn — the database drives the loop.

Heavily inspired by Steve Yegge's [Beads](https://github.com/steveyegge/beads) / [Gas Town](https://sunsama.com/blog/gas-town) and its pull-based coordination model — agents claim work from a shared queue instead of being directed by an orchestrator.

The key difference: Gas Town keeps agent sessions alive, continuously pulling tasks and burning tokens on idle loops. Minuano terminates the Claude Code process after each task and spawns a fresh one for the next. This trades session continuity for precise token control — agents only consume tokens when actively working.

See [AI Orchestration: Reinventing Linda](https://otavio.cat/posts/ai-orchestration-reinventing-linda/) for how tuple-space coordination patterns informed this design.

## How it works

1. You describe work as tasks with dependencies (a DAG).
2. `minuano run` spawns Claude Code agents in tmux windows.
3. Each agent pulls a ready task, reads inherited context, writes code, runs tests, and marks it done.
4. When a task completes, its dependents become ready automatically (via a Postgres trigger).
5. Agents loop until the queue is empty.

With `--worktrees`, each agent works in an isolated git worktree. On task completion, changes auto-commit and enqueue for merge via `minuano merge`.

## Quick start

```bash
make build

minuano up                # start postgres (Docker)
minuano migrate           # apply schema

minuano add "Design auth system" --priority 8
minuano add "Implement auth endpoints" --after design-auth
minuano tree              # visualize the DAG

minuano run --agents 2 --worktrees   # spawn agents in isolated worktrees
minuano agents --watch               # monitor progress
minuano merge --watch                # process merge queue
```

## Minuano vs Tramuntana

**Minuano** (this tool) is the headless engine. Use it for:

- Batch processing — `minuano run --agents 4` and walk away
- CI pipelines — scripted task creation and agent spawning
- Local dev — direct terminal access to agents via `minuano attach`

**[Tramuntana](https://github.com/otaviocarvalho/tramuntana)** is the Telegram interface. Use it for:

- Interactive sessions — chat with Claude Code from your phone
- Per-topic isolation — one Telegram topic = one Claude Code session
- Mobile-friendly — screenshots, inline keyboards, rich formatting

Both share the same Minuano database. Tramuntana calls Minuano commands under the hood.

## Commands

### Infrastructure

| Command | Description |
|---------|-------------|
| `minuano up` | Start Docker postgres container |
| `minuano down` | Stop Docker postgres container |
| `minuano migrate` | Run pending database migrations |

### Task management

**`minuano add <title>`** — Create a task

| Flag | Description | Default |
|------|-------------|---------|
| `--after <id>` | Dependency task ID (repeatable) | — |
| `--priority <0-10>` | Task priority | `5` |
| `--capability <str>` | Required agent capability | — |
| `--test-cmd <str>` | Test command override | `go test ./...` |
| `--project <id>` | Project ID | `$MINUANO_PROJECT` |
| `--body <str>` | Task specification body | — |

**`minuano show <id>`** — Print task spec + full context log

| Flag | Description |
|------|-------------|
| `--json` | Output as JSON |

**`minuano edit <id>`** — Open task body in `$EDITOR`

**`minuano status`** — Table view of all tasks

| Flag | Description |
|------|-------------|
| `--project <id>` | Filter by project |
| `--json` | Output as JSON |

**`minuano tree`** — Print dependency tree with status symbols

| Flag | Description |
|------|-------------|
| `--project <id>` | Filter by project |

**`minuano search <query>`** — Full-text search across task context

### Agent management

**`minuano run`** — Spawn agents in tmux

| Flag | Description | Default |
|------|-------------|---------|
| `--agents <n>` | Number of agents | `1` |
| `--names <a,b,c>` | Comma-separated agent names | auto-generated |
| `--capability <str>` | Agent capability | — |
| `--attach` | Attach to tmux after spawning | `false` |
| `--worktrees` | Isolate each agent in a git worktree | `false` |

**`minuano spawn <name>`** — Spawn a single named agent

| Flag | Description | Default |
|------|-------------|---------|
| `--capability <str>` | Agent capability | — |
| `--worktrees` | Isolate in a git worktree | `false` |

**`minuano agents`** — Show running agents

| Flag | Description |
|------|-------------|
| `--watch` | Refresh every 2s |

**`minuano attach [id]`** — Attach to tmux session; jump to agent/task window if ID given

**`minuano logs <id>`** — Capture lines from agent's tmux pane

| Flag | Description | Default |
|------|-------------|---------|
| `--lines <n>` | Number of lines | `50` |

**`minuano kill [id]`** — Kill agent, release claimed tasks

| Flag | Description |
|------|-------------|
| `--all` | Kill all agents |

**`minuano reclaim`** — Reset stale claimed tasks back to ready

| Flag | Description | Default |
|------|-------------|---------|
| `--minutes <n>` | Stale threshold | `30` |

### Prompt generation

These commands output prompts for Claude Code agents. Used by Tramuntana and agent bootstrap scripts.

**`minuano prompt single <task-id>`** — Prompt for working on one task

**`minuano prompt auto`** — Loop prompt that claims tasks until the queue is empty

| Flag | Description |
|------|-------------|
| `--project <id>` | Project to claim from (`$MINUANO_PROJECT`) |

**`minuano prompt batch <id1> [id2...]`** — Prompt for completing multiple tasks in sequence

### Merge queue

**`minuano merge`** — Process pending merge queue entries

| Flag | Description |
|------|-------------|
| `--watch` | Poll every 5s and process continuously |

**`minuano merge status`** — Show merge queue status

### Global flags

| Flag | Description | Default |
|------|-------------|---------|
| `--db <url>` | Database URL | `$DATABASE_URL` |
| `--session <name>` | Tmux session name | `$MINUANO_SESSION` or `minuano` |

## Agent scripts

Helper scripts placed in `$PATH` for agents at runtime. These are the agent's interface to the coordination database.

| Script | Usage | Description |
|--------|-------|-------------|
| `minuano-claim` | `minuano-claim [--project <name>]` | Atomically claim one ready task. Prints JSON or exits empty. |
| `minuano-pick` | `minuano-pick <task-id>` | Claim a specific task by ID (prefix match). |
| `minuano-done` | `minuano-done <task-id> <summary>` | Run tests, mark done on pass, record failure on fail. Auto-commits and enqueues merge in worktree mode. |
| `minuano-observe` | `minuano-observe <task-id> <note>` | Record an observation to the task's context log. |
| `minuano-handoff` | `minuano-handoff <task-id> <note>` | Record a handoff note before long operations or context resets. |

All scripts require `DATABASE_URL` and `AGENT_ID` environment variables (set automatically by `minuano spawn`).

## Environment variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | — (required) |
| `MINUANO_SESSION` | Tmux session name | `minuano` |
| `MINUANO_PROJECT` | Default project ID for commands | — |
| `EDITOR` | Text editor for `minuano edit` | `vi` |
| `MINUANO_TEST_CMD` | Override test command in `minuano-done` | task metadata or `go test ./...` |
| `MINUANO_BASE_BRANCH` | Base branch for worktree merge | `main` |

Set automatically by `minuano spawn`:

| Variable | Description |
|----------|-------------|
| `AGENT_ID` | Unique agent identifier |
| `WORKTREE_DIR` | Absolute path to agent's worktree (worktree mode only) |
| `BRANCH` | Git branch name `minuano/<agent-id>` (worktree mode only) |

## Requirements

- Go 1.24+
- Docker (for PostgreSQL)
- tmux
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI

## License

MIT
