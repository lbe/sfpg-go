# sfpg-go agent guide

Applies to all agents planning, reviewing, or executing work in this repository.

`sfpg-go` is a self-hosted Go photo gallery using SQLite (`ncruces/go-sqlite3`),
`html/template`, HTMX, Hyperscript, daisyUI, and TailwindCSS. Read `go.mod` for the
current Go version and dependencies.

## Authorization and scope

- Follow explicit user instructions over repository guidance and skill workflows.
- Questions authorize answers and relevant read-only inspection, not changes.
  “Can you implement X?” is a question. Answer it and wait for authorization.
- Imperatives such as “Implement,” “Fix,” “Run,” “Commit,” or “Proceed” authorize
  the requested work and its necessary steps. Continue within that scope without
  asking for approval again. “Continue” preserves the existing authorization.
- A request to create a plan authorizes plan text in the reply, not implementation.
  Write a plan file only when requested. An explicit request to write a document
  authorizes that document.
- When authorization is ambiguous, ask “Want me to implement that?” and wait.
  Skills, tools, and workflows do not authorize additional work.

See [.cursor/rules/approval-before-action.mdc](.cursor/rules/approval-before-action.mdc).

## Engineering and communication

- Understand the affected behavior, code, and dependencies before proposing or
  changing them. Verify uncertain facts; distinguish evidence from assumptions.
- If you don't know, say so. Use available tools to resolve uncertainty; never
  invent facts.
- Working software is the baseline. Deliver clean, idiomatic code that an expert
  human would consider maintainable.
- Follow existing architecture and idiomatic Go. Keep solutions simple; introduce
  interfaces for concrete needs. Avoid speculative abstractions and premature
  optimization, while addressing obvious costs such as heavy allocation in loops.
- Stay within the requested scope. Preserve unrelated work, including existing
  uncommitted changes.
- Keep responses concise and on point. Lead with the answer; include only details
  needed to understand it or act. Omit filler, repeated summaries, and unsolicited
  elaboration. Let the user ask for more detail.
- Keep comments and documentation concise, specific, and appropriate to the
  audience: user guidance in `README.md`, technical detail in
  `docs/ARCHITECTURE.md`.
- Keep learnings in agent memory or session notes when available. Do not create
  repository notes unless asked. Spend time and tokens on useful work.

## Project safeguards

- Never manually edit `version.go`; `scripts/gen_version.sh` manages it through
  `go generate` and Air rebuilds.
- Do not stop, restart, or interfere with `air`. The dev server is at
  `http://localhost:8083`; let Air rebuild after edits. If changes are not reflected,
  check for build failures and notify the user. If required verification needs a
  server that is unavailable, report the blocker.
- No JavaScript in application UI code; use Hyperscript and HTMX. The existing
  `#config-password-validator` in `web/templates/config-modal.html.tmpl` is the
  approved exception. Further exceptions require explicit user approval.
- Do not use Python for scripting unless explicitly instructed. Prefer curl for
  HTTP checks when sufficient. Parse HTML, JSON, and other known formats with a
  parser, not regular expressions.
- Do not change `.gitignore` or force-add ignored files without explicit user
  instruction or approval. A plan alone does not authorize either action.
- Do not bypass commit hooks unless explicitly instructed by the user or explicitly
  required by a user-approved plan. Otherwise stop and notify the user.

## Architecture rules

Read the relevant reference sections before changing the corresponding area;
inspect the implementation and nearby tests for current behavior.

- **Database:** Acquire connections from the existing pool and return them with
  `defer pool.Put(conn)` after successful acquisition. For test databases, reuse
  existing harnesses. `TestUseMemDB: true` requires an empty DSN; a nonempty DSN
  selects file-backed mode. See
  [connection pools](docs/ARCHITECTURE.md#53-connection-pool-design) and
  [dbconnpool](docs/ARCHITECTURE.md#1119-dbconnpool).
- **Handlers and test seams:** Unit tests inject `HandlerQueries` through handler
  groups; routing/integration tests use the server router. Reuse explicit
  `*.testSeams.*` fields; do not introduce `testHook*` fields. See
  [test seams](docs/ARCHITECTURE.md#62-test-seams) and
  [choosing a seam](docs/ARCHITECTURE.md#1345-choosing-a-test-seam).
- **HTMX responses:** Preserve the distinction between `RenderPage` layout
  partials and `RenderTemplate` fragments, including header detection, `Vary`, and
  partial `no-store` behavior. Follow the
  [route contracts](docs/ARCHITECTURE.md#htmx-html-responses) and
  `internal/server/ui/htmx_partial.go`. Return HTTP 200 for validation responses
  HTMX must swap; use `hx-swap-oob="outerHTML"` for OOB updates.
- **Hyperscript:** Use `@attr`, `exists`, `matches`, and `halt the event`. Inside
  `<script type="text/hyperscript">`, use `querySelectorAll('.class')` instead of
  `<.class/>` to avoid Go template escaping. Read the
  [Hyperscript reference](references/hyperscript-reference.md).
- **Configuration:** Preserve defaults → database → YAML → environment → CLI
  precedence. See
  [configuration management](docs/ARCHITECTURE.md#101-configuration-sources--precedence).

## Verification and formatting

- Keep unit, integration, and end-to-end tests distinct in names, locations, and
  build tags. Follow the [TDD process](references/tdd_process.md) for implementation
  work and [test organization](docs/ARCHITECTURE.md#131-test-organization).
- Assert HTML structurally using `internal/testutil`; do not use string/byte
  substring checks on response bodies. For layout partials, use
  `FirstLiteralDocumentShellTag` before `ParseHTML`: the parser synthesizes document
  shell tags. Read the
  [HTML testing methodology](references/methodology-html-content-test-writing.md).
- Run checks appropriate to the change and required by the approved task. Capture
  test output once in `tmp/`, preserve the command's exit status, then inspect the
  saved output. Do not pipe test execution directly into grep or rerun tests merely
  to filter their output differently.
- After Go edits, run `scripts/format-go-changed.sh` with the changed files and
  `go build -o /dev/null .`. The formatter always runs gofmt and limits goimports
  to new files, changed imports, or `FORMAT_GO_IMPORTS=1`.
- Run Prettier only on changed `.html.tmpl`, `.md`, and `.sh` files. Do not run
  whole-tree formatting or whole-package goimports.
- For template edits, run `make validate-templates` (includes Hyperscript
  validation). For Hyperscript changes, also inspect rendered HTML with curl for
  escaped code such as `&amp;lt;`; source validation alone is insufficient. See the
  [escaping guidance](scripts/hyperscript_validation.md#go-template-escaping-detection).
- Report what changed, checks actually run, their results, and remaining blockers.
  Do not claim unrun tests passed.

## Planning and execution

- Plans must specify behavior, concrete file scope, dependencies, verification
  commands, and stop conditions. Each committing task must leave a buildable,
  verifiable tree; do not depend on later tasks to fix unused code or broken tests.
- When executing an approved plan, follow its task order, file whitelist, and
  verification commands. On ambiguity, a missing prerequisite, an unexpected
  failure, or a conflict with actual behavior, stop and report the task and evidence.
  Do not rewrite the plan, weaken assertions, commit, or advance without user
  direction. See
  [.cursor/rules/plan-executor-zero-deviation.mdc](.cursor/rules/plan-executor-zero-deviation.mdc).
- When authoring or executing a plan for the Pi workflow, read
  [the Pi runbook](plans/pi-runbook-sfpg-go.md) for its role boundaries, task format,
  review gates, and test budget. Do not duplicate its full-suite checks before
  every commit.

## Commits

Commit only when authorized by the user or the approved execution plan.

Before committing, read the current version from `version.go`, include it if changed,
and write `tmp/commit_message.txt`. Commit with `git commit -F tmp/commit_message.txt`:

```text
<type>(optional-scope): <imperative summary>

<body>

<actual test status>

Version: <exact value from version.go>
```

The version belongs only in the footer, never in the subject. Preserve normal hooks.

## Additional references

- [Architecture](docs/ARCHITECTURE.md): package map, schema, data flows, middleware,
  concurrency, and testing.
- [README](README.md): user setup and usage.
- [Deployment](DEPLOYMENT.md): runtime operations and proxy checks. For Caddy smoke
  against Air, use `SFPG_BACKEND_PORT=8083` without managing Air.
- [Environment configuration](ENV_CONFIGURATION.md): flags and environment variables.
- [HTMX reference](references/htmx-reference.md): request and swap patterns.
- `web-testsuite/helpers_test.go` and `web-testsuite/auth_test.go`: live-server
  authentication, login rate limits, and restart synchronization. Login is POST-only.
