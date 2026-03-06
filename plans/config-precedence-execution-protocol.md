# Config Precedence Execution Protocol

## Purpose

This protocol enforces deterministic execution for the config precedence workstream.

## Scope Lock

- Execute exactly one planned step at a time.
- Do not begin step N+1 until step N is implemented, verified, and committed.
- If step N fails any gate, pause progression and resolve within step N scope.

## Strict Linear Execution Policy

1. Select one approved step from the plan.
2. Implement only that step's scoped files and tests.
3. Run required verification gates for that step.
4. Commit only that step with a file-based commit message.
5. Report outcomes (files changed, test/build status, blockers).
6. Only then request or use approval for the next step.

No parallel implementation across multiple planned steps is allowed.

## Per-Step Sub-Agent Ownership

- Assign one implementation owner per step.
- Ownership is exclusive while the step is in progress.
- The owner is responsible for:
  - Scope control and no cross-step edits.
  - TDD cycle completion (RED -> GREEN -> REFACTOR).
  - Verification and commit hygiene.
  - Reporting status and blockers.
- Handoff is allowed only after a committed step boundary.

## Mandatory TDD Cycle

For every behavioral change in a step:

1. RED: Add or update tests that fail for the intended behavior.
2. GREEN: Implement minimum code to pass tests.
3. REFACTOR: Improve readability/structure while keeping tests green.

Rules:

- No production behavior change without a preceding failing test when applicable.
- Keep refactors inside the same step and preserve behavior.

## Required Pre-Commit Checks (Per Step)

Run all required checks before committing:

```bash
mkdir -p tmp && go test -tags "integration e2e" ./... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|PASS|ERROR" ./tmp/test_output.txt
```

```bash
go build -o /dev/null .
```

Additional file-type checks:

- Go files changed: run formatting tools required by project policy.
- Template changes: run template and hyperscript validation gates required by project policy.

If any check fails, fix within the current step scope before commit.

## File-Based Commit Workflow

1. Write commit message to `tmp/commit_message.txt`.
2. Use subject line in conventional style appropriate for the step.
3. Include body with:

- Step identifier and concise scope summary.
- Verification artifacts (for example, test output file paths).
- Explicit statement of behavior impact.

4. Commit with:

```bash
git commit -F tmp/commit_message.txt
```

## Reporting Contract (After Each Step)

Provide:

- Changed files.
- Commit hash.
- Test/build verification summary.
- Blockers (if any).
