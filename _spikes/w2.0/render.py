#!/usr/bin/env -S uv run --script
# /// script
# dependencies = []
# ///
"""Render the W2.0 ledger tables from the raw files in results/.

Usage: _spikes/w2.0/render.py [results-dir]

Prints three Markdown tables (AC-P2 allocations, the lazy pass of AC-P8,
decode time against the sonic-naive comparator) and exits non-zero when the
(M) and (L) allocation counts differ, when a count exceeds its frozen
budget, or when a raw file did not exit 0.
"""

import re
import statistics
import sys
from pathlib import Path

FROZEN = {
    "result": 4, "type-last": 4, "duplicates": 17, "result-20": 24, "score-flood-mini": 21,
    "escaped-names": 10, "escaped-member-names": 30, "structured-legend": 12,
    "deviation-lone-surrogate": 14, "unknown-answer-type": 1, "parity-big-exp-unknown": 1,
    "no-answers": 0, "structured-legend-flood-1k": 91, "structured-legend-flood-10k": 686,
}
DECODE = re.compile(r"DECODE (\S+)\s+bytes=(\d+)\s+allocs=(\d+)\s+allocBytes=(\d+)\s+budget=(\d+)\s+misses=(\d+)/(\d+)")
LAZY = re.compile(r"LAZY (\S+)\.json\s+members=(\d+)\s+allocs=(\d+)\s+bytes=(\d+)\s+bound=(\d+)")
BENCH = re.compile(r"^Benchmark(Decode|DecodeNaiveSonic)/(\S+?)-\d+\s+\d+\s+([\d.]+) ns/op\s+[\d.]+ MB/s\s+(\d+) B/op\s+(\d+) allocs/op")


def read(path: Path) -> str:
    text = path.read_text()
    if not re.search(r"^# exit 0 at", text, re.M):
        sys.exit(f"{path}: the run did not exit 0")
    return text


def main() -> int:
    out = Path(sys.argv[1] if len(sys.argv) > 1 else Path(__file__).parent / "results")
    decode = {h: {m[0]: m[1:] for m in DECODE.findall(read(out / f"alloc-{h}.txt"))} for h in "ML"}
    lazy = {h: {m[0]: m[1:] for m in LAZY.findall(read(out / f"alloc-codec-{h}.txt"))} for h in "ML"}
    bench: dict[str, dict[tuple[str, str], list[tuple[float, int, int]]]] = {}
    for h in "ML":
        rows: dict[tuple[str, str], list[tuple[float, int, int]]] = {}
        for line in read(out / f"bench-{h}.txt").splitlines():
            if m := BENCH.match(line):
                rows.setdefault((m[1], m[2]), []).append((float(m[3]), int(m[4]), int(m[5])))
        bench[h] = rows
    bad = 0
    if decode["M"] != decode["L"] or lazy["M"] != lazy["L"]:
        print("(M) and (L) allocation counts differ", file=sys.stderr)
        bad = 1

    def naive_allocs(name: str) -> str:
        cells = []
        for h in "ML":
            runs = bench[h].get(("DecodeNaiveSonic", name))
            cells.append(str(statistics.median(r[2] for r in runs)) if runs else "no row")
        return " / ".join(cells)

    print("| Fixture | Body bytes | Frozen budget | Allocations (M) | Allocations (L) | Bytes | Without questions (allocs/bytes) | Naive allocs/op (M / L) |")
    print("| --- | --- | --- | --- | --- | --- | --- | --- |")
    for name in FROZEN:
        m, l = decode["M"][name], decode["L"][name]
        if int(m[1]) > FROZEN[name] or int(l[1]) > FROZEN[name]:
            bad = 1
        print(f"| `{name}` | {m[0]} | {FROZEN[name]} | {m[1]} | {l[1]} | {m[2]} | {m[4]}/{m[5]} | {naive_allocs(name)} |")
    print()
    print("| Fixture | Members iterated | Lazy-pass allocations (M = L) | Bytes | Bound 20 + ⌈members/15⌉ + escaped keys + 1 |")
    print("| --- | --- | --- | --- | --- |")
    for name, (members, allocs, nbytes, bound) in lazy["M"].items():
        if int(allocs) > int(bound):
            bad = 1
        print(f"| `{name}` | {members} | {allocs} | {nbytes} | {bound} |")
    print()
    print("| Fixture | (M) decode | (M) naive | (M) ratio | (L) decode | (L) naive | (L) ratio |")
    print("| --- | --- | --- | --- | --- | --- | --- |")

    def med(h: str, bench_name: str, name: str) -> float | None:
        runs = bench[h].get((bench_name, name))
        return statistics.median(r[0] for r in runs) if runs else None

    def fmt(ns: float | None) -> str:
        if ns is None:
            return "no row"
        for unit, scale in (("ms", 1e6), ("µs", 1e3)):
            if ns >= scale:
                return f"{ns / scale:.4g} {unit}"
        return f"{ns:.4g} ns"

    for name in FROZEN:
        cells = []
        for h in "ML":
            sdk, naive = med(h, "Decode", name), med(h, "DecodeNaiveSonic", name)
            ratio = f"{sdk / naive:.2f}" if sdk and naive else "-"
            cells += [fmt(sdk), fmt(naive), ratio]
        print(f"| `{name}` | " + " | ".join(cells) + " |")
    return bad


if __name__ == "__main__":
    sys.exit(main())
