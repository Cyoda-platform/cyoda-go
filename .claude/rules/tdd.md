# TDD Protocol

## Feature Development: RED → GREEN → REFACTOR

1. **RED:** Write the full test suite for the feature first. These tests are the executable contract. Run them to prove they fail.
2. **GREEN:** Implement the minimum code to make the tests pass. Do not write production code without a failing test driving it.
3. **REFACTOR:** Clean up the implementation while keeping all tests green.

Do not move to the next task until the current tests and all prior tests are green.

## Bug Fix TDD

1. **Reproduce:** Write a failing test that demonstrates the bug. Run it to confirm it fails.
2. **Fix:** Make the minimal code change to make the test pass.
3. **Verify:** Run the full test suite to confirm no regressions.

Never fix a bug without first writing a test that catches it. The test proves the bug exists and prevents regression.
