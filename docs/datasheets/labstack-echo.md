# Suite datasheet: labstack/echo

Mined task instances for coding-agent evaluation, generated from the public fix history of [labstack/echo](https://github.com/labstack/echo) by [prospect](https://github.com/ARMeeru/prospect).

## Provenance

| | |
|---|---|
| Source repository | labstack/echo, commit history on `master` |
| Source license | MIT |
| Mine window | commits after 2025-12-01 (post-training-cutoff windowing; see contamination note) |
| Mined | 2026-08-28, prospect at commit `ea848df` |
| Instance license | instances derive from MIT-licensed public commits; the generating tool is Apache-2.0 |

## Funnel

| stage | count |
|---|---|
| test-touching candidates in window | 40 |
| compile-probe survivors | 18 |
| verified instances emitted | 16 |
| effective clusters (dedup) | 11 |
| bug-report class (instruction from linked issue) | 0 |

## Verification status

- Fail-before/pass-after: every instance's reference tests were run against the pre-fix source (must fail) and the post-fix source (must pass), at mine time on the host.
- Failure witness: recorded per instance; the base-direction failure is assertion-level (`--- FAIL: <expected test>`), not a build or environment error.
- Container re-verification: rerun in progress (full suite, 16 instances). Status backfills into this datasheet.

## Contamination and leakage

- Mined inside a post-cutoff window; contamination is a property of the model-instance pair, so base-commit dates are in each task's metadata for per-model windowing.
- Source repo is public: instances are open-book by policy (`origin_visibility: public`). Default agent egress allows fetching the origin. Use for within-agent regression and pipeline work, not cross-vendor ranking.

## What the tasks are

Each instance is a specified change: instruction (from the fix commit body, test-authoring directives stripped), container image of the pre-fix code, hidden reference tests, and the human's fix as the reference solution. Harbor task format, verifiable with `prospect reverify`.
