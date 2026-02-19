# Agent Task Loop

You are an autonomous coding agent. Work through the task queue without waiting
for human input. Exit cleanly when the queue is empty.

## Setup

Scripts are in ./scripts/ relative to this file. Add to PATH for this session:
export PATH="$PATH:$(cd "$(dirname "$0")/.." && pwd)/scripts"

Your agent ID is in $AGENT_ID. Your database is in $DATABASE_URL.

## Loop

1. **Claim**: Run `minuano-claim`
   - Prints JSON: your task spec + context array.
   - No output: queue empty or nothing ready. Exit cleanly.

2. **Read context** from the JSON:
   - `body`: your complete specification
   - `context[].kind == "inherited"`: findings from dependency tasks — read these first
   - `context[].kind == "handoff"`: where a previous attempt left off on this task
   - `context[].kind == "test_failure"`: what broke last time — fix exactly this

3. **Write observations** as you discover things:
   `minuano-observe <id> "auth middleware expects Bearer not X-Auth-Token"`

4. **Write a handoff** before any long operation or risky change:
   `minuano-handoff <id> "completed token generation at src/auth/token.go, next: refresh endpoint"`

5. **Submit**: When you believe the work is complete:
   `minuano-done <id> "brief summary of what was built"`
   - Tests pass → task marked done, loop back to step 1
   - Tests fail → failure written to context, task reset to ready, loop back to step 1
     (you or another agent will fix it with the failure logs as context)

## Rules

- Never mark a task done without calling `minuano-done`. It runs the tests.
- If you see a `test_failure` context entry: fix only what broke. Do not rewrite unrelated code.
- If after reading failure logs you genuinely cannot determine the fix, write a detailed
  handoff note explaining what you tried, then call `minuano-done` anyway to record the
  failure cleanly. A human will triage tasks that reach max_attempts.
- One task per outer loop iteration. Exit after `minuano-done` returns success.
- Do not ask for clarification. Make a reasonable interpretation, note it as an observation,
  proceed.
