# Execution Protocol Checklist - Config Precedence Hardening Plan

## Mandatory Per-Step Workflow

### 1. TDD Enforcement (MANDATORY)

Per `references/tdd_process.md`:

- [ ] **RED**: Write failing test FIRST
- [ ] Run test to confirm it fails for the right reason
- [ ] **GREEN**: Write minimal implementation to make test pass
- [ ] Run test to confirm it passes
- [ ] **REFACTOR**: Improve code while keeping tests green

**NEVER write implementation before tests.**

### 2. AGENTS.md Approval Protocol (MANDATORY)

Per `AGENTS.md` Zeroth Law:

- [ ] **Analyze and Propose**: Describe what you will do and why
- [ ] **STOP. Await Approval**: Do not proceed without explicit user approval
- [ ] **Act ONLY after approval**: Execute only approved actions

### 3. Pre-Commit Verification Gates (ALL MUST PASS)

#### Code Formatting

- [ ] Run `gofmt -w .` on changed Go files
- [ ] Run `goimports -w .` on changed Go files
- [ ] Run `prettier --write <file>` on changed `*.html.tmpl` files

#### Template & Hyperscript Validation

- [ ] Run `make validate-templates`
- [ ] If templates/Hyperscript changed: `make validate-hyperscript` or
      `go run ./scripts/validate-hyperscript.go web/templates`

#### Build Verification

- [ ] Run `go build -o /dev/null .` (must exit 0)

#### Test Verification

- [ ] Run `go test -tags "integration e2e" ./... > ./tmp/test_output.txt 2>&1`
- [ ] Run `grep -E "FAIL|PASS|ERROR" ./tmp/test_output.txt` to verify results
- [ ] **NEVER** use pipes with grep during test runs (causes multiple compilations)

#### Runtime Verification

- [ ] Test against running dev server on `localhost:8083` with curl
- [ ] **NEVER** start your own server (air is running on port 8083)
- [ ] **NEVER** spawn background processes with `&`

#### HTML Content Assertions

Per `references/methodology-html-content-test-writing.md`:

- [ ] Use `testutil.ParseHTML()` for response parsing
- [ ] Use `FindElementByID()`, `FindElementByClass()`, `FindElementByTag()`
- [ ] Use `GetAttr()`, `GetTextContent()` for assertions
- [ ] **NEVER** use `strings.Contains()` on HTML response bodies

### 4. Commit Workflow

- [ ] Prepare commit message in `./tmp/commit_message.txt`
- [ ] Run `git commit -F ./tmp/commit_message.txt`
- [ ] Verify commit succeeded before proceeding to next step

### 5. Error Ownership

- [ ] If this step discovers errors, this same sub-agent MUST fix them
- [ ] Re-run all verification gates after fixes
- [ ] Do not proceed until all gates pass

## HTMX/Hyperscript Specific Rules

### HTMX Patterns (references/htmx-referencd.md)

- [ ] Check for `Hx-Request` header to identify HTMX partials
- [ ] Use `hx-swap-oob="outerHTML"` for out-of-band updates
- [ ] Return 200 for validation errors (not 400) so HTMX processes response
- [ ] Verify OOB elements have `id` attributes and matching DOM targets exist

### Hyperscript Validation (scripts/hyperscript_validation.md)

- [ ] Run `go run ./scripts/validate-hyperscript.go <file-or-dir>`
- [ ] Use single quotes for outer Hyperscript strings in templates
- [ ] Use HTML entities (`&quot;`) for inner double quotes
- [ ] Validate before committing template changes

## Forbidden Practices (ZERO TOLERANCE)

- ❌ **NEVER** implement without tests first
- ❌ **NEVER** start your own dev server
- ❌ **NEVER** skip curl testing against localhost:8083
- ❌ **NEVER** use `strings.Contains()` on httptest responses
- ❌ **NEVER** use Python (use bash or Perl for scripts)
- ❌ **NEVER** make assumptions without verification
- ❌ **NEVER** skip the approval step (AGENTS.md Zeroth Law)

## Verification Commands Reference

### Quick Verification

```bash
# Format check
gofmt -l . | grep -v vendor
goimports -l . | grep -v vendor

# Build
go build -o /dev/null .

# Tests (efficient pattern)
go test -tags "integration e2e" ./... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|PASS|ERROR" ./tmp/test_output.txt

# Template validation
make validate-templates

# Hyperscript validation
make validate-hyperscript
```

### Runtime Testing

```bash
# Test authenticated flow
curl -s http://localhost:8083/gallery/1 | head -5

# Login
curl -s -X POST http://localhost:8083/login \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "username=admin" \
  -d "password=admin" \
  -c ./tmp/cookies.txt

# Access authenticated endpoint
curl -s http://localhost:8083/dashboard \
  -b ./tmp/cookies.txt | head -10
```

## Step Completion Criteria

A step is complete ONLY when:

1. All proposed actions executed successfully
2. All verification gates pass
3. Commit successfully created
4. No errors or warnings remain unresolved
