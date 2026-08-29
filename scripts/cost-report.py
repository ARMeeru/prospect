#!/usr/bin/env python3
"""cost-report.py — aggregate token usage (and optional $ cost) from Harbor job dirs.

Usage:
  scripts/cost-report.py <jobs-dir> [more-jobs-dirs...]

Reads agent transcripts (claude-code stream-json) and sums token usage per
model. If a prices.json exists next to this script — {"claude-sonnet-5":
{"in": 3.0, "out": 15.0, "cache_read": 0.3, "cache_write": 3.75}, ...} (per
1M tokens) — prints dollars as well. Without prices, prints raw tokens; the
frozen secondary metric is cost-per-solved-task, so fill in prices before the
4-week readout.
"""
import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
prices = {}
ppath = os.path.join(HERE, "prices.json")
if os.path.exists(ppath):
    prices = json.load(open(ppath))

totals = {}
for jd in sys.argv[1:]:
    for tfile in glob.glob(os.path.join(jd, "**", "agent", "*.txt"), recursive=True):
        model = None
        for line in open(tfile, encoding="utf-8", errors="replace"):
            try:
                d = json.loads(line)
            except json.JSONDecodeError:
                continue
            if d.get("type") == "system" and d.get("model"):
                model = d["model"].split("[")[0]
            u = (d.get("message") or {}).get("usage") if isinstance(d.get("message"), dict) else None
            if u:
                t = totals.setdefault(model or "unknown", {"in": 0, "out": 0, "cache_read": 0, "cache_write": 0})
                t["in"] += u.get("input_tokens", 0) or 0
                t["out"] += u.get("output_tokens", 0) or 0
                t["cache_read"] += u.get("cache_read_input_tokens", 0) or 0
                t["cache_write"] += u.get("cache_creation_input_tokens", 0) or 0

print(f"{'model':24} {'in':>10} {'out':>10} {'cache_read':>12} {'cache_write':>12} {'cost_usd':>10}")
grand = 0.0
for m, t in sorted(totals.items()):
    cost = ""
    p = prices.get(m)
    if p:
        c = (t["in"] * p.get("in", 0) + t["out"] * p.get("out", 0)
             + t["cache_read"] * p.get("cache_read", 0) + t["cache_write"] * p.get("cache_write", 0)) / 1e6
        grand += c
        cost = f"{c:.4f}"
    print(f"{m:24} {t['in']:>10} {t['out']:>10} {t['cache_read']:>12} {t['cache_write']:>12} {cost:>10}")
if grand:
    print(f"\ntotal cost: ${grand:.4f}")
else:
    print("\n(no prices.json — tokens only; add prices for the readout)")
