# Minuano — Development Guide

## Project

Minuano is a Go CLI that coordinates Claude Code agents via tmux, using PostgreSQL as the
coordination substrate. See `DESIGN.md` for the full specification.

## Architecture

- **Language**: Go
- **CLI framework**: cobra (`github.com/spf13/cobra`)
- **Database**: PostgreSQL 16 via pgx/v5 (`github.com/jackc/pgx/v5`) — no ORM
- **TUI**: bubbletea + lipgloss (for watch views)
- **All SQL lives in `internal/db/queries.go`** — no raw SQL in command files

## Repository Layout

```
cmd/minuano/main.go          Entry point, cobra root command
internal/db/              Database layer (connection, queries, migrations)
internal/tmux/            Tmux session/window management
internal/agent/           Agent lifecycle (spawn, kill, heartbeat)
internal/tui/             Optional bubbletea watch views
scripts/                  Bash scripts called by agents (minuano-claim, minuano-done, etc)
claude/                   CLAUDE.md agent loop instructions
docker/                   docker-compose.yml for postgres
tasks/                    Ordered implementation tasks (this file governs execution)
```

## Task Execution Protocol

Implementation follows the ordered tasks in `tasks/`. **Always work sequentially.**

### How to proceed

1. **Read the current task file** from `tasks/` in numeric order (01, 02, 03, ...).
2. **Check "Depends on"** — if a dependency task is not yet complete, do that one first.
3. **Read DESIGN.md** for the detailed specification of whatever you're building. The task
   file tells you *what* to build; DESIGN.md tells you *how*.
4. **Implement** the task following its Steps and Acceptance criteria.
5. **Verify** the Acceptance criteria are met before moving on.
6. **Move to the next task** in numeric order.

### Task phases (in order)

| Phase | Tasks | What it covers |
|-------|-------|----------------|
| 1 — Foundation | 01–05 | Project init, Docker, DB schema, connection layer, queries |
| 2 — Core CLI | 06–14 | CLI skeleton, all DB-facing commands (up/down/migrate/add/status/tree/show/edit/search) |
| 3 — Tmux & Agent Infra | 15–16 | Tmux package, agent lifecycle package |
| 4 — Agent CLI Commands | 17–23 | run, spawn, agents, attach, logs, kill, reclaim |
| 5 — Agent Runtime | 24–25 | Bash scripts for agents, CLAUDE.md agent loop |
| 6 — Polish | 26 | Bubbletea TUI watch view |
| 7 — Tramuntana Integration | I-01–I-06 | See `/home/otavio/code/tramuntana/tasks/integration/` and `INTEGRATION.md` |

### Current progress

Track which task you're on. When you complete a task, note it here:

- [x] 01 — Project Initialization
- [x] 02 — Docker Setup
- [x] 03 — Database Migration File
- [x] 04 — Database Connection Layer
- [x] 05 — Database Queries
- [x] 06 — CLI Skeleton
- [x] 07 — `minuano up` / `minuano down`
- [x] 08 — `minuano migrate`
- [x] 09 — `minuano add`
- [x] 10 — `minuano status`
- [x] 11 — `minuano tree`
- [x] 12 — `minuano show`
- [x] 13 — `minuano edit`
- [x] 14 — `minuano search`
- [x] 15 — Tmux Package
- [x] 16 — Agent Package
- [x] 17 — `minuano run`
- [x] 18 — `minuano spawn`
- [x] 19 — `minuano agents`
- [x] 20 — `minuano attach`
- [x] 21 — `minuano logs`
- [x] 22 — `minuano kill`
- [x] 23 — `minuano reclaim`
- [x] 24 — Agent Scripts
- [x] 25 — Agent CLAUDE.md
- [x] 26 — TUI Watch View
- [x] I-01 — `--json` flag on status and show
- [x] I-02 — `ClaimByID` query
- [x] I-03 — Project filter on `AtomicClaim`
- [x] I-04 — `minuano-pick` script
- [x] I-05 — `--project` flag on `minuano-claim`
- [x] I-06 — `minuano prompt` command

## Conventions

- **No ORM.** All SQL in `internal/db/queries.go` as typed Go functions.
- **No extra dependencies** beyond those listed in DESIGN.md without a clear reason.
- **Partial ID matching** — any command that takes a task/agent ID should support prefix matching.
- **Status symbols**: `○` pending, `◎` ready, `●` claimed, `✓` done, `✗` failed.
- **Error handling** — return errors to the caller, print user-friendly messages at the command level.
- **Test commands** — use `go test ./...` as the default unless overridden by task metadata.

## Building & Running

```bash
go build ./cmd/minuano          # build the binary
minuano up                      # start postgres
minuano migrate                 # apply migrations
minuano status                  # check tasks
```
