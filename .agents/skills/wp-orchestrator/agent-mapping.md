# Agent System Mapping

This skill delegates implementation and review to platform-specific subagents.
Load this file, find your agent platform, and use the listed subagent names when
spawning coders and reviewers.

## Pi

Pi's delegation tool: `subagent`

| Role     | Subagent Name | agentScope | Notes |
|----------|---------------|------------|-------|
| Coder    | `worker`      | `user`     | General-purpose agent with full tool access |
| Reviewer | `reviewer`    | `user`     | Code review specialist with read-only tools |

**Parallel batch syntax:**

```text
subagent(tasks: [
  { agent: "worker",   task: "<coder prompt for WP-1>" },
  { agent: "worker",   task: "<coder prompt for WP-3>" },
  ...
])
```

**Limitation:** `tasks` array runs all subagents concurrently but blocks until
*all* complete. True per-WP interleaving (review WP-1 while WP-3 is still
coding) is not supported by current Pi tooling. Use wave-level batching
instead: all coders in parallel → all reviewers in parallel → retry batch.

## Claude Code

| Role     | Subagent Name | Notes |
|----------|---------------|-------|
| Coder    | TBD           | Add Claude Code subagent names when available |
| Reviewer | TBD           | |

## Extending

To add a new platform, append a section with the platform name as a heading
and a table mapping Coder and Reviewer roles to the platform's subagent names.
Include any platform-specific invocation syntax under the table.
