# prospect

Turns your repository's fix history into benchmark tasks for coding agents, saved in [Harbor](https://github.com/harbor-framework/harbor) format.

Every merged commit that touched both source and tests is a small, self-contained proof of work: someone changed the code, added tests, and those tests pass. prospect walks that history, checks out the parent of each such commit, adds the tests the fix introduced, and keeps only the instances where those tests compile against the old code and actually fail on it. The fix itself becomes the reference solution. What comes out the other end is a Harbor task directory per fix, with instruction, environment, hidden tests, and reference patch, ready to run against any agent Harbor supports.

## Why

Public benchmarks tell you how agents do on someone else's tasks. That's useful for picking a model off a leaderboard and mostly useless for picking one for your codebase. If your real question is "which model handles our kind of work," the honest answer comes from testing on fixes from your own history, where the tests already exist and the correct answer is known.

## Results

292 witness-verified instances from 7 repositories (294 emitted), mined in a single pass (2026-08-28):

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

Two honesty notes on these numbers. First, instances that share reference tests measure the same thing, so they're clustered at analysis time; fiber's 203 instances are really about 24 independent observations. Second, every instance above passed container verification with an assertion-level failure witness: the tests provably failed on the old code *at the failing assertion*, not because a build broke.

To show the whole loop working end to end, we ran a 4-week pre-registered comparison with claude-code on these suites: sonnet-5 vs opus-5, 3 trials per instance, versions pinned, transcripts audited for origin fetches. Cumulative pass rates were 69% vs 74%, a gap that never exceeded a single point in any week, while opus cost 1.9× more in tokens. That comparison is the kind of recorded decision this tool exists to produce.

## Usage

```bash
go build -o prospect .
./prospect build /path/to/repo --out ./suite   # full pipeline: mine, reverify, deleak, issues, dedup
./prospect mine /path/to/repo --out ./mined [--since 2025-12-01] [--limit 90] [--probe-only] [--env KEY=VALUE ...]
./prospect reverify ./mined --jobs 2          # re-verify emitted artifacts in their containers
./prospect deleak ./mined --repo /path/to/repo --name repo  # strip test-authoring directives, flag leak suspects
./prospect issues ./mined                      # rewrite instructions from linked issue bodies
./prospect dedup ./mined                       # cluster instances sharing reference tests
./prospect score --suite ./suite SONNET=/jobs/a OPUS=/jobs/b  # cluster-weighted scorecard from Harbor job dirs
./prospect migrate ./mined                     # lift schema-1 task metadata to schema 2
./prospect audit <harbor-jobs-dir> --origin <repo-slug>  # leak audit for closed-book suites
```

Each sweep should also go through `scripts/dogfood-run.sh`, which pins tool and agent versions to a ledger and audits the transcripts afterwards.

## What it measures (and doesn't)

This is a benchmark of specified changes: "here is the change, implement it correctly on this codebase." That's the dominant real-world use of coding agents, and it's what the data measures cleanly. It is *not* a bug-diagnosis benchmark: tasks where the agent must find the bug from a symptom are a different pipeline (mining from issue reports instead of fix commits) and may come later.

Two structural facts to keep in mind. The compile probe deliberately throws away fixes where the new tests can't compile against the old interfaces, so the suite systematically excludes the hairy, architectural work where agents differ most. And instances whose tests reference the origin repo are open-book for any agent with network access, so those are marked `origin_visibility: public` and shouldn't be used for cross-vendor rankings. Instances from private repos are closed-book (the origin 404s for tokenless agents), and that's the column to trust.

Bug-fix diagnosis tasks do exist here, but only for repos where contributors link issues in their PRs (`Fixes #N`); the instruction then comes from the issue text instead of the fix description. 12 of the 294 instances are in that class. If your team adopts issue linkage, your own mined suite grows that class automatically.

## Caveats

- Go repos only, for now.
- Instances sharing reference tests are clustered by `prospect dedup`; treat clusters, not raw counts, as units of evidence.
- Service containers are parsed from the repo's GitHub Actions workflows. Tests that need services declared only in docker-compose (a mailpit instance, say) may stay unverified.
- The undiagnosed failure from our own runs (one concurrency-heavy fix passed on the host and failed in the container on both alpine and debian) is documented rather than hidden. Verification is strict; when in doubt it throws instances out.

## License

[Apache-2.0](LICENSE)
