#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Render the W4.3 ledger tables from the raw files in results/.

Usage: render.py RESULTS_DIR

Reads bench-{M,L}.txt (BenchmarkSD2, -count=5), alloc-{M,L}.txt
(TestSD2Allocs and TestPreparedForFirstCall, -v) and typed-{M,L}.txt
(TestAllocTypedDecode, -v), checks that (M) and (L) agree on every
allocation count, and prints the Markdown tables of docs/perf/ledger.md's
W4.3 section and the section 6.6 verdict to stdout, so that no number in
them is typed by hand. Times are the minimum of the five runs; ratios are
of those minimums.
"""

import re
import sys
from pathlib import Path

HOSTS = ("M", "L")
FIXTURES = ("result.json", "result-20.json", "structured-legend-flood-1k.json")
VARIANTS = (
    "0-DecodeAs",
    "1-reflect-addr",
    "2-unsafe-offset",
    "2h-unsafe-offset-heap",
    "3-reflect-set",
)
BENCH = re.compile(
    r"^BenchmarkSD2/(?P<fixture>[^/]+)/(?P<variant>\S+?)(?:-\d+)?\s+\d+\s+"
    r"(?P<ns>[\d.]+) ns/op\s+(?P<b>\d+) B/op\s+(?P<allocs>\d+) allocs/op"
)
SD2ALLOC = re.compile(r"SD2ALLOC (\S+) fields=(\d+) (.*)$")
FIRST = re.compile(
    r"FIRST (\S+) fields=(\d+) first=(\S+) second=(\S+) byHand=(\S+) "
    r"prepare=(\S+) typedExtra=(\S+) perField=(\S+) warmUp=(\S+)"
)
TYPED = re.compile(
    r"TYPED result bytes=(\d+) answersDecode=(\S+) decodeAs=(\S+) "
    r"systemOne=(\S+) ask=(\S+)"
)


def bench(path: Path) -> dict[tuple[str, str], dict[str, float]]:
    """Return the minimum ns/op and the B/op and allocs/op per case."""
    runs: dict[tuple[str, str], list[tuple[float, int, int]]] = {}
    for line in path.read_text().splitlines():
        m = BENCH.match(line)
        if m:
            key = (m["fixture"], m["variant"])
            runs.setdefault(key, []).append(
                (float(m["ns"]), int(m["b"]), int(m["allocs"]))
            )
    out = {}
    for key, rs in runs.items():
        if len(rs) != 5:
            sys.exit(f"{path}: {key} has {len(rs)} runs, want 5")
        if len({(b, a) for _, b, a in rs}) != 1:
            sys.exit(f"{path}: {key} B/op or allocs/op differ between runs")
        ns = sorted(r[0] for r in rs)
        out[key] = {"min": ns[0], "max": ns[-1], "b": rs[0][1], "allocs": rs[0][2]}
    for f in FIXTURES:
        for v in VARIANTS:
            if (f, v) not in out:
                sys.exit(f"{path}: no {f}/{v}")
    return out


def lines(path: Path, pattern: re.Pattern[str]) -> list[tuple[str, ...]]:
    """Return the groups of every line of path that pattern finds."""
    found = [
        m.groups() for m in map(pattern.search, path.read_text().splitlines()) if m
    ]
    if not found:
        sys.exit(f"{path}: no line matches {pattern.pattern}")
    return found


def ns(x: float) -> str:
    """Format a time in ns/op as benchstat does, to four significant digits."""
    return f"{x:.4g}" if x < 1000 else f"{x / 1000:.4g} µs"


def spread(r: dict[str, float]) -> str:
    """Format a case's minimum and its max/min spread."""
    return f"{ns(r['min'])} (×{r['max'] / r['min']:.3f})"


def row(cells: list[object]) -> str:
    """Format cells as a Markdown table row."""
    return "| " + " | ".join(map(str, cells)) + " |"


def main() -> None:
    results = Path(sys.argv[1])
    b = {h: bench(results / f"bench-{h}.txt") for h in HOSTS}
    alloc = {h: lines(results / f"alloc-{h}.txt", SD2ALLOC) for h in HOSTS}
    first = {h: lines(results / f"alloc-{h}.txt", FIRST) for h in HOSTS}
    typed = {h: lines(results / f"typed-{h}.txt", TYPED) for h in HOSTS}
    for name, per in (("SD2ALLOC", alloc), ("FIRST", first), ("TYPED", typed)):
        if per["M"] != per["L"]:
            sys.exit(
                f"{name} lines differ between (M) and (L):\n{per['M']}\n{per['L']}"
            )
    for f in FIXTURES:
        for v in VARIANTS:
            if b["M"][(f, v)]["allocs"] != b["L"][(f, v)]["allocs"]:
                sys.exit(f"{f}/{v}: allocs/op differ between hosts")

    print("S-D2: min ns/op of 5 (spread max/min), B/op, allocs/op")
    print()
    print("| Fixture | Variant | (M) ns/op | (L) ns/op | B/op | allocs/op |")
    print("| --- | --- | ---: | ---: | ---: | ---: |")
    for f in FIXTURES:
        for v in VARIANTS:
            m, l_ = b["M"][(f, v)], b["L"][(f, v)]
            cells = [f"`{f}`", v, spread(m), spread(l_), m["b"], m["allocs"]]
            print(row(cells))
    print()
    print("Ratios of the minimums")
    print()
    print(
        "| Fixture | Host | (1)/(2) | DecodeAs/(2) | (3)/(2) "
        "| allocation (2h)−(2) | reflect per field ((1)−(2h))/fields |"
    )
    print("| --- | --- | ---: | ---: | ---: | ---: | ---: |")
    fields = {g[0]: int(g[1]) for g in alloc["M"]}
    verdict = {}
    for f in FIXTURES:
        for h in HOSTS:
            r = {v: b[h][(f, v)]["min"] for v in VARIANTS}
            one, zero, two, heap, three = (
                r["1-reflect-addr"],
                r["0-DecodeAs"],
                r["2-unsafe-offset"],
                r["2h-unsafe-offset-heap"],
                r["3-reflect-set"],
            )
            verdict[(f, h)] = (one / two, zero / two)
            cells = [
                f"`{f}`",
                f"({h})",
                f"{one / two:.2f}×",
                f"{zero / two:.2f}×",
                f"{three / two:.2f}×",
                f"{heap - two:.1f} ns",
                f"{(one - heap) / fields[f]:.2f} ns",
            ]
            print(row(cells))
    print()
    print("Section 6.6 verdict: unsafe offsets only if (1) > 2x (2) on both hosts")
    for f in FIXTURES:
        both = all(verdict[(f, h)][0] > 2 for h in HOSTS)
        both0 = all(verdict[(f, h)][1] > 2 for h in HOSTS)
        print(
            f"- {f}: replica (1)/(2) > 2 on both hosts: {both}; "
            f"DecodeAs/(2) > 2 on both hosts: {both0}"
        )
    print()
    print("PreparedFor first call, mallocs/bytes (identical on both hosts)")
    print()
    print(
        "| Type | Fields | First call | Second call | Same set by hand "
        "(builder + Prepare) | Prepare alone | Typed extra | Per field |"
    )
    print("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
    for name, n, first_, second, hand, prep, extra, per, _warm in first["M"]:
        print(row([f"`{name}`", n, first_, second, hand, prep, extra, per]))
    print()
    print(f"Warm-up call (the process's first PreparedFor): {first['M'][0][8]}")
    print()
    print(
        "SD2ALLOC (identical on both hosts):",
        *(" ".join(g) for g in alloc["M"]),
        sep="\n  ",
    )
    print(
        "TYPED (identical on both hosts):",
        *(" ".join(g) for g in typed["M"]),
        sep="\n  ",
    )


if __name__ == "__main__":
    main()
