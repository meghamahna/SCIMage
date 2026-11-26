---
name: test-driven-dev
description: Test-driven development workflow (red → green → refactor) with a strict test budget (the fewest tests that catch real regressions) and a test-quality checklist. Use when implementing features or fixing bugs, when asked to add/write/fix tests, when reproducing a bug, or when reviewing test coverage.
---

# Test-driven development

## Test budget (read this first)
Tests cost tokens to write, run and maintain. Write the fewest tests that would catch a real regression in this change, then move on.
- **New behavior:** one test for the main success path, plus one for each failure path the change introduces. Add a boundary test only where an off-by-one or limit is plausible.
- **Bug fix:** one test that reproduces the bug.
- **Refactor with no behavior change:** no new tests; the existing ones must stay green.
- **Typical change: 1 to 5 tests.** If you need more, say why in one line in your reply.
- Extend an existing test file, or add a row to an existing table-driven test, before creating new files. Use one parameterized test instead of near-copies.
- **Don't test:** trivial getters and setters, framework or library behavior, private helpers already covered through the public interface, generated code, constants, or log text.
- Stop when every changed behavior that carries real risk has a test. Coverage percentages are not a goal.

## Loop
1. **Red.** Write the smallest failing test that expresses the next behavior. Run it and **watch it fail for the right reason**.
2. **Green.** Write the minimum code that makes it pass. Run the focused test, then the related suite.
3. **Refactor.** Remove duplication and improve names while tests stay green, and keep refactors in separate steps from behavior changes.
4. Repeat. Commit at green points (commit-helper).

**Bug fixes start with a test that reproduces the bug.** The fix is done when that test passes and nothing else broke.

Run throwaway code inline (`python3 - <<'EOF'`) or save it with `python3 .claude/tools/save.py scratch <name>`; never Write/Edit inside `.claude/`. Once the real test exists and passes, remove anything you created that isn't part of the change (`python3 .claude/tools/cleanup.py candidates`, then `remove <path>`).

## Finding the commands
- Check the knowledge graph first: `python3 .claude/tools/kg.py query test`, or the `command-test` node.
- Otherwise use `python3 .claude/tools/stack.py`, then the project manifest (package.json scripts, pyproject, Makefile).
- Run only the tests you touched plus the directly related suite, with quiet or fail-only flags, and read only the failures. Run the full suite once at the end if it is fast; if it is slow, say so and leave it to CI.

## What to test
- **Behavior through public interfaces,** not private internals.
- **Pick cases by input class:** group inputs the code treats the same way (valid, each kind of invalid, each special case) and test the one or two classes most likely to break, with a boundary value where a limit exists. You don't need a test per class.
- **Edge cases to consider** (choose the risky ones; don't test them all):
  - empty and one input; many inputs
  - boundaries (0, max, off-by-one)
  - invalid and malformed input
  - unicode and whitespace
  - concurrency or ordering, if relevant
  - time zones and DST, if relevant
- **Failure paths:** errors raised or returned, retries, timeouts, and that partial failure leaves no partial state.
- **Security-relevant behavior:** authz denied, input rejected.

## Test quality checklist
- [ ] The name states the behavior: `test_refund_rejected_when_order_already_refunded`.
- [ ] Arrange-Act-Assert with one behavior per test, and assertions on outcomes rather than call counts where possible.
- [ ] Hermetic and deterministic: no real network, sleep or wall clock (inject a clock), seeded randomness, no shared mutable global state, and files only in a per-test temp directory.
- [ ] Independent: no order dependence, and shared state is reset.
- [ ] **Prefer fakes over mocks.** A fake is a small working implementation (an in-memory repository, a fake clock). Mock only at boundaries you don't own (HTTP, third-party SDKs), and assert on outcomes rather than on which calls were made.
- [ ] Fixtures are minimal and obviously fake: emails on `example.com`, and fictional 555-01xx phone numbers. **Never use real PII or real keys.** The harness blocks them.
- [ ] Fast: unit tests run in milliseconds. Integration tests are marked and separated.
- [ ] No asserting on log text or error strings unless that is the contract.

## Done means
- New behavior is covered, the reproducing test for a bug exists, and the full relevant suite passes.
- Every risky changed behavior has one test. Don't chase a coverage number, and don't add tests after that point.
