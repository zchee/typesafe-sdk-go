#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Render the W1.3 ledger tables from the raw files in results/.

Usage: render.py RESULTS_DIR BASE

Reads alloc-{M,L}-base<BASE>.txt (TestAllocPrepare -v) and
bench-{M,L}-base<BASE>.txt (go test -bench -benchmem -count=5) and prints the
Markdown tables of docs/perf/ledger.md's W1.3 section to stdout, so that no
number in them is typed by hand.
"""

import re
import statistics
import sys
from dataclasses import dataclass, field
from pathlib import Path

HOSTS = ("M", "L")

CASES = {
    "c1-sketch": "1: the plan's §5 sketch (noul, choice of 2, score of 3, raw noul)",
    "c2-noul-short": "2: one noul, short text",
    "c3-choice-20x10": "3: 20 choices × 10 options",
    "c4a-score-20x8-text": "4a: 20 scores × 8 text levels",
    "c4b-score-20x8-json": "4b: 20 scores × 8 JSON levels, pretty-printed input",
    "c5-raw-100x3": "5: 100 raw noul × 3 fields (string, float, nested map)",
    "c6-escapes": "6: the sketch, every name and text + U+0000–U+001F, U+2028, U+2029, U+1F600",
    "n8a-array-score": "NIT 8a: 20 raw score, criteria `RawJSON` array (8 pretty levels)",
    "n8a-array-control": "NIT 8a control: the same, type `Score` (no falsiness check)",
    "n8b-map-score": "NIT 8b: 100 raw score, criteria `JSON` nested object (pretty)",
    "n8b-map-control": "NIT 8b control: the same, type `Score` (no falsiness check)",
}

PINNED = {"c1-sketch", "c2-noul-short", "c3-choice-20x10"}

RUNS = re.compile(r"runs of (\S+)\s+mallocs/bytes:((?: \d+/\d+)+)")
RESULT = re.compile(r"(\S+): (\d+) mallocs, (\d+) bytes; prepared (\d+) bytes")
BENCH = re.compile(
    r"^Benchmark(Prepare|FalsyJSON)/(\S+)-\d+\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op"
)


@dataclass
class AllocCase:
    """One case of an alloc file: the five runs and the reported result."""

    runs: list[tuple[int, int]] = field(default_factory=list)
    mallocs: int = 0
    bytes: int = 0
    prepared: int = 0


def load_alloc(path: Path) -> dict[str, AllocCase]:
    """Return per case the five runs, the reported minimum and the length."""
    out: dict[str, AllocCase] = {}
    for line in path.read_text().splitlines():
        if m := RUNS.search(line):
            runs = [
                (int(a), int(b)) for a, b in (r.split("/") for r in m.group(2).split())
            ]
            out.setdefault(m.group(1), AllocCase()).runs = runs
        elif m := RESULT.search(line):
            d = out.setdefault(m.group(1), AllocCase())
            d.mallocs, d.bytes, d.prepared = (int(x) for x in m.group(2, 3, 4))
    return out


def load_bench(path: Path) -> dict[tuple[str, str], list[tuple[float, int, int]]]:
    """Return per (benchmark, case) the samples (ns/op, B/op, allocs/op)."""
    out: dict[tuple[str, str], list[tuple[float, int, int]]] = {}
    for line in path.read_text().splitlines():
        if m := BENCH.match(line):
            key = (m.group(1), m.group(2))
            out.setdefault(key, []).append(
                (float(m.group(3)), int(m.group(4)), int(m.group(5)))
            )
    return out


def fmt_ns(ns: float, digits: int = 4) -> str:
    """Spell a duration with a unit and the given significant digits."""
    if ns >= 1000:
        return f"{ns / 1000:#.{digits}g} µs"
    return f"{ns:#.{digits}g} ns"


def med_spread(samples: list[float]) -> tuple[float, float]:
    """Median and half the min-to-max spread."""
    return statistics.median(samples), (max(samples) - min(samples)) / 2


def cell_time(samples: list[tuple[float, int, int]]) -> str:
    """Median ± half the spread of the ns/op samples, in the median's unit
    and to its decimal places."""
    med, half = med_spread([x[0] for x in samples])
    scale, unit = (1000.0, "µs") if med >= 1000 else (1.0, "ns")
    shown = f"{med / scale:#.4g}".rstrip(".")
    decimals = len(shown.split(".")[1]) if "." in shown else 0
    return f"{shown} {unit} ± {half / scale:.{decimals}f} {unit}"


def num(n: int) -> str:
    """Spell n with a space between groups of three digits, as the ledger does."""
    return f"{n:,}".replace(",", " ")


def one(values: set[int]) -> str:
    """The value when all agree, else the range."""
    if len(values) == 1:
        return num(values.pop())
    return "–".join(map(num, sorted(values)))


# Allocations per call site, from breakdown-M-base<BASE>.txt read against the
# code; main() checks that each case's sites sum to its measured count.
SITES = (
    "fixed: `buf`, `entries`, `*Prepared`",
    "tables: `labels` per choice, `levels` per score",
    "raw map keys: `slices.Sorted(maps.Keys(m))`",
    "`spans` growth",
    "`index` map (> 8 questions)",
    "`seen` map (> 32 questions)",
    "`buf` regrowth",
    "`falsyJSON` copies",
)
BREAKDOWN = {
    "c1-sketch": (3, 2, 4, 0, 0, 0, 0, 0),
    "c2-noul-short": (3, 0, 0, 0, 0, 0, 0, 0),
    "c3-choice-20x10": (3, 20, 0, 0, 4, 0, 0, 0),
    "c4a-score-20x8-text": (3, 20, 0, 0, 4, 0, 0, 0),
    "c4b-score-20x8-json": (3, 20, 0, 9, 4, 0, 0, 0),
    "c5-raw-100x3": (3, 0, 1700, 0, 4, 3, 0, 0),
    "c6-escapes": (3, 2, 4, 0, 0, 0, 3, 0),
    "n8a-array-score": (3, 0, 120, 0, 4, 0, 5, 120),
    "n8a-array-control": (3, 0, 120, 0, 4, 0, 5, 0),
    "n8b-map-score": (3, 0, 600, 0, 4, 3, 5, 600),
    "n8b-map-control": (3, 0, 600, 0, 4, 3, 5, 0),
}


def main() -> None:
    """Print the tables."""
    results, base = Path(sys.argv[1]), sys.argv[2]
    alloc = {h: load_alloc(results / f"alloc-{h}-base{base}.txt") for h in HOSTS}
    bench = {h: load_bench(results / f"bench-{h}-base{base}.txt") for h in HOSTS}
    for h in HOSTS:
        missing = set(CASES) - set(alloc[h])
        if missing:
            sys.exit(f"alloc-{h}: missing cases {sorted(missing)}")
    for case in CASES:
        m, ell = alloc["M"][case], alloc["L"][case]
        if (m.mallocs, m.bytes, m.prepared) != (ell.mallocs, ell.bytes, ell.prepared):
            sys.exit(f"{case}: (M) and (L) differ; the tables assume they do not")
        if sum(BREAKDOWN[case]) != m.mallocs:
            sys.exit(
                f"{case}: sites sum to {sum(BREAKDOWN[case])}, measured {m.mallocs}"
            )

    print("#### Allocations and prepared length (W1.3-01, W1.3-04)\n")
    print(
        "| Case | mallocs (M) | mallocs (L) | bytes (M), 5 runs | bytes (L), 5 runs"
        " | prepared bytes | pinned |"
    )
    print("| --- | --- | --- | --- | --- | --- | --- |")
    for case, desc in CASES.items():
        cells = [f"`{case}` {desc}"]
        for h in HOSTS:
            d = alloc[h][case]
            agree = sum(1 for r in d.runs if r[0] == d.mallocs)
            cells.append(f"{num(d.mallocs)} ({agree}/5)")
        for h in HOSTS:
            cells.append(one({r[1] for r in alloc[h][case].runs}))
        cells.append(one({alloc[h][case].prepared for h in HOSTS}))
        cells.append("yes" if case in PINNED else "")
        print("| " + " | ".join(cells) + " |")

    print("\n#### Allocations by call site (identical on (M) and (L); W1.3-03)\n")
    print("| Case | " + " | ".join(SITES) + " | total |")
    print("| --- |" + " --- |" * (len(SITES) + 1))
    for case, sites in BREAKDOWN.items():
        print(
            f"| `{case}` | "
            + " | ".join(num(n) for n in sites)
            + f" | {num(sum(sites))} |"
        )

    print("\n#### Time (median of 5 ± half the min-to-max spread; W1.3-02, W1.3-05)\n")
    print("| Case | (M) | (L) | B/op | allocs/op |")
    print("| --- | --- | --- | --- | --- |")
    for case in CASES:
        s = {h: bench[h][("Prepare", case)] for h in HOSTS}
        bop = one({x[1] for h in HOSTS for x in s[h]})
        aop = one({x[2] for h in HOSTS for x in s[h]})
        print(
            f"| `{case}` | {cell_time(s['M'])} | {cell_time(s['L'])} | {bop} | {aop} |"
        )

    print("\n#### NIT 8: the falsiness check's share (score − control)\n")
    print(
        "| Pair | mallocs | share of mallocs | bytes | share of bytes"
        " | time (M), share | time (L), share |"
    )
    print("| --- | --- | --- | --- | --- | --- | --- |")
    for pair, n in (("n8a-array", 20), ("n8b-map", 100)):
        a_s, a_c = alloc["M"][f"{pair}-score"], alloc["M"][f"{pair}-control"]
        dm = a_s.mallocs - a_c.mallocs
        db = a_s.bytes - a_c.bytes
        cells = [
            f"`{pair}` ({n} questions)",
            f"{num(a_s.mallocs)} − {num(a_c.mallocs)} = {num(dm)} ({dm / n:g} per question)",
            f"{dm / a_s.mallocs:.1%}",
            f"{num(a_s.bytes)} − {num(a_c.bytes)} = {num(db)} ({num(db // n)} per question)",
            f"{db / a_s.bytes:.1%}",
        ]
        for h in HOSTS:
            ts = statistics.median(x[0] for x in bench[h][("Prepare", f"{pair}-score")])
            tc = statistics.median(
                x[0] for x in bench[h][("Prepare", f"{pair}-control")]
            )
            cells.append(
                f"{fmt_ns(ts)} − {fmt_ns(tc)} = {fmt_ns(ts - tc)}"
                f" ({fmt_ns((ts - tc) / n)} per question), {(ts - tc) / ts:.1%}"
            )
        print("| " + " | ".join(cells) + " |")

    print("\n#### NIT 8: `falsyJSON` alone (`BenchmarkFalsyJSON`; W1.3-02, W1.3-05)\n")
    print("| Input | (M) | (L) | B/op | allocs/op |")
    print("| --- | --- | --- | --- | --- |")
    for case in ("small-14B", "array", "map"):
        s = {h: bench[h][("FalsyJSON", case)] for h in HOSTS}
        bop = one({x[1] for h in HOSTS for x in s[h]})
        aop = one({x[2] for h in HOSTS for x in s[h]})
        print(
            f"| `{case}` | {cell_time(s['M'])} | {cell_time(s['L'])} | {bop} | {aop} |"
        )


if __name__ == "__main__":
    main()
