# prospect

Mines verified coding-agent task instances from a git repository's real fix history, and emits them in [Harbor](https://github.com/harbor-framework/harbor) task format.

For every merged commit that changed both source and tests — or a test-only "failing property" commit followed by its fix — prospect:

1. constructs the **base state** (parent commit, or the test commit itself) and overlays the reference tests,
2. applies a **compile probe** — interface-coupled fixes whose tests can't build on the base are discarded in seconds, with zero judgment,
3. **behaviorally verifies** fail-before (base) and pass-after (fix) inside containers, with service containers parsed from the repo's own GitHub Actions workflow,
4. asserts a **failure witness** — the base run must fail at the assertion level (`--- FAIL: <expected test>`), not via build errors or environment noise,
5. emits a Harbor task: `instruction.md`, `task.toml`, `environment/` (base snapshot + Dockerfile), `tests/` (hidden reference tests + `test.sh` writing a reward file), `solution/` (reference patch + `solve.sh`).

## Results

294 verified instances across 7 repositories in one pass (2026-08-28):

| repository | post-cutoff candidates | compile-probe survivors | verified instances |
|---|---|---|---|
| a private Go backend | 102 | 40 | 18 (closed-book) |
| [spiffe/spire](https://github.com/spiffe/spire) | 60 | 19 | 8 |
| [gin-gonic/gin](https://github.com/gin-gonic/gin) | 21 | 14 | 8 |
| [go-chi/chi](https://github.com/go-chi/chi) | 13 | 9 | 8 |
| [labstack/echo](https://github.com/labstack/echo) | 40 | 18 | 16 |
| [gofiber/fiber](https://github.com/gofiber/fiber) | 488 | 322 | 203 |
| [jackc/pgx](https://github.com/jackc/pgx) | 94 | 58 | 31 |
| **total** | | | **294** |

Dedup clustering (shared reference test files) reduces raw counts to an effective suite size — e.g. fiber's 203 instances form 24 clusters. Instance counts are post-dedup-labeling; treat clusters, not raw counts, as units of evidence.

As an end-to-end demonstration, a 4-week pre-registered dogfood ran these suites through Harbor with claude-code (sonnet-5 vs opus-5, 3 trials per instance, per-run version pinning, origin-fetch audits, weekly over-specificity labeling). Cumulative result: sonnet 69.4%, opus 73.6% — a stable ≤1-point weekly difference, far below the pre-registered 10-point meaningfulness bar — while opus cost 1.87× more. Recorded decision: standardize on sonnet.

## Usage

```bash
go build -o prospect .
./prospect mine /path/to/repo --out ./mined [--since 2025-12-01] [--limit 90] [--probe-only] [--env KEY=VALUE ...]
./prospect reverify ./mined --jobs 2          # re-verify emitted artifacts in their containers
./prospect deleak ./mined --repo /path/to/repo --name repo  # strip test-authoring directives, flag leak suspects
./prospect issues ./mined                      # rewrite instructions from linked issue bodies
./prospect dedup ./mined                       # cluster instances sharing reference tests
./prospect audit <harbor-jobs-dir> --origin <repo-slug>  # leak audit for closed-book suites
```

Results land in `--out`: one directory per task plus `results.json` and `summary.md`.

## Data policy

- **origin_visibility** is recorded per instance. `private` instances: the origin returns 404 to tokenless agents, so PR numbers and repo names in instructions leak nothing — these are closed-book.
- `public` instances: **open-book by policy.** Default Harbor egress allows an agent to fetch the origin and copy the fix; plus their content predates most model training cutoffs. Public instances are for harness plumbing and within-agent regression only — never cross-vendor ranking.
- Training contamination is a property of the *model-instance pair*: suites record base-commit dates so evaluation can window to post-cutoff commits.
- Uncontaminated, closed-book instances can only come from private repos. This column is structurally capped; it is the number that matters and the number we publish first.

## Scope statement (read before quoting any number from this tool)

**This is a change-spec benchmark with a bug-report subclass where repos support it.** prospect mines well-scoped behavioral fixes — the compile probe deliberately discards interface-coupled changes, which are exactly where coding agents differ most. It measures "can an agent implement a specified change correctly on my codebase, at what cost," not "can an agent diagnose your repo's bugs." The bug-report subclass requires repos with enforced `Fixes #N` linkage (12 of 294 instances as of 2026-08-28, all public); teams that adopt issue linkage grow their own bug-report suite — issue-first mining is a possible v2.

## Limitations

- **Single language:** Go repos only.
- **Message-assertion lint:** reference tests asserting on error *text* (rather than behavior) are flagged via `verifier_lint` in meta.json and should be hand-reviewed — a semantically correct fix worded differently can fail such tests.
- **Dedup is heuristic:** clusters by shared reference test files.
- **Services:** container environments for service-dependent instances are best-effort (parsed from Actions workflows); instances whose tests need unexported services (e.g. docker-compose-only mailpit) may stay unverified.

## License

[Apache-2.0](LICENSE)
