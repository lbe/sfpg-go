# Hyperscript Validation Guide (Automated)

Use the automated CLI validator to scan templates and validate Hyperscript syntax directly from the command line. No browser or separate server is required.

## Quick Start

Validate a single file or an entire directory:

```bash
# Validate a single template file
go run ./scripts/validate-hyperscript.go web/templates/config-modal.html.tmpl

# Validate all templates recursively
go run ./scripts/validate-hyperscript.go web/templates
```

Exit codes:

- 0: All snippets valid or no hyperscript found
- 1: One or more invalid snippets detected

## CLI Options

- `-json`: Output machine-readable JSON
- `-hyperscript=<path>`: Use a local `hyperscript.js` instead of CDN
- `-quiet`: Only output errors (hide valid results)
- `-ext=".html,.tmpl,.gohtml"`: Comma-separated extensions to include

Examples:

```bash
# JSON output for tooling
go run ./scripts/validate-hyperscript.go -json web/templates

# Use a local hyperscript.js file
go run ./scripts/validate-hyperscript.go -hyperscript=third_party/_hyperscript.min.js web/templates

# Scan multiple paths with custom extensions
go run ./scripts/validate-hyperscript.go -ext=".html,.tmpl" web/templates zarchive
```

## What It Validates

- Attribute-based Hyperscript: `_="..."` and `_='...'`
- Script blocks: `<script type="text/hyperscript"> ... </script>`
- HTML entities inside attributes are decoded before validation (e.g., `&quot;` → `"`)

## Recommended Workflow

1. Write Hyperscript directly in your template.
2. Run the CLI validator against the changed file or `web/templates`.
3. Fix any reported errors and re-run until clean.

## Common Issues

- Go's `html/template` is strict about quotes and escaping inside attributes.
  - Prefer single quotes `'...'` for outer Hyperscript strings.
  - Use HTML entities for double quotes inside strings: `&quot;`.
  - Example: `_="on click set html to '<div class=&quot;test&quot;>content</div>'"`

- Avoid backticks in Hyperscript strings within Go templates.
  - If needed, build strings via concatenation instead of template literals.
  - Example: `'template ' + var` instead of `` `template ${var}` ``.

## Integration Tips

- Add a Makefile target to check templates quickly:

  ```make
  validate-hyperscript:
  go run ./scripts/validate-hyperscript.go web/templates
  ```

- Use `-quiet` in CI to only show failures.

## Best Practices

1. Validate before committing changes to templates.
2. Escape quotes properly with HTML entities when inside attributes.
3. Keep Hyperscript simple; prefer DOM operations for complex string building.
4. Validate incrementally as you edit to catch issues early.
