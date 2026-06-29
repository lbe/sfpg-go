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

## Forward Reference Detection

The validator also detects **forward references** — cases where `install <BehaviorName>`
appears before the corresponding `behavior <BehaviorName>` definition in the same file.
Hyperscript behaviors must be parsed before they can be installed.

### How It Works

After extracting all hyperscript snippets from a file, the validator parses each snippet
using the hyperscript AST and collects all `behavior` and `install` features. Features
are sorted by line number and checked in order:

- `behavior <Name>` registers the behavior as defined at that line
- `install <Name>` is flagged if no matching behavior has been defined yet

### Cross-File Mitigation

If a behavior is defined in a different file (e.g., a layout template that wraps partial
content), `install` references to it in the partial will not find a matching `behavior`
within the same file. These are reported as **WARNING** (not ERROR) since the behavior
may be defined elsewhere.

Example output:

```
[⚠ WARNING] web/templates/config-modal.html.tmpl:86
  forward reference: install "TabSwitcher" at line 86, behavior not found in same file
```

### Best Practice

Behaviors used across multiple templates should be defined in the layout template
(`layout.html.tmpl`), which is parsed first. The installs in partial templates will
show warnings (not errors), confirming the cross-file dependency.

## Recommended Workflow

1. Write Hyperscript directly in your template.
2. Run the CLI validator against the changed file or `web/templates`.
3. Fix any reported errors and re-run until clean.
4. Check forward reference warnings: ensure cross-file behaviors are defined in the correct template.

## Go Template Escaping Detection

The validator warns when hyperscript code inside `<script type="text/hyperscript">` blocks
contains `<` characters that Go's `html/template` will escape at render time.

Go's template engine does NOT recognize `text/hyperscript` as a JavaScript MIME type, so
it remains in HTML context where `<` followed by a character that cannot start an HTML tag
(such as `.` in `<.class/>` selectors) gets escaped to `&amp;lt;`. This breaks hyperscript
at runtime even though the validator sees valid syntax in the source file.

To avoid this, use `querySelectorAll('.class')` instead of `<.class/>` inside script blocks,
or use `@attr` syntax instead of `getAttribute()`. Attribute-based hyperscript (`_="..."`)
is not affected because Go handles attribute content differently.

Example warning output:

```
[WARNING] web/templates/layout.html.tmpl:1132
  Go html/template may escape `<` to `&amp;lt;` in script block: hidden to <.tab-panel/> in
```

## Common Issues

- Go's `html/template` is strict about quotes and escaping inside attributes.
  - Prefer single quotes `'...'` for outer Hyperscript strings.
  - Use HTML entities for double quotes inside strings: `&quot;`.
  - Example: `_="on click set html to '<div class=&quot;test&quot;>content</div>'"`

- Avoid backticks in Hyperscript strings within Go templates.
  - If needed, build strings via concatenation instead of template literals.
  - Example: `'template ' + var` instead of `` `template ${var}` ``.

- Inside `<script type="text/hyperscript">` blocks, prefer `@attr` syntax over
  `getAttribute('attr')` — it is more idiomatic and avoids Go template escaping issues.
  Use `querySelectorAll('.class')` instead of `<.class/>` selectors to avoid `<` characters
  that Go's template engine may escape.

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
