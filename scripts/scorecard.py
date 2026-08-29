#!/usr/bin/env python3
"""scorecard.py — paired scorecard per the frozen pre-registration rules.

Usage:
  scripts/scorecard.py <sonnet-jobs-dir> <opus-jobs-dir>

Rules (PRE_REGISTRATION.md): an instance passes for a model if >=2 of its
trials scored reward 1.0. Rate-limited trials (429 / session limit) are
excluded from the denominator, and a sweep with >=2 limit hits is VOID —
the scorecard refuses to score it. Pass difference <10 points means "no
meaningful quality difference -> standardize on the cheaper model".
"""
import glob
import json
import os
import sys

VOID_MARKERS = ("session limit", "429")
MIN_HITS_BEFORE_VOID = 2


def load_sweep(jd):
    """Returns (per_instance_trial_rewards, limit_hits, voided)."""
    per, hits, exceptions = {}, 0, 0
    for rj in glob.glob(os.path.join(jd, "*", "result.json")):
        r = json.load(open(rj))
        for ev in r.get("stats", {}).get("evals", {}).values():
            for score, ids in ev.get("reward_stats", {}).get("reward", {}).items():
                for tid in ids:
                    per.setdefault(tid.split("__")[0], []).append(float(score))
    for exf in glob.glob(os.path.join(jd, "*", "*", "exception.txt")):
        exceptions += 1
        try:
            content = open(exf, encoding="utf-8", errors="replace").read()
        except OSError:
            continue
        if any(m in content for m in VOID_MARKERS):
            hits += 1
    voided = hits >= MIN_HITS_BEFORE_VOID
    return per, hits, voided, exceptions


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: scorecard.py <sonnet-jobs-dir> <opus-jobs-dir>")
    results = {}
    for model, jd in zip(("sonnet", "opus"), sys.argv[1:]):
        per, hits, voided, exceptions = load_sweep(jd)
        print(f"{model}: {jd}")
        print(f"  trials grouped: {sum(len(v) for v in per.values())} | exceptions: {exceptions} | limit hits: {hits}"
              + ("  => SWEEP VOID" if voided else ""))
        results[model] = (per, voided)
    if any(v for _, v in results.values()):
        print("\nrefusing to score: at least one sweep is VOID (rate-limited). Re-run after reset.")
        sys.exit(1)

    sonnet, opus = (results[m][0] for m in ("sonnet", "opus"))
    keys = sorted(set(sonnet) | set(opus))
    def passed(per, k):
        return sum(per.get(k, [])) >= 2
    ps = sum(passed(sonnet, k) for k in keys)
    po = sum(passed(opus, k) for k in keys)
    print(f"\n{'instance':44} sonnet  opus")
    for k in keys:
        print(f"{k[:44]:44} {'✓' if passed(sonnet, k) else '✗':>6} {'✓' if passed(opus, k) else '✗':>6}")
    print(f"\npass rate: sonnet {ps}/{len(keys)} = {100*ps/len(keys):.0f}%  |  opus {po}/{len(keys)} = {100*po/len(keys):.0f}%")
    print(f"pass diff (sonnet - opus): {ps - po} points  "
          f"({'<10 -> no meaningful difference -> cheaper model' if abs(ps-po) < 10 else '>=10 -> higher-pass model'})")
    print(f"both: {sum(1 for k in keys if passed(sonnet,k) and passed(opus,k))} | "
          f"sonnet-only: {sum(1 for k in keys if passed(sonnet,k) and not passed(opus,k))} | "
          f"opus-only: {sum(1 for k in keys if passed(opus,k) and not passed(sonnet,k))} | "
          f"neither: {sum(1 for k in keys if not passed(sonnet,k) and not passed(opus,k))}")


if __name__ == "__main__":
    main()
