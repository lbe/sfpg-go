# WP Orchestrator Process Documentation

## Overview

The `wp-orchestrator` skill executes remediation work packages (WPs) from `tmp/plan-remediation-*.md` in isolated git worktrees, verifies them, subjects them to independent review, and updates the plan status only after all gates pass.

This document describes the process for both the orchestrating agent and human reviewers.

## Goals

1. **Never modify `main` directly** for non-Phase-4 WP implementation work.
2. **Respect dependencies** and execute WPs in topological order.
3. **Allow safe parallelism** only when WPs are independent and touch disjoint files.
4. **Guarantee review** by a subagent that did not write the code.
5. **Update the plan** atomically and only after the code passes review.

## Directory layout

```text
.agents/skills/wp-orchestrator/
├── SKILL.md
├── PROCESS.md
└── scripts/
    ├── wp-lib.sh              # Shared functions
    ├── wp-guard.sh            # Worktree create/verify, deps, overlap
    ├── wp-analyze.sh          # Dependency order and parallel groups
    ├── wp-plan-update.sh      # Locked plan status updates
    ├── wp-verify.sh           # Tier A verification
    ├── wp-phase2-gate.sh      # Phase 2 metrics and coverage gates
    ├── wp-review-auto.sh      # Read-only diagnostic checks
    ├── wp-review-parse.sh     # Machine review gate
    └── wp-commit-prep.sh      # Commit message and staging
```

At runtime, the skill creates:

```text
.worktrees/wp-N/                  # Git worktree for non-Phase-4 WPs
.worktrees/wp-N/tmp/commit_message.txt
.worktrees/wp-N/tmp/test_output.txt
.worktrees/wp-N/tmp/wp-N-metrics.txt
.worktrees/wp-N/tmp/wp-N-moves.txt
.worktrees/wp-N/tmp/wp-N-cover-diff.txt
tmp/.plan-remediation.lock        # File lock for plan updates
tmp/wp-N-review-cycle-1.md        # Saved review reports
tmp/wp-N-status.md                # Sidecar status for parallel execution
```

Phase 4 WPs use the main worktree instead of `.worktrees/wp-N`.

## Script invocation (mandatory)

All orchestration scripts resolve paths from the **main worktree root**
(`/home/whgi/src2/sfpg-go`). Run them from there — **not** from inside
`.worktrees/wp-N`.

- **Orchestrator** runs `wp-analyze.sh`, `wp-guard.sh`, `wp-plan-update.sh`,
  and `wp-commit-prep.sh` from main.
- **Coder and reviewer subagents** run `wp-verify.sh` and `wp-review-auto.sh`
  from main after `cd /home/whgi/src2/sfpg-go`.
- Pass the real plan path to `--plan` (for example
  `tmp/plan-remediation-2026-07-08-closure.md`); do not use shell globs.
- Verification logs for worktree WPs go under
  `/home/whgi/src2/sfpg-go/.worktrees/wp-N/tmp/`.

Implementation edits still happen only inside `<active-directory>`.

## State machine

The plan file recognizes these statuses:

- `pending`
- `in-progress`
- `done`
- `on hold`

A WP is considered **Tier-A done** when it reaches `done`. It is **fully done** for e2e-affecting changes only after Tier B passes on `main`.

## Agent System Mapping

This skill delegates work to platform-specific subagents. Which subagent name
to use for each role depends on the agent platform running the orchestrator.

**Before spawning any subagent, load [agent-mapping.md](agent-mapping.md) and
look up the Coder and Reviewer entries for your platform.** Use the names and
invocation syntax specified there.

For Pi, the delegation tool is `subagent` with `agentScope: "user"` (or `"both"`
if project-local agents exist). The default mapping is:
- Coder → `agent: "worker"`
- Reviewer → `agent: "reviewer"`

## Step-by-step process

### 1. Request parsing

Recognized forms:

- `Use wp-orchestrator skill. Implement WP-1 from tmp/plan-remediation-2026-07-08.md.`
- `Use wp-orchestrator skill. Implement WP-1, WP-3, and WP-5 in parallel from tmp/plan-remediation-2026-07-08.md.`
- `Use wp-orchestrator skill. Implement WP-19 with dependencies from tmp/plan-remediation-2026-07-08.md.`

Extract WP identifiers, plan file path, dependency expansion flag, and parallel flag.

### 2. Analysis

Run:

```bash
scripts/wp-analyze.sh --plan FILE wp-1 ...
```

Outputs:

- Topologically sorted order.
- Parallel execution waves.
- File-overlap report.
- Phase 4 detection.

If the user requested `with dependencies`, add dependencies to the execution set. If parallel execution is requested but overlap exists, fall back to sequential.

### 3. Dependency check

For each WP:

```bash
scripts/wp-guard.sh --plan FILE --check-deps WP-N
```

Hard dependencies must be `done`. `on hold` dependencies are ignored. Stop and report blockers on failure.

### 4. Worktree creation

For most WPs:

```bash
scripts/wp-guard.sh --plan FILE --create WP-1 WP-3 WP-5
```

This:

- Verifies you are on branch `main` and the main worktree is clean except for `version.go`.
- Creates branch `wp-N-short-desc` from `main`.
- Creates worktree `.worktrees/wp-N`.
- Initializes git submodules in the worktree.

For Phase 4 WPs, the script creates the branch and checks it out in the main worktree instead.

Use `--dry-run` to preview without changing state.

### 5. Status update

```bash
scripts/wp-plan-update.sh --plan FILE WP-N in-progress
```

### 6. Implementation by coding subagent

Spawn one coding subagent per WP using the platform's delegation tool (Pi: `subagent` with `agent: 'worker'` — see [agent-mapping.md](agent-mapping.md)). The tool does **not** accept a `cwd` parameter; include the active directory explicitly in the prompt and use absolute paths for all file references.

**Worktree WP:**

- Active directory: `/home/whgi/src2/sfpg-go/.worktrees/wp-N`
- Branch: `wp-N-short-desc`

**Phase 4 WP:**

- Active directory: `/home/whgi/src2/sfpg-go` (main worktree root)
- Branch: `wp-N-short-desc`

**Cycle 1 prompt:**

> You are in the WP-N worktree at `<active-directory>`. Implement WP-N from
> `<plan-file>` (`/home/whgi/src2/sfpg-go/tmp/`). Use only absolute paths under
> `<active-directory>`. Follow repo conventions and `AGENTS.md`. Make minimal
> changes. Do not touch files outside `<active-directory>`.
>
> BEFORE FINISHING, run verification from the **main worktree root** (not from
> `<active-directory>`):
>
> ```bash
> cd /home/whgi/src2/sfpg-go
> ./.agents/skills/wp-orchestrator/scripts/wp-verify.sh \
>   --plan <plan-file> WP-N \
>   > /home/whgi/src2/sfpg-go/.worktrees/wp-N/tmp/verify-output-WP-N.log 2>&1
> ```
>
> For Phase 4 WPs, write the log to `/home/whgi/src2/sfpg-go/tmp/verify-output-WP-N.log`.
>
> The script MUST exit 0. If it fails, fix the issues and re-run until it passes.
> The log file is your proof that verification succeeded.

**Cycle 2+ prompt:**

> You are in the WP-N worktree at `<active-directory>`. Fix WP-N based on this review feedback. Use only absolute paths under `<active-directory>`. Do not introduce unrelated changes.
>
> BEFORE FINISHING, run `wp-verify.sh` from `/home/whgi/src2/sfpg-go` (same command
> as cycle 1, with log path `.../tmp/verify-output-WP-N-cycle-N.log`) and ensure
> it passes.
>
> Feedback: ...

### 7. Boundary verification

```bash
scripts/wp-guard.sh --plan FILE --verify WP-N
```

Confirms:

- Main worktree has no unexpected tracked changes.
- Worktree exists and is on the correct branch (or main is on the correct branch for Phase 4).

If main is dirty outside allowed areas, fail and report. **Never auto-revert.** The user must clean main.

### 8. Review (read-only subagent)

Spawn a fresh review subagent using the platform's delegation tool (Pi: `subagent` with `agent: 'reviewer'` — see [agent-mapping.md](agent-mapping.md)). The tool does **not** accept a `cwd` parameter; include `<active-directory>` explicitly in the prompt and use absolute paths.

The reviewer is **read-only** — it must not edit any files. It produces corrective instructions, not code changes.

The review subagent:

- Reads WP-N requirements from the plan.
- Inspects changed files via `git -C <active-directory> diff`.
- Runs `wp-review-auto.sh` from `/home/whgi/src2/sfpg-go` (read-only and
  diagnostic-only) as part of its analysis:
  `cd /home/whgi/src2/sfpg-go && ./.agents/skills/wp-orchestrator/scripts/wp-review-auto.sh --plan <plan-file> WP-N`
- For WP-16 and WP-51 … WP-54, verifies package ownership, rejects tag/rename-only changes and unrelated-package moves, inspects the evidence generated by `wp-verify.sh`, and requires the automated ≤1 uncovered-function gate to pass.
- Evaluates requirements coverage, style, tests, security, scope, and file locations.
- **Does not edit files.**
- Outputs `REVIEW: PASS` or `REVIEW: FAIL` with specific, actionable corrective instructions as the first line.

Save output to `tmp/wp-N-review-cycle-{n}.md`.

The orchestrator may use `wp-review-parse.sh` to machine-parse the reviewer output, or parse the first line directly.

### 9. Fix loop

The orchestrator runs a unified coder↔reviewer loop (max 3 cycles):

```
cycle = 1
while cycle <= 3:
    if cycle > 1:
        spawn coder subagent with corrective instructions
        coder implements fixes, runs wp-verify.sh, logs output
    run wp-guard --verify
    spawn reviewer subagent (read-only)
    save reviewer output to tmp/wp-N-review-cycle-{cycle}.md
    if reviewer output starts with "REVIEW: PASS":
        break
    cycle += 1

if cycle > 3:
    stop, report final review, status stays in-progress
```

### 10. Commit preparation

After review passes, run `wp-commit-prep.sh`. This script generates `tmp/commit_message.txt` and stages changed files. It does **not** re-run verification — the last coder run already passed `wp-verify.sh` and the reviewer confirmed the state is clean.

```bash
scripts/wp-commit-prep.sh --plan FILE WP-N
```

The user then runs `bash tmp/commit-all-worktrees.sh` from main for worktree WPs; that script also runs `wp-verify.sh` before each commit as a final guard. Phase 4 WPs are committed separately in main.

### 11. Mark done

Only after review passes:

```bash
scripts/wp-plan-update.sh --plan FILE WP-N done
```

For parallel WPs, write sidecar `tmp/wp-N-status.md` and merge at the end. Use the file lock in `wp-plan-update.sh` for any direct plan updates.

### 12. Tier B reminder

For e2e-affecting WPs, remind the user to run Tier B on `main` after merge:

```bash
make test-all > ./tmp/test_output.txt 2>&1
make test-browser
```

The WP is not fully done until Tier B passes.

## Parallel execution

Parallel execution is gated by:

1. No hard dependencies between WPs.
2. No file overlap (markdown tables and git diff).
3. Each WP in its own worktree/branch (or main branch for Phase 4).

### Wave-level batching

Pi's `subagent` tool runs concurrent tasks via the `tasks` array but blocks
until *all* tasks complete. True per-WP interleaving (review WP-1 while WP-3
is still coding) is not supported. Instead, batch same-phase work into parallel
`subagent` calls at each phase boundary.

For a wave of independent WPs (e.g., WP-1, WP-3, WP-5):

**1. Code phase** — launch all coders in one parallel batch:

```text
subagent(agentScope: "user", tasks: [
  { agent: "worker", task: "<coder cycle-1 prompt for WP-1>" },
  { agent: "worker", task: "<coder cycle-1 prompt for WP-3>" },
  { agent: "worker", task: "<coder cycle-1 prompt for WP-5>" },
])
```

All coders run concurrently. The call returns when every coder finishes.

**2. Verify boundary** — run `wp-guard --verify` for each WP whose coder passed.

**3. Review phase** — launch all reviewers in one parallel batch:

```text
subagent(agentScope: "user", tasks: [
  { agent: "reviewer", task: "<reviewer prompt for WP-1>" },
  { agent: "reviewer", task: "<reviewer prompt for WP-3>" },
  { agent: "reviewer", task: "<reviewer prompt for WP-5>" },
])
```

**4. Retry phase** — for WPs whose reviewer returned corrective instructions,
launch fix-coders in another parallel batch (same coder prompt template with
the reviewer's feedback appended). Max 3 cycles per WP.

**5. Done phase** — run `wp-commit-prep` and mark `done` for WPs that passed review.

If file overlap is detected, fall back to fully sequential execution.

The parent merges sidecar status files into the plan at the end.

### Per-WP state tracking

The orchestrator tracks each WP's progress across phase boundaries:

| WP   | State     | Cycle |
| ---- | --------- | ----- |
| WP-1 | reviewing | 1     |
| WP-3 | reviewing | 1     |
| WP-5 | done      | 1     |

States: `coding`, `verifying-boundary`, `reviewing`, `done`, `failed`.

All WPs share the same phase at any moment — all code together, all review
together. Individual WPs exit a phase early if they fail or reach done.

## Failure handling

| Failure                                     | Result                                                 |
| ------------------------------------------- | ------------------------------------------------------ |
| Dependency not `done`                       | Stop; report blockers.                                 |
| File overlap in parallel request            | Fall back to sequential.                               |
| Main worktree modified outside allowed area | Fail WP; report; do not auto-revert.                   |
| Coder `wp-verify.sh` fails                  | Coder must fix and re-run before returning.            |
| Reviewer returns corrective instructions    | Feed back to coder (max 3 cycles).                     |
| Review fails after 3 cycles                 | Stop; status stays `in-progress`; report final review. |

## Responsibilities

### Orchestrator agent

- Parse the request.
- Run analysis and guard scripts.
- Spawn and coordinate coding and review subagents. Never edits code.
- Update plan status only after all gates pass.
- Report final results and Tier B reminders.

### Coding subagent

- Implement WP requirements.
- Follow repo style and conventions.
- Keep changes minimal.
- **Run `wp-verify.sh` from `/home/whgi/src2/sfpg-go` as exit criteria and log output.** The coder is not done until it passes.
- Fix review feedback.

### Review subagent

- Critically evaluate the implementation.
- Run `wp-review-auto.sh` from `/home/whgi/src2/sfpg-go` (read-only and diagnostic-only) as part of analysis.
- Check style, tests, security, and scope.
- **Read-only: never edit files.**
- Produce corrective instructions (not code changes) when issues are found.

### Human user

- Approve WP execution once per batch.
- Review final reports and failed-review output.
- Run `tmp/commit-all-worktrees.sh` and merge.
- Run Tier B on `main` for e2e-affecting WPs.

## Integration with the broader plan

The plan file `tmp/plan-remediation-*.md` is the source of truth for WP status. It is gitignored and lives in the main worktree. The skill updates it in place.

Worktree branches (`wp-N-short-desc`) contain the actual code changes. They are merged into `main` through the normal project workflow after the WP is marked `done`.

## Notes for reviewers

- The skill is intentionally mechanical. It delegates interpretation to the coding subagent.
- The review subagent acts as a quality gate, not a co-author.
- Guard scripts are plain bash so they can be reviewed and tested independently.
- `version.go` is ignored by boundary checks because it is auto-generated.
- Main-dirty failures are never auto-reverted; the user must clean main.
