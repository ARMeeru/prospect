#!/bin/bash
# dogfood-run.sh — one pre-registered weekly sweep with version pinning and
# the origin-fetch audit. Usage:
#   scripts/dogfood-run.sh <model> [trials] [suite-root]
#   scripts/dogfood-run.sh claude-sonnet-5 3   # closed-book suite default
set -eu
cd "$(dirname "$0")/.."
# Never source the user's rc (it may exit in non-interactive shells) —
# extract the token from it instead, or inherit an already-exported one.
export PATH="$HOME/.local/bin:$PATH"
if [ -z "${ANTHROPIC_AUTH_TOKEN:-}" ]; then
  ANTHROPIC_AUTH_TOKEN=$(sed -n 's/^export ANTHROPIC_AUTH_TOKEN=//p' ~/.zshrc 2>/dev/null | tail -1 | tr -d '"'"'"'')
fi
[ -n "${ANTHROPIC_AUTH_TOKEN:-}" ] || { echo "error: ANTHROPIC_AUTH_TOKEN not found (env or ~/.zshrc)"; exit 1; }
export CLAUDE_CODE_OAUTH_TOKEN="$ANTHROPIC_AUTH_TOKEN"
export CLAUDE_FORCE_OAUTH=1

MODEL="${1:?usage: dogfood-run.sh <model> [trials] [suite-root]}"
TRIALS="${2:-3}"
SUITE="${3:-$PWD/_suites/prospect-lendleaf}"
STAMP=$(date +%Y%m%d-%H%M%S)
JOBS="/tmp/dogfood-jobs/$STAMP"
LEDGER="$PWD/runs/ledger.jsonl"
mkdir -p runs "$JOBS"

# --- version pinning: every run records its toolchain, or it didn't happen ---
TOOL_COMMIT=$(git rev-parse HEAD 2>/dev/null || echo norepo)
HARBOR_VER=$(harbor --version 2>/dev/null || echo unknown)
DOCKERFILE_HASH=$(shasum -a 256 "$SUITE"/*/environment/Dockerfile | shasum -a 256 | cut -d' ' -f1)
printf '{"ts":"%s","model":"%s","trials":%s,"suite":"%s","tool_commit":"%s","harbor":"%s","dockerfiles_sha256":"%s","jobs_dir":"%s"}\n' \
  "$STAMP" "$MODEL" "$TRIALS" "$(basename "$SUITE")" "$TOOL_COMMIT" "$HARBOR_VER" "$DOCKERFILE_HASH" "$JOBS" >> "$LEDGER"

# --- scored run ---
harbor run -p "$SUITE" -a claude-code -m "$MODEL" -n 4 -k "$TRIALS" -o "$JOBS"

# --- rate-limit audit: subscription 429s void the sweep (not agent failures) ---
LIMIT_HITS=$(find "$JOBS" -name exception.txt -exec grep -l "session limit\|429" {} \; 2>/dev/null | wc -l | tr -d ' ')
if [ "${LIMIT_HITS:-0}" -ge 2 ]; then
  echo "SWEEP VOID: $LIMIT_HITS trials hit subscription rate limits — results must not be scored; re-run after reset"
  printf '{"ts":"%s","model":"%s","audit":"void","limit_hits":%s}\n' "$STAMP" "$MODEL" "$LIMIT_HITS" >> "$LEDGER"
else
  printf '{"ts":"%s","model":"%s","audit":"clean","limit_hits":%s}\n' "$STAMP" "$MODEL" "$LIMIT_HITS" >> "$LEDGER"
fi

# --- origin-fetch audit: any reference to the private origin voids trials ---
# (closed-book suite only; pass --origin "" equivalence by skipping for public roots)
case "$(basename "$SUITE")" in
  *spire*|*gin*|*chi*|*echo*|*fiber*|*pgx*) echo "public suite: origin audit skipped (open-book by policy)" ;;
  *) ./prospect audit "$JOBS" --origin samuraixwandering || echo "VIOLATION RECORDED in ledger context: $STAMP" ;;
esac
echo "sweep $STAMP complete — job dir: $JOBS"
