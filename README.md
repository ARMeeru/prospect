# prospect

Mines verified coding-agent task instances from a git repository's real fix history, and emits them in [Harbor](https://github.com/harbor-framework/harbor) task format.

For every merged commit that changed both source and tests (or a test-only "failing property" commit followed by its fix), prospect:

1. constructs the **base state** (parent commit, or the test commit itself) and overlays the reference tests,
2. applies a **compile probe** — interface-coupled fixes whose tests can't build on the base are discarded in seconds,
3. **behaviorally verifies** fail-before (base) and pass-after (fix) — including service containers parsed from the repo's own GitHub Actions workflow,
4. emits a Harbor task: `instruction.md`, `task.toml`, `environment/` (base snapshot + Dockerfile), `tests/` (hidden reference tests + `test.sh` writing a reward file), `solution/` (reference patch + `solve.sh`).

## Usage

```bash
go build -o prospect .
./prospect mine /path/to/repo --out ./mined [--since 2025-08-01] [--limit 90] [--env KEY=VALUE ...]
```

Results land in `--out`: one directory per task plus `results.json` and `summary.md`.

## Status

Phase 1 v0 — see [ROADMAP](../agent-eval-harness/ROADMAP.md) for gates and results. Validated on seven repositories (a private Go backend: 18 closed-book instances; six public Go repos: the rest), including reproduction of all hand-verified instances.

**Scope statement (read before quoting any number from this tool):** **this is a change-spec benchmark with a bug-report subclass where repos support it.** prospect mines well-scoped behavioral fixes — the compile probe deliberately discards interface-coupled changes, which are exactly where coding agents differ most. It measures "can an agent implement a specified change correctly on my codebase, at what cost," not "can an agent diagnose your repo's bugs." The bug-report subclass requires repos with enforced `Fixes #N` linkage (12 of 292 instances as of 2026-08-28, all public); teams that adopt issue linkage grow their own bug-report suite — issue-first mining is a possible v2. Phase-1 external review ruled Gate 1 FAILED on count (19 < 25 on the best repo); all instances are container-verified with assertion-level failure witnesses since the Phase-1.5 witness sweep.

## Data policy

- **origin_visibility** is recorded per instance. `private` instances (from a private Go repository): the origin returns 404 to tokenless agents, so PR numbers and repo names in instructions leak nothing — these are closed-book.
- `public` instances (spire): **open-book by policy.** Default Harbor egress allows an agent to fetch the origin and copy the fix; plus their content predates most model training cutoffs. Public instances are for harness plumbing and within-agent regression only — never cross-vendor ranking.
- Training contamination is a property of the *model-instance pair*: suites record base-commit dates so evaluation can window to post-cutoff commits.
- Uncontaminated, closed-book instances can only come from private repos. This column is structurally capped; it is the number that matters and the number we publish first.

## Known v0 limits

- Service-dependent tests: services come from the first GitHub Actions workflow that declares containers; services declared only in docker-compose (e.g. mailpit) are missed — some instances therefore stay unverified.
- Emitted tasks are host-verified (`verified_host_only = true`): running them inside Harbor with multi-container environments is Phase-2 work.
- Go repos only. No dedup yet. Instructions are commit-message-derived; PR-body enrichment is planned.
