# TDD Process - MANDATORY

## CRITICAL RULE: Tests FIRST, Implementation SECOND

### TDD Cycle (MUST FOLLOW):

1. **RED**: Write failing test FIRST
   - Test must fail for the right reason (feature doesn't exist)
   - Run test to confirm it fails
   - DO NOT write implementation code yet

2. **GREEN**: Write minimal implementation to make test pass
   - Only write code needed to pass the test
   - Run test to confirm it passes

3. **REFACTOR**: Improve code while keeping tests green
   - Run tests after refactoring to ensure they still pass

### For Each Todo Item:

1. Read the plan requirements
2. Write the test file FIRST (before any implementation)
3. Run the test - it MUST fail
4. Only then write the implementation
5. Run the test again - it MUST pass
6. Move to next todo

### NEVER:

- Write implementation before tests
- Write tests and implementation together
- Skip the "red" phase
- Assume tests will pass without running them first

### ALWAYS:

- Write test first
- Run test to see it fail
- Then implement
- Run test to see it pass
