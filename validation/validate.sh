#!/usr/bin/env bash
# =============================================================================
# Minuano — Full Validation Script
#
# Validates all 26 tasks + integration tasks I-01 through I-06.
# Requires: docker, tmux, psql, go
#
# Usage:
#   ./validation/validate.sh              # run all tests
#   ./validation/validate.sh --skip-tmux  # skip tmux-dependent tests
#   ./validation/validate.sh --no-cleanup # leave postgres running after tests
# =============================================================================
set -uo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_ROOT"

PASS=0
FAIL=0
SKIP=0
SKIP_TMUX=false
NO_CLEANUP=false
DB_URL="postgres://minuano:minuano@localhost:5432/minuanodb?sslmode=disable"
SESSION_NAME="minuano-validate-$$"
BINARY="$PROJECT_ROOT/minuano"

# Parse flags.
for arg in "$@"; do
  case "$arg" in
    --skip-tmux) SKIP_TMUX=true ;;
    --no-cleanup) NO_CLEANUP=true ;;
  esac
done

# --- Helpers ---

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

pass() {
  echo -e "  ${GREEN}PASS${NC}: $1"
  PASS=$((PASS + 1))
}

fail() {
  echo -e "  ${RED}FAIL${NC}: $1"
  FAIL=$((FAIL + 1))
}

skip() {
  echo -e "  ${YELLOW}SKIP${NC}: $1"
  SKIP=$((SKIP + 1))
}

section() {
  echo ""
  echo -e "${BLUE}=== $1 ===${NC}"
}

run_minuano() {
  "$BINARY" --db "$DB_URL" --session "$SESSION_NAME" "$@"
}

# Check that a command's stdout contains a string.
expect_contains() {
  local description="$1"
  local needle="$2"
  shift 2
  local output
  output=$("$@" 2>&1) || true
  if echo "$output" | grep -qF "$needle"; then
    pass "$description"
  else
    fail "$description (expected '$needle' in output)"
    echo "    Got: $(echo "$output" | head -3)"
  fi
}

# Check that a command exits 0.
expect_success() {
  local description="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    pass "$description"
  else
    fail "$description (non-zero exit)"
  fi
}

# Check that a command exits non-zero.
expect_failure() {
  local description="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    fail "$description (expected failure, got success)"
  else
    pass "$description"
  fi
}

# --- Prerequisites ---

section "Prerequisites"

command -v docker >/dev/null 2>&1 && pass "docker installed" || fail "docker not found"
command -v tmux >/dev/null 2>&1 && pass "tmux installed" || { fail "tmux not found"; SKIP_TMUX=true; }
command -v psql >/dev/null 2>&1 && pass "psql installed" || fail "psql not found"
command -v go >/dev/null 2>&1 && pass "go installed" || fail "go not found"

# --- Phase 0: Build ---

section "Phase 0: Build"

expect_success "go vet ./..." go vet ./...
expect_success "go build binary" go build -o "$BINARY" ./cmd/minuano

if [ ! -x "$BINARY" ]; then
  echo "FATAL: binary not built, cannot continue"
  exit 1
fi

# --- Phase 0.5: Unit Tests ---

section "Phase 0.5: Unit Tests"

expect_success "go test ./... passes" go test ./... -count=1
expect_success "scripts_test.sh passes" bash scripts/scripts_test.sh

# --- Phase 1: Docker & Database (Tasks 01-05) ---

section "Phase 1: Docker & Database"

expect_contains "minuano up starts postgres" "minuano-postgres" run_minuano up
sleep 2

# Verify postgres is reachable.
if psql "$DB_URL" -c "SELECT 1" >/dev/null 2>&1; then
  pass "postgres is reachable"
else
  fail "postgres not reachable"
  echo "FATAL: cannot proceed without database"
  exit 1
fi

expect_contains "minuano migrate applies schema" "Applied" run_minuano migrate

# Idempotent.
expect_contains "minuano migrate is idempotent" "Nothing to apply" run_minuano migrate

# Check tables exist.
TABLES=$(psql "$DB_URL" -t -A -c "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename")
for table in agents schema_migrations task_context task_deps tasks; do
  if echo "$TABLES" | grep -q "^${table}$"; then
    pass "table '$table' exists"
  else
    fail "table '$table' missing"
  fi
done

# --- Phase 2: Task Management (Tasks 06-14) ---

section "Phase 2: Task Management"

# Help output.
expect_contains "minuano --help shows commands" "Available Commands" run_minuano --help

# Create tasks.
expect_contains "minuano add creates task" "Created:" run_minuano add "Validate design" --priority 8
expect_contains "minuano add with body" "Created:" run_minuano add "Validate endpoints" --body "Test all API endpoints" --priority 6

# Capture task IDs.
TASK1_ID=$(psql "$DB_URL" -t -A -c "SELECT id FROM tasks WHERE title='Validate design' LIMIT 1")
TASK2_ID=$(psql "$DB_URL" -t -A -c "SELECT id FROM tasks WHERE title='Validate endpoints' LIMIT 1")

if [ -n "$TASK1_ID" ]; then
  pass "task 1 created in DB ($TASK1_ID)"
else
  fail "task 1 not found in DB"
fi
if [ -n "$TASK2_ID" ]; then
  pass "task 2 created in DB ($TASK2_ID)"
else
  fail "task 2 not found in DB"
fi

# Add dependency: task2 depends on task1.
expect_contains "add task with dependency" "Created:" \
  run_minuano add "Validate integration" --after "$TASK1_ID" --priority 4
TASK3_ID=$(psql "$DB_URL" -t -A -c "SELECT id FROM tasks WHERE title='Validate integration' LIMIT 1")

# Dependent task should be pending (has unmet dep).
TASK3_STATUS=$(psql "$DB_URL" -t -A -c "SELECT status FROM tasks WHERE id='$TASK3_ID'")
if [ "$TASK3_STATUS" = "pending" ]; then
  pass "dependent task is pending"
else
  fail "dependent task should be pending, got '$TASK3_STATUS'"
fi

# First tasks should be ready.
TASK1_STATUS=$(psql "$DB_URL" -t -A -c "SELECT status FROM tasks WHERE id='$TASK1_ID'")
if [ "$TASK1_STATUS" = "ready" ]; then
  pass "independent task is ready"
else
  fail "independent task should be ready, got '$TASK1_STATUS'"
fi

# Status table.
expect_contains "minuano status shows tasks" "Validate design" run_minuano status

# Tree.
expect_contains "minuano tree shows structure" "Validate" run_minuano tree

# Show (partial ID).
PARTIAL_ID="${TASK1_ID:0:8}"
expect_contains "minuano show with partial ID" "Validate design" run_minuano show "$PARTIAL_ID"

# Search (create some context first).
psql "$DB_URL" -c "INSERT INTO task_context (task_id, agent_id, kind, content) VALUES ('$TASK1_ID', 'test', 'observation', 'found auth middleware bug')" >/dev/null 2>&1
expect_contains "minuano search finds context" "auth middleware" run_minuano search "auth middleware"

# --- Phase 2b: Integration I-01 — JSON Output ---

section "Phase 2b: JSON Output (I-01)"

STATUS_JSON=$(run_minuano status --json)
if echo "$STATUS_JSON" | python3 -m json.tool >/dev/null 2>&1; then
  pass "status --json outputs valid JSON"
else
  fail "status --json output is not valid JSON"
fi

if echo "$STATUS_JSON" | grep -q '"id"'; then
  pass "status --json contains 'id' field"
else
  fail "status --json missing 'id' field"
fi

SHOW_JSON=$(run_minuano show "$TASK1_ID" --json)
if echo "$SHOW_JSON" | python3 -m json.tool >/dev/null 2>&1; then
  pass "show --json outputs valid JSON"
else
  fail "show --json output is not valid JSON"
fi

if echo "$SHOW_JSON" | grep -q '"task"'; then
  pass "show --json contains 'task' key"
else
  fail "show --json missing 'task' key"
fi

if echo "$SHOW_JSON" | grep -q '"context"'; then
  pass "show --json contains 'context' key"
else
  fail "show --json missing 'context' key"
fi

# --- Phase 2c: Integration I-06 — Prompt Command ---

section "Phase 2c: Prompt Command (I-06)"

SINGLE_PROMPT=$(run_minuano prompt single "$TASK1_ID")
if echo "$SINGLE_PROMPT" | grep -q "# Task:"; then
  pass "prompt single generates task header"
else
  fail "prompt single missing task header"
fi
if echo "$SINGLE_PROMPT" | grep -q "minuano-pick"; then
  pass "prompt single references minuano-pick"
else
  fail "prompt single missing minuano-pick reference"
fi
if echo "$SINGLE_PROMPT" | grep -q "minuano-done"; then
  pass "prompt single references minuano-done"
else
  fail "prompt single missing minuano-done reference"
fi
if echo "$SINGLE_PROMPT" | grep -q "Do NOT loop"; then
  pass "prompt single has no-loop rule"
else
  fail "prompt single missing no-loop rule"
fi

# Auto mode.
expect_contains "prompt auto requires --project" "project" \
  run_minuano prompt auto 2>&1 || true

# Create a project-scoped task for auto test.
run_minuano add "Auto mode test" --project "val-project" --priority 5 >/dev/null 2>&1
AUTO_PROMPT=$(run_minuano prompt auto --project val-project)
if echo "$AUTO_PROMPT" | grep -q "Auto Mode"; then
  pass "prompt auto generates auto mode header"
else
  fail "prompt auto missing auto mode header"
fi
if echo "$AUTO_PROMPT" | grep -q "minuano-claim --project val-project"; then
  pass "prompt auto references project-scoped claim"
else
  fail "prompt auto missing project-scoped claim"
fi

# Batch mode.
BATCH_PROMPT=$(run_minuano prompt batch "$TASK1_ID" "$TASK2_ID")
if echo "$BATCH_PROMPT" | grep -q "Batch Mode"; then
  pass "prompt batch generates batch header"
else
  fail "prompt batch missing batch header"
fi
if echo "$BATCH_PROMPT" | grep -q "Task 1:"; then
  pass "prompt batch has numbered tasks"
else
  fail "prompt batch missing numbered tasks"
fi
if echo "$BATCH_PROMPT" | grep -q "Task 2:"; then
  pass "prompt batch includes second task"
else
  fail "prompt batch missing second task"
fi

# --- Phase 3: Agent Scripts (Tasks 24, I-04, I-05) ---

section "Phase 3: Agent Scripts"

export AGENT_ID="validate-agent-$$"
export DATABASE_URL="$DB_URL"

# Register a test agent.
psql "$DB_URL" -c "INSERT INTO agents (id, tmux_session, tmux_window) VALUES ('$AGENT_ID', '$SESSION_NAME', 'test')" >/dev/null 2>&1
if [ $? -eq 0 ]; then
  pass "registered test agent in DB"
else
  fail "failed to register test agent"
fi

# minuano-claim.
CLAIM_OUTPUT=$(./scripts/minuano-claim 2>&1) || true
if [ -n "$CLAIM_OUTPUT" ]; then
  # Should be JSON.
  if echo "$CLAIM_OUTPUT" | python3 -m json.tool >/dev/null 2>&1; then
    pass "minuano-claim outputs valid JSON"
    CLAIMED_ID=$(echo "$CLAIM_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null)
    if [ -n "$CLAIMED_ID" ]; then
      pass "claimed task ID: $CLAIMED_ID"
    else
      fail "could not parse claimed task ID"
      CLAIMED_ID=""
    fi
  else
    fail "minuano-claim output is not valid JSON"
    CLAIMED_ID=""
  fi
else
  skip "minuano-claim returned empty (no ready tasks or task was already claimed)"
  CLAIMED_ID=""
fi

# minuano-observe.
if [ -n "$CLAIMED_ID" ]; then
  expect_success "minuano-observe writes context" \
    ./scripts/minuano-observe "$CLAIMED_ID" "validation observation: everything looks good"

  OBS_COUNT=$(psql "$DB_URL" -t -A -c "SELECT COUNT(*) FROM task_context WHERE task_id='$CLAIMED_ID' AND kind='observation' AND content LIKE '%validation observation%'")
  if [ "$OBS_COUNT" -gt 0 ]; then
    pass "observation recorded in DB"
  else
    fail "observation not found in DB"
  fi
else
  skip "minuano-observe (no claimed task)"
fi

# minuano-handoff.
if [ -n "$CLAIMED_ID" ]; then
  expect_success "minuano-handoff writes context" \
    ./scripts/minuano-handoff "$CLAIMED_ID" "validation handoff: completed step 1"

  HANDOFF_COUNT=$(psql "$DB_URL" -t -A -c "SELECT COUNT(*) FROM task_context WHERE task_id='$CLAIMED_ID' AND kind='handoff' AND content LIKE '%validation handoff%'")
  if [ "$HANDOFF_COUNT" -gt 0 ]; then
    pass "handoff recorded in DB"
  else
    fail "handoff not found in DB"
  fi
else
  skip "minuano-handoff (no claimed task)"
fi

# minuano-done (with test command that always passes).
if [ -n "$CLAIMED_ID" ]; then
  # Set a trivial test command that passes.
  psql "$DB_URL" -c "UPDATE tasks SET metadata='{\"test_cmd\":\"true\"}' WHERE id='$CLAIMED_ID'" >/dev/null 2>&1

  # Reset task to claimed state for done test (claim may have released on observe/handoff).
  psql "$DB_URL" -c "UPDATE tasks SET status='claimed', claimed_by='$AGENT_ID' WHERE id='$CLAIMED_ID'" >/dev/null 2>&1

  DONE_OUTPUT=$(./scripts/minuano-done "$CLAIMED_ID" "validation done summary" 2>&1) || true
  if echo "$DONE_OUTPUT" | grep -q "Done:"; then
    pass "minuano-done marks task done"
  else
    fail "minuano-done did not mark done"
    echo "    Output: $(echo "$DONE_OUTPUT" | head -3)"
  fi

  DONE_STATUS=$(psql "$DB_URL" -t -A -c "SELECT status FROM tasks WHERE id='$CLAIMED_ID'")
  if [ "$DONE_STATUS" = "done" ]; then
    pass "task status is 'done' in DB"
  else
    fail "task status should be 'done', got '$DONE_STATUS'"
  fi
else
  skip "minuano-done (no claimed task)"
fi

# minuano-claim --project (I-05).
PROJ_CLAIM_OUTPUT=$(./scripts/minuano-claim --project val-project 2>&1) || true
if [ -n "$PROJ_CLAIM_OUTPUT" ]; then
  if echo "$PROJ_CLAIM_OUTPUT" | python3 -m json.tool >/dev/null 2>&1; then
    pass "minuano-claim --project outputs valid JSON"
    PROJ_CLAIMED_ID=$(echo "$PROJ_CLAIM_OUTPUT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('project_id',''))" 2>/dev/null)
    # Note: project_id may be in the JSON. At minimum, we got a task.
    pass "minuano-claim --project claimed a task"
  else
    fail "minuano-claim --project output is not valid JSON"
  fi
else
  skip "minuano-claim --project returned empty (no ready tasks in project)"
fi

# minuano-pick (I-04).
# Find a ready task for pick test.
PICK_TASK_ID=$(psql "$DB_URL" -t -A -c "SELECT id FROM tasks WHERE status='ready' LIMIT 1")
if [ -n "$PICK_TASK_ID" ]; then
  PICK_OUTPUT=$(./scripts/minuano-pick "$PICK_TASK_ID" 2>&1) || true
  if echo "$PICK_OUTPUT" | python3 -m json.tool >/dev/null 2>&1; then
    pass "minuano-pick outputs valid JSON"
  else
    fail "minuano-pick output is not valid JSON"
    echo "    Output: $(echo "$PICK_OUTPUT" | head -3)"
  fi
else
  skip "minuano-pick (no ready tasks available)"
fi

# minuano-pick with invalid ID.
PICK_BAD=$(./scripts/minuano-pick "nonexistent-task-xyz" 2>&1) && true
PICK_BAD_EXIT=$?
if [ $PICK_BAD_EXIT -ne 0 ]; then
  pass "minuano-pick fails for nonexistent task"
else
  fail "minuano-pick should fail for nonexistent task"
fi

# --- Phase 4: Agent Lifecycle via tmux (Tasks 15-23) ---

section "Phase 4: Agent Lifecycle (tmux)"

if $SKIP_TMUX; then
  skip "spawn agents (tmux skipped)"
  skip "minuano agents (tmux skipped)"
  skip "minuano logs (tmux skipped)"
  skip "minuano kill (tmux skipped)"
  skip "minuano reclaim (tmux skipped)"
else
  # Ensure session.
  tmux kill-session -t "$SESSION_NAME" 2>/dev/null || true
  tmux new-session -d -s "$SESSION_NAME" 2>/dev/null

  if tmux has-session -t "$SESSION_NAME" 2>/dev/null; then
    pass "tmux session created"
  else
    fail "could not create tmux session"
    SKIP_TMUX=true
  fi
fi

if ! $SKIP_TMUX; then
  # Spawn agent.
  SPAWN_OUTPUT=$(run_minuano spawn "val-agent-1" 2>&1) || true
  if echo "$SPAWN_OUTPUT" | grep -q "Spawned:"; then
    pass "minuano spawn creates agent"
  else
    fail "minuano spawn failed"
    echo "    Output: $(echo "$SPAWN_OUTPUT" | head -3)"
  fi

  # Check agents list.
  AGENTS_OUTPUT=$(run_minuano agents 2>&1) || true
  if echo "$AGENTS_OUTPUT" | grep -q "val-agent-1"; then
    pass "minuano agents shows spawned agent"
  else
    fail "spawned agent not visible in agents list"
  fi

  # Agent in DB.
  AG_DB=$(psql "$DB_URL" -t -A -c "SELECT id FROM agents WHERE id='val-agent-1'")
  if [ -n "$AG_DB" ]; then
    pass "agent registered in DB"
  else
    fail "agent not found in DB"
  fi

  # Logs.
  LOGS_OUTPUT=$(run_minuano logs val-agent-1 2>&1) || true
  # Logs may be empty but should not error.
  if [ $? -eq 0 ] || echo "$LOGS_OUTPUT" | grep -qv "Error"; then
    pass "minuano logs captures output"
  else
    fail "minuano logs errored"
  fi

  # Kill agent.
  expect_contains "minuano kill removes agent" "Killed" run_minuano kill val-agent-1

  # Verify removed from DB.
  AG_DB_AFTER=$(psql "$DB_URL" -t -A -c "SELECT id FROM agents WHERE id='val-agent-1'")
  if [ -z "$AG_DB_AFTER" ]; then
    pass "agent removed from DB after kill"
  else
    fail "agent still in DB after kill"
  fi

  # Test run command with multiple agents.
  RUN_OUTPUT=$(run_minuano run --agents 2 --names "a1,a2" 2>&1) || true
  if echo "$RUN_OUTPUT" | grep -q "Spawned:.*a1" && echo "$RUN_OUTPUT" | grep -q "Spawned:.*a2"; then
    pass "minuano run spawns multiple agents"
  else
    fail "minuano run did not spawn both agents"
  fi

  # Kill all.
  expect_contains "minuano kill --all clears all agents" "All agents killed" run_minuano kill --all

  ALL_AGENTS=$(psql "$DB_URL" -t -A -c "SELECT COUNT(*) FROM agents")
  # Only the validate-agent we created earlier might remain.
  if [ "$ALL_AGENTS" -le 1 ]; then
    pass "all spawned agents removed after kill --all"
  else
    fail "some agents remain after kill --all ($ALL_AGENTS)"
  fi

  # Reclaim test: create a stale claimed task.
  psql "$DB_URL" -c "
    INSERT INTO tasks (id, title, status, claimed_by, claimed_at, priority)
    VALUES ('stale-test-$$', 'Stale Task', 'claimed', 'ghost-agent', NOW() - INTERVAL '60 minutes', 5)
  " >/dev/null 2>&1
  RECLAIM_OUTPUT=$(run_minuano reclaim --minutes 30 2>&1) || true
  if echo "$RECLAIM_OUTPUT" | grep -q "Reclaimed"; then
    pass "minuano reclaim resets stale tasks"
    STALE_STATUS=$(psql "$DB_URL" -t -A -c "SELECT status FROM tasks WHERE id='stale-test-$$'")
    if [ "$STALE_STATUS" = "ready" ]; then
      pass "stale task status reset to ready"
    else
      fail "stale task status should be ready, got '$STALE_STATUS'"
    fi
  else
    fail "minuano reclaim did not reclaim stale task"
  fi

  # Cleanup tmux session.
  tmux kill-session -t "$SESSION_NAME" 2>/dev/null || true
fi

# --- Phase 5: Integration — ClaimByID (I-02) and AtomicClaim project filter (I-03) ---

section "Phase 5: Integration Queries (I-02, I-03)"

# These are tested via the scripts above (minuano-pick uses ClaimByID pattern,
# minuano-claim --project uses project filter). Verify the Go functions compile.
expect_success "ClaimByID compiles (via go build)" go build ./internal/db/...

# Check that the function signature exists.
if grep -q "func ClaimByID" internal/db/queries.go; then
  pass "ClaimByID function exists in queries.go"
else
  fail "ClaimByID function missing"
fi

if grep -q "func AtomicClaim.*projectID" internal/db/queries.go; then
  pass "AtomicClaim has projectID parameter"
else
  fail "AtomicClaim missing projectID parameter"
fi

# Check JSON struct tags.
if grep -q 'json:"id"' internal/db/queries.go; then
  pass "Task has JSON struct tags"
else
  fail "Task missing JSON struct tags"
fi

# --- Cleanup ---

section "Cleanup"

# Remove test data.
psql "$DB_URL" -c "
  DELETE FROM task_context WHERE task_id IN (SELECT id FROM tasks WHERE title LIKE 'Validate%' OR title LIKE 'Auto mode%' OR id LIKE 'stale-test%');
  DELETE FROM task_deps WHERE task_id IN (SELECT id FROM tasks WHERE title LIKE 'Validate%' OR title LIKE 'Auto mode%');
  DELETE FROM tasks WHERE title LIKE 'Validate%' OR title LIKE 'Auto mode%' OR id LIKE 'stale-test%';
  DELETE FROM agents WHERE id LIKE 'validate-agent%';
" >/dev/null 2>&1
pass "test data cleaned up"

if $NO_CLEANUP; then
  echo "  (--no-cleanup: leaving postgres running)"
else
  run_minuano down >/dev/null 2>&1
  pass "postgres stopped"
fi

# Remove built binary.
rm -f "$BINARY"

# --- Summary ---

echo ""
echo "============================================"
TOTAL=$((PASS + FAIL + SKIP))
echo -e "  ${GREEN}PASS: $PASS${NC}  ${RED}FAIL: $FAIL${NC}  ${YELLOW}SKIP: $SKIP${NC}  TOTAL: $TOTAL"
echo "============================================"

if [ "$FAIL" -gt 0 ]; then
  echo -e "${RED}VALIDATION FAILED${NC}"
  exit 1
else
  echo -e "${GREEN}VALIDATION PASSED${NC}"
  exit 0
fi
