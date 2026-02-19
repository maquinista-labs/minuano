#!/usr/bin/env bash
# Test that all agent scripts are correctly set up.
set -euo pipefail

SCRIPTS_DIR="$(cd "$(dirname "$0")" && pwd)"
PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

echo "=== Agent Scripts Tests ==="

# Test: all scripts exist
for script in minuano-claim minuano-done minuano-observe minuano-handoff minuano-pick; do
    if [ -f "$SCRIPTS_DIR/$script" ]; then
        pass "$script exists"
    else
        fail "$script does not exist"
    fi
done

# Test: all scripts are executable
for script in minuano-claim minuano-done minuano-observe minuano-handoff minuano-pick; do
    if [ -x "$SCRIPTS_DIR/$script" ]; then
        pass "$script is executable"
    else
        fail "$script is not executable"
    fi
done

# Test: all scripts have correct shebang
for script in minuano-claim minuano-done minuano-observe minuano-handoff minuano-pick; do
    if head -1 "$SCRIPTS_DIR/$script" | grep -q '^#!/usr/bin/env bash'; then
        pass "$script has correct shebang"
    else
        fail "$script has wrong shebang"
    fi
done

# Test: all scripts use set -euo pipefail
for script in minuano-claim minuano-done minuano-observe minuano-handoff minuano-pick; do
    if grep -q 'set -euo pipefail' "$SCRIPTS_DIR/$script"; then
        pass "$script uses strict mode"
    else
        fail "$script missing strict mode"
    fi
done

# Test: claim script requires AGENT_ID
if grep -q 'AGENT_ID' "$SCRIPTS_DIR/minuano-claim"; then
    pass "claim checks AGENT_ID"
else
    fail "claim missing AGENT_ID check"
fi

# Test: claim script uses FOR UPDATE SKIP LOCKED
if grep -q 'FOR UPDATE SKIP LOCKED' "$SCRIPTS_DIR/minuano-claim"; then
    pass "claim uses FOR UPDATE SKIP LOCKED"
else
    fail "claim missing FOR UPDATE SKIP LOCKED"
fi

# Test: done script requires task ID and summary
if grep -q 'TASK_ID.*Usage:' "$SCRIPTS_DIR/minuano-done"; then
    pass "done requires task ID"
else
    fail "done missing task ID requirement"
fi

# Test: observe and handoff use quote_literal for SQL safety
for script in minuano-observe minuano-handoff; do
    if grep -q 'quote_literal' "$SCRIPTS_DIR/$script"; then
        pass "$script uses quote_literal"
    else
        fail "$script missing quote_literal"
    fi
done

# Test: done handles both pass and fail cases
if grep -q 'EXIT_CODE' "$SCRIPTS_DIR/minuano-done"; then
    pass "done checks exit code"
else
    fail "done missing exit code check"
fi

# Test: claim script supports --project flag
if grep -q '\-\-project' "$SCRIPTS_DIR/minuano-claim"; then
    pass "claim supports --project flag"
else
    fail "claim missing --project support"
fi

# Test: claim script uses PROJECT_FILTER
if grep -q 'PROJECT_FILTER' "$SCRIPTS_DIR/minuano-claim"; then
    pass "claim uses PROJECT_FILTER variable"
else
    fail "claim missing PROJECT_FILTER"
fi

# Test: pick script requires task ID
if grep -q 'TASK_ID.*Usage:' "$SCRIPTS_DIR/minuano-pick"; then
    pass "pick requires task ID"
else
    fail "pick missing task ID requirement"
fi

# Test: pick script checks AGENT_ID
if grep -q 'AGENT_ID' "$SCRIPTS_DIR/minuano-pick"; then
    pass "pick checks AGENT_ID"
else
    fail "pick missing AGENT_ID check"
fi

# Test: pick uses row_to_json (same output format as claim)
if grep -q 'row_to_json' "$SCRIPTS_DIR/minuano-pick"; then
    pass "pick uses row_to_json"
else
    fail "pick missing row_to_json"
fi

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
