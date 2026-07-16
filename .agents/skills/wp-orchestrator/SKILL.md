---
name: wp-orchestrator
description: Execute a remediation work package (WP) from a plan file in isolation, verify it, review it independently, and update the plan status only after all gates pass. Enforces the Worktree-Per-WP Execution Model, respects dependencies, and runs main-worktree WPs (Phase 4 templates, WP-48, WP-50) on a feature branch in the main checkout.
---

# WP Orchestrator Skill

## Purpose

Execute a remediation work package (WP) from `tmp/plan-remediation-*.md` in isolation, verify it, review it independently, and update the plan status only after all gates pass.

This skill enforces the **Worktree-Per-WP Execution Model** from the plan:

- Most WPs run in `.worktrees/wp-N` on branch `wp-N-short-desc`.
- **Main-worktree WPs** run on a feature branch in the main checkout (not an isolated worktree): Phase 4 templates (WP-39 … WP-42), dev-server e2e WPs (WP-48, WP-50), and any WP with `**Execute in:** main worktree` in the plan. Detected by `wp_uses_main_worktree()` in `wp-lib.sh`.
- Main is never modified by non-main-worktree WP code.
- Dependencies are respected; independent WPs can run in parallel.

## When to invoke

Invoke this skill when the user says anything like:

```text
Use wp-orchestrator skill. Implement WP-1 from tmp/plan-remediation-2026-07-08.md.
I have reviewed the scope and approve execution.
```

The skill accepts:

- One or more WP identifiers (`WP-1`, `wp-1`, `Wp-1` are all normalized to `WP-1`).
- Explicit plan file path or newest `tmp/plan-remediation-*.md`.
- Phrases like `with dependencies` or `in parallel`.

If the user asks a question rather than requesting implementation, answer directly and do not invoke this skill.

## Workflow overview

1. **Parse** requested WPs from the plan.
2. **Analyze** dependencies and file overlap with `scripts/wp-analyze.sh`.
3. **Check hard dependencies** with `scripts/wp-guard.sh --check-deps`.
4. **Create** worktrees and branches with `scripts/wp-guard.sh --create` (or switch branch in main for main-worktree WPs).
5. **Update status** to `in-progress` with `scripts/wp-plan-update.sh`.
6. **Implement** via coding subagent(s). Coder runs `wp-verify.sh` from main as exit criteria and logs output.
7. **Verify boundary** with `scripts/wp-guard.sh --verify`.
8. **Review** via a read-only review subagent. Reviewer runs `wp-review-auto.sh` from main (diagnostic-only) as part of its analysis and returns `REVIEW: PASS` or corrective instructions.
9. **Fix loop**: if reviewer returns corrective instructions, feed back to coder and repeat from step 6 (max 3 cycles).
10. **Prepare commit** with `scripts/wp-commit-prep.sh` (generates commit message, stages files; no re-verification).
11. **Update plan** to `done` with `scripts/wp-plan-update.sh` only after review passes.
12. **Report** results and remind user to run Tier B on `main` after merge if required.

## Script invocation (mandatory)

Orchestration scripts resolve paths from the **main worktree root**
(`/home/whgi/src2/sfpg-go`). They must be run from there — **not** from inside
`.worktrees/wp-N`.

| Script | Run from main? |
| ------ | -------------- |
| `wp-analyze.sh`, `wp-guard.sh`, `wp-plan-update.sh` | Yes (orchestrator) |
| `wp-verify.sh`, `wp-review-auto.sh` | Yes (coder and reviewer subagents) |
| `wp-commit-prep.sh` | Yes (orchestrator) |

Implementation edits happen only inside `<active-directory>` (the WP worktree or
main checkout for main-worktree WPs). Pass the real plan filename to `--plan` (for example
`tmp/plan-remediation-2026-07-08-closure.md`); do not use shell globs.

## Where does this WP run?

| Detection | Active directory | Branch | Commit path |
| --------- | ---------------- | ------ | ----------- |
| `wp_uses_main_worktree()` false (default) | `/home/whgi/src2/sfpg-go/.worktrees/wp-N` | `wp-N-short-desc` | `bash tmp/commit-all-worktrees.sh` |
| `wp_uses_main_worktree()` true | `/home/whgi/src2/sfpg-go` (main checkout) | `wp-N-short-desc` | Commit separately from main checkout |

Run `wp-analyze.sh` to see routing per WP (`main worktree` vs `.worktrees/wp-N`).

Main-worktree WPs that touch `web-testsuite/` or Playwright require `air` on `localhost:8083` during verify.

## Hard rules

- Do not edit files in the main worktree for non-main-worktree WPs.
- **Do not modify** `.agents/skills/wp-orchestrator/scripts/*` during implementation WPs. Orchestrator script fixes require a dedicated tooling commit with explicit user approval.
- If `wp-verify.sh` fails, fix the WP code or report the script bug. **Never** PATH-hack, wrapper scripts, or patch orchestrator scripts from a worktree to pass verification.
- **Never** add global `--build-tags` to `golangci-lint run` for all WPs. E2eweb verification uses the existing dev-server block in `wp-verify.sh` only when `web-testsuite/` or Playwright files change.
- **All code changes happen in coder subagents.** The orchestrator never edits WP implementation code.
- The coder subagent must run `wp-verify.sh` from `/home/whgi/src2/sfpg-go` and log output before returning. The coder is not done until `wp-verify.sh` exits 0.
- Do not mark a WP `done` without passing `wp-guard --verify` and the independent subagent review.
- Do not start a WP until `wp-guard --check-deps` passes.
- Review subagents are **read-only**. They must not edit any files. They produce corrective instructions, not code changes.
- Do not remove worktrees or branches without user approval.

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

## Detailed steps

### 1. Parse and analyze

```bash
./.agents/skills/wp-orchestrator/scripts/wp-analyze.sh --plan tmp/plan-remediation-2026-07-08.md wp-1 wp-3 wp-5
```

Outputs:

- Topologically sorted execution order.
- Parallel execution waves.
- File-overlap report.
- Main-worktree vs isolated worktree routing.

If the user asked `with dependencies`, include dependencies in the execution set. If file overlap is detected and parallel execution was requested, fall back to sequential.

### 2. Check dependencies

For each WP:

```bash
./.agents/skills/wp-orchestrator/scripts/wp-guard.sh --plan tmp/plan-remediation-2026-07-08.md --check-deps WP-N
```

Stop and report blockers if this fails. Dependencies with status `on hold` are not treated as blockers.

### 3. Create worktrees

For most WPs:

```bash
./.agents/skills/wp-orchestrator/scripts/wp-guard.sh --plan tmp/plan-remediation-2026-07-08.md --create wp-1 wp-3 wp-5
```

This:

- Verifies you are on branch `main` and the main worktree is clean except for `version.go`.
- Creates branch `wp-N-short-desc` from `main`.
- Creates worktree `.worktrees/wp-N`.
- Initializes git submodules in the worktree.

For **main-worktree WPs** (`wp_uses_main_worktree`: WP-39 … WP-42, WP-48, WP-50, or plan `Execute in: main worktree`), the script creates the branch from `main` and checks it out in the main checkout. **Do not** run `git worktree add` for these WPs.

Use `--dry-run` to preview actions without making changes.

### 4. Update status to in-progress

```bash
./.agents/skills/wp-orchestrator/scripts/wp-plan-update.sh --plan tmp/plan-remediation-2026-07-08.md WP-1 in-progress
```

### 5. Implement via coding subagent

Spawn one coding subagent per WP using the platform's delegation tool (Pi: `subagent` with `agent: 'worker'` — see [agent-mapping.md](agent-mapping.md)). The tool does **not** accept a `cwd` parameter; include the active directory explicitly in the prompt and use absolute paths for all file references.

**For worktree WPs:**

- Active directory: `/home/whgi/src2/sfpg-go/.worktrees/wp-N`
- Branch: `wp-N-short-desc`

**For main-worktree WPs** (Phase 4 templates, WP-48, WP-50, or `Execute in: main worktree`):

- Active directory: `/home/whgi/src2/sfpg-go` (main checkout)
- Branch: `wp-N-short-desc`
- `air` on `localhost:8083` required for e2eweb/Playwright verify

**Cycle 1 prompt template:**

> You are in the WP-N worktree at `<active-directory>`. Implement WP-N from
> `<plan-file>` (`/home/whgi/src2/sfpg-go/tmp/`).
> Use only absolute paths under `<active-directory>`. Do not touch files outside `<active-directory>`.
> Follow repo Go conventions and `AGENTS.md`.
> Make minimal, focused changes.
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
> For main-worktree WPs, write the log to `/home/whgi/src2/sfpg-go/tmp/verify-output-WP-N.log`.
>
> The script MUST exit 0. If it fails, fix the issues and re-run until it passes.
> The log file is your proof that verification succeeded.

**Cycle 2+ prompt (corrective instructions from reviewer):**

> You are in the WP-N worktree at `<active-directory>`. Fix the WP-N implementation based on the review feedback below. Use only absolute paths under `<active-directory>`. Do not introduce unrelated changes.
>
> BEFORE FINISHING, run `wp-verify.sh` from `/home/whgi/src2/sfpg-go` (same command
> as cycle 1, with log path `.../tmp/verify-output-WP-N-cycle-N.log`) and ensure
> it passes.
>
> Review feedback:
> ...

### 6. Verify boundary

```bash
./.agents/skills/wp-orchestrator/scripts/wp-guard.sh --plan tmp/plan-remediation-2026-07-08.md --verify WP-1
```

If this detects worktree boundary violations, fail immediately and report the offending files. Do not auto-revert; the user must clean main.

For **worktree-only** `--verify`, main must be clean (except `version.go`) only when main is still on branch `main`. If main is on a feature branch (parallel main-worktree WP), worktree boundary checks still run but main dirtiness is not an error.

### 7. Review (read-only subagent)

Spawn a fresh review subagent using the platform's delegation tool (Pi: `subagent` with `agent: 'reviewer'` — see [agent-mapping.md](agent-mapping.md)). The tool does **not** accept a `cwd` parameter; include the active directory explicitly in the prompt and use absolute paths.

The reviewer is **read-only** — it must not edit any files.

**Reviewer prompt template:**

> You are a senior Go code reviewer reviewing WP-N in `<active-directory>`. Use only absolute paths under `<active-directory>`. **You are READ-ONLY. Do not edit any files.**
>
> Read the WP requirements from `<plan-file>` in `/home/whgi/src2/sfpg-go/tmp/`.
> Inspect changed files with `git -C <active-directory> diff`.
>
> Run diagnostic checks from the **main worktree root**:
>
> ```bash
> cd /home/whgi/src2/sfpg-go
> ./.agents/skills/wp-orchestrator/scripts/wp-review-auto.sh \
>   --plan <plan-file> WP-N
> ```
>
> Evaluate:
>
> - Requirements coverage (is every requirement addressed?)
> - Go style and repo conventions (per AGENTS.md)
> - Test quality and coverage
> - **Phase 2 gate WPs (WP-16 and WP-51 … WP-54):** confirm the read-only structural gate run by `wp-review-auto.sh` passes, then inspect the metrics, moves, and coverage evidence generated by `wp-verify.sh`. Reject tag-only and rename-in-place changes. Reject moving a `package server` test that uses unexported symbols into an unrelated subdirectory. Relocation must target the production owner, use black-box exported APIs in an independent test package, or move production and tests together. Require **≤1** uncovered server function in both default and integration profiles.
> - **Wrapper orphan check:** if tests for `batched_write`, `batcher_wiring`, or `auth_service` moved out of `internal/server/`, verify `internal/server/` production symbols still have exercising tests.
> - Dead code (unused functions, variables, types, imports, test helpers — flag any that should be removed)
> - Security (input validation, auth, data exposure)
> - Minimal scope (are changes confined to what the WP asks for?)
> - Correct file locations (worktree isolation, no main-tree leaks)
>
> Output exactly one of the following as the first line:
>
> ```
> REVIEW: PASS
> ```
>
> or corrective instructions:
>
> ```
> REVIEW: FAIL
> - path/to/file.go:42 — specific issue, what's wrong, how to fix
> - path/to/file_test.go:15 — specific issue, what's wrong, how to fix
> ```

The first line MUST be exactly "REVIEW: PASS" or "REVIEW: FAIL".

Save the review output to `tmp/wp-N-review-cycle-{n}.md` in the main worktree.

### 8. Fix loop

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

The orchestrator may use `wp-review-parse.sh` to machine-parse the reviewer output, or parse the first line directly.

### 9. Prepare commit

After review passes, run `wp-commit-prep.sh`. This script generates `tmp/commit_message.txt` and stages changed files. It does **not** re-run verification — the last coder run already passed `wp-verify.sh` and the reviewer confirmed the state is clean.

```bash
./.agents/skills/wp-orchestrator/scripts/wp-commit-prep.sh --plan tmp/plan-remediation-2026-07-08.md WP-1
```

The user must run `bash tmp/commit-all-worktrees.sh` from main to commit **isolated worktree** WPs. That script also runs `wp-verify.sh` before each commit as a final guard. **Main-worktree WPs** (Phase 4, WP-48, WP-50) are committed separately from the main checkout, not via `commit-all-worktrees.sh`.

### 10. Mark done

Only after review passes and commit is prepared:

```bash
./.agents/skills/wp-orchestrator/scripts/wp-plan-update.sh --plan tmp/plan-remediation-2026-07-08.md WP-1 done
```

For parallel WPs, use sidecar files (`tmp/wp-N-status.md`) and merge them at the end. Direct concurrent edits to the plan file are not allowed.

### 11. Tier B reminder

If the WP requires Tier B (e2e/browser-affecting), remind the user:

> WP-N is Tier-A done. After merging to `main`, run `make test-all` and `make test-browser` with `air` on `localhost:8083`. The WP is not fully done until Tier B passes.

## Parallel execution

Parallel execution is allowed only when:

1. `wp-analyze.sh` reports no hard dependency between the WPs.
2. `wp-guard.sh --overlap` reports no shared production/test files.
3. Each WP has its own worktree/branch (or main-checkout feature branch for main-worktree WPs).

Worktree + worktree parallel (e.g. WP-22 + WP-47) is safe when file sets do not overlap. Worktree + main-worktree parallel (e.g. WP-21 + WP-48) is safe when file sets do not overlap; `wp-guard --verify` for worktree WPs does not require a clean main checkout if main is on another feature branch.

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

The orchestrator tracks per-WP state across each phase boundary:

| WP   | State     | Cycle |
| ---- | --------- | ----- |
| WP-1 | reviewing | 1     |
| WP-3 | reviewing | 1     |
| WP-5 | done      | 1     |

States: `coding`, `verifying-boundary`, `reviewing`, `done`, `failed`.

## Failure handling

| Condition                                   | Action                                                 |
| ------------------------------------------- | ------------------------------------------------------ |
| Dependency not `done`                       | Stop; report blockers.                                 |
| File overlap in parallel request            | Fall back to sequential.                               |
| Main worktree modified outside allowed area | Fail WP; report offending files; do not auto-revert.   |
| Coder `wp-verify.sh` fails                  | Coder must fix and re-run before returning.            |
| Reviewer returns corrective instructions    | Feed back to coder (max 3 cycles).                     |
| Review fails after 3 cycles                 | Stop; status stays `in-progress`; report final review. |

## Files in this skill

- `SKILL.md` — this file.
- `PROCESS.md` — full process documentation.
- `scripts/wp-lib.sh` — shared functions.
- `scripts/wp-guard.sh` — worktree create/verify, deps, overlap.
- `scripts/wp-analyze.sh` — dependency order and parallel-group analysis.
- `scripts/wp-plan-update.sh` — locked plan status updates.
- `agent-mapping.md` — platform-specific subagent name mappings.
- `scripts/wp-verify.sh` — Tier A verification.
- `scripts/wp-phase2-gate.sh` — Phase 2 structural, metric, and coverage gates.
- `scripts/wp-review-auto.sh` — read-only diagnostic checks (style, lint, scope).
- `scripts/wp-review-parse.sh` — machine review gate.
- `scripts/wp-commit-prep.sh` — commit message and staging.

## Notes

- `tmp/plan-remediation-*.md` is gitignored; plan updates happen in the main worktree.
- `version.go` is auto-generated and ignored by boundary checks.
- Never run `make test-all` from inside a worktree; Tier B is post-merge on `main`.
- Never run `wp-verify.sh` or `wp-review-auto.sh` from inside `.worktrees/wp-N`; always `cd` to `/home/whgi/src2/sfpg-go` first.
- The skill is the mandatory execution path; the manual plan instructions describe what it automates.
