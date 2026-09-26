#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Render the W5.2 ledger tables from the raw files in results/.

Reads, per host (M) and (L), the files _spikes/s-c1/run.sh wrote:

- ac-p1-{M,L}.txt: TestAllocEncode and TestAllocScratchSequence, -count=5;
- alloc-{M,L}.txt: the other root allocation tests of ci.yaml's list,
  TestLinearityFloodTime and TestResponseCapOverTheWire, -count=5;
- codec-{M,L}.txt: internal/codec's TestEncodeStateAllocations,
  TestLazyPassAllocations and TestNewBodyAllocations, -count=5.

Every allocation series is a "runs of <label> mallocs/bytes: m/b ..." line
that testsupport.StableMin or testsupport.Spread logs, five runs per
invocation. For each series the tables give the number of runs, the minimum,
the maximum and the spread (maximum minus minimum), each taken per counter
over every run of every invocation, as testsupport.Spread takes them over one
invocation's runs, and how many runs equal the minimum.

The series of an exact pin (testsupport.StableMin: three of five runs must
equal the minimum) give the rate p at which a run exceeds its series'
minimum; with runs independent, one invocation of such a series fails when
at least three of its five runs exceed it, with probability
sum_{k=3..5} C(5,k) p^k (1-p)^(5-k). The script prints p per host and that
probability at p and at ruling R104-corr's 1/100.

Usage: render.py [--series] (--series also prints every series' row).
"""

import logging
import math
import re
import sys
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path

logger = logging.getLogger("render")

HERE = Path(__file__).parent
RESULTS = HERE / "results"
HOSTS = ("M", "L")
FILES = ("ac-p1", "alloc", "codec")
RUN = re.compile(r"^=== RUN\s+(?P<test>Test\w+)")
RUNS = re.compile(r"runs of (?P<label>.+?)\s+mallocs/bytes:(?P<runs>(?: \d+/\d+)+)\s*$")
RESULT = re.compile(
    r"^\s+\S+\.go:\d+: (?P<line>(?:ENCODE|SEQ|DECODE|CALL|ITEM|MEM|LINEARITY|WIRE|LOG|JSON|LAZY|TYPED) .*)$"
)
LOAD = re.compile(r"^# load (?:before|after): .*load averages?: *(?P<one>[\d.]+)")


@dataclass
class Series:
    """Every run of one allocation series, in file order."""

    test: str
    label: str
    runs: list[tuple[int, int]] = field(default_factory=list)

    @property
    def least(self) -> tuple[int, int]:
        """The minimum of each counter over every run."""
        return (min(m for m, _ in self.runs), min(b for _, b in self.runs))

    @property
    def most(self) -> tuple[int, int]:
        """The maximum of each counter over every run."""
        return (max(m for m, _ in self.runs), max(b for _, b in self.runs))

    @property
    def above(self) -> int:
        """How many runs differ from the minimum in either counter."""
        return sum(1 for run in self.runs if run != self.least)


def spread_series(s: Series, host: str) -> bool:
    """Report whether the test checks s with testsupport.Spread, not StableMin.

    Args:
        s: the series.
        host: M or L; the arm64 AC-P1 exception is recorded on (M) only.

    Returns:
        True for a series recorded or bounded on every run, False for an
        exact pin.
    """
    if s.test in ("TestMemStatsCap",) or s.label.endswith(" naive"):
        return True
    if s.test == "TestAllocScratchSequence":
        kind = s.label.split(" ", 1)[0]
        return kind in ("pointer-to-struct", "flat-map") or s.label.endswith(" call 1")
    return (
        s.test == "TestAllocEncode"
        and host == "M"
        and s.label == "pointer-to-struct/6MiB"
    )


def parse(path: Path) -> tuple[dict[tuple[str, str], Series], list[str], list[float]]:
    """Return the series, the result lines and the load readings of path.

    Args:
        path: a raw go test -v output file written by _spikes/s-c1/run.sh.

    Returns:
        (test, label) -> Series in first-seen order; every result line; the
        1-minute load averages of the header and the footer.
    """
    series: dict[tuple[str, str], Series] = {}
    results: list[str] = []
    loads: list[float] = []
    test = ""
    for line in path.read_text().splitlines():
        if m := LOAD.match(line):
            loads.append(float(m["one"]))
        if m := RUN.match(line):
            test = m["test"]
            continue
        if m := RUNS.search(line):
            s = series.setdefault((test, m["label"]), Series(test, m["label"]))
            for run in m["runs"].split():
                mallocs, nbytes = run.split("/")
                s.runs.append((int(mallocs), int(nbytes)))
            continue
        if m := RESULT.match(line):
            results.append(m["line"])
    return series, results, loads


def pair(v: tuple[int, int]) -> str:
    """Render a (mallocs, bytes) pair as m/b."""
    return f"{v[0]}/{v[1]}"


def spread(s: Series) -> str:
    """Render a series' spread as +m/+b."""
    lo, hi = s.least, s.most
    return f"+{hi[0] - lo[0]}/+{hi[1] - lo[1]}"


ENC = re.compile(
    r"^ENCODE (?P<kind>[\w.-]+)/(?P<size>\w+)\s+body=(?P<body>\d+)\s+E_sonic=(?P<e>\S+)\s+B=(?P<b>\d)"
    r"\s+sdk=(?P<sdk>\S+)\s+(?:above|max)=\S+\s+scratch=(?P<cap>\d+)\s+warm=\s*(?P<warm>\d+)\s+(?P<verdict>\w+)"
)
DEC = re.compile(
    r"^DECODE (?P<name>\S+)\s+bytes=(?P<bytes>\d+)\s+allocs=(?P<allocs>\d+)\s+allocBytes=\d+\s+budget=(?P<budget>\d+)"
    r"\s+misses=\S+\s+naive=(?P<naive>\S+)\s+ratio=(?P<ratio>\S*)"
)


def encode_table(results: dict[str, dict[str, list[str]]]) -> None:
    """Print AC-P1's single-size rows: every kind and size on both hosts.

    Each cell gives the body encode's count (every invocation's, when they
    differ), E_sonic and B, the range of the scratch the steady state left
    over the invocations, and the verdict.

    Args:
        results: the result lines by host and file.
    """
    rows: dict[tuple[str, str], dict[str, list[re.Match[str]]]] = {}
    for h in HOSTS:
        for line in results[h]["ac-p1"]:
            if m := ENC.match(line):
                rows.setdefault((m["kind"], m["size"]), {x: [] for x in HOSTS})[
                    h
                ].append(m)
    print("| Kind / size | (M) body encode; E_sonic, B; scratch; verdict | (L) |")
    print("| --- | --- | --- |")
    for (kind, size), cell in rows.items():
        out = []
        for h in HOSTS:
            ms = cell[h]
            sdk = ", ".join(dict.fromkeys(m["sdk"] for m in ms))
            e = ", ".join(dict.fromkeys(m["e"] for m in ms))
            caps = [int(m["cap"]) / (1 << 20) for m in ms]
            verdict = ", ".join(dict.fromkeys(m["verdict"] for m in ms))
            out.append(
                f"{sdk}; {e}, {ms[0]['b']}; {min(caps):.2f}–{max(caps):.2f} MiB; {verdict}"
            )
        print(f"| {kind}/{size} | {out[0]} | {out[1]} |")
    print()


def decode_table(results: dict[str, dict[str, list[str]]]) -> None:
    """Print AC-P2's rows: every fixture that decodes, with the naive ratio.

    Args:
        results: the result lines by host and file.
    """
    rows: dict[str, dict[str, str]] = {}
    for h in HOSTS:
        for line in results[h]["alloc"]:
            if m := DEC.match(line):
                cell = rows.setdefault(m["name"], {})
                cell["bytes"], cell["budget"] = m["bytes"], m["budget"]
                cell.setdefault(f"allocs-{h}", m["allocs"])
                cell.setdefault(f"naive-{h}", m["naive"])
                cell.setdefault(f"ratio-{h}", m["ratio"] or "-")
    print(
        "| Fixture | Bytes | Allocations (M) / (L) | Budget | Naive (M) / (L) | Ratio (M) / (L) |"
    )
    print("| --- | ---: | ---: | ---: | ---: | ---: |")
    for name in sorted(rows):
        c = rows[name]
        print(
            f"| {name} | {c['bytes']} | {c['allocs-M']} / {c['allocs-L']} | {c['budget']} "
            f"| {c['naive-M']} / {c['naive-L']} | {c['ratio-M']} / {c['ratio-L']} |"
        )
    print()


def fail3of5(p: float) -> float:
    """Return the probability that at least 3 of 5 runs exceed the minimum."""
    return sum(math.comb(5, k) * p**k * (1 - p) ** (5 - k) for k in range(3, 6))


def main() -> None:
    """Print the W5.2 tables."""
    detail = "--series" in sys.argv[1:]
    data: dict[str, dict[tuple[str, str], Series]] = {h: {} for h in HOSTS}
    results: dict[str, dict[str, list[str]]] = {h: {} for h in HOSTS}
    for h in HOSTS:
        for f in FILES:
            path = RESULTS / f"{f}-{h}.txt"
            if not path.exists():
                logger.error("missing %s", path)
                sys.exit(1)
            found, lines, loads = parse(path)
            data[h].update(found)
            results[h][f] = lines
            print(
                f"{path.name}: {len(found)} series, {len(lines)} result lines, load {' → '.join(f'{x:.2f}' for x in loads)}"
            )
    print()

    print(
        "| Test | Series (M) / (L) | Runs per series | Exact pins: runs above the minimum (M) / (L) | Largest spread (M) / (L) |"
    )
    print("| --- | ---: | ---: | ---: | --- |")
    tests: dict[str, None] = {}
    for h in HOSTS:
        for s in data[h].values():
            tests.setdefault(s.test)
    rate: dict[str, list[int]] = {
        h: [0, 0] for h in HOSTS
    }  # above, runs over exact pins
    for test in tests:
        cells = [f"`{test}`"]
        counts: list[str] = []
        runs: set[int] = set()
        above: list[str] = []
        widest: list[str] = []
        for h in HOSTS:
            ss = [s for s in data[h].values() if s.test == test]
            counts.append(str(len(ss)))
            runs.update(len(s.runs) for s in ss)
            exact = [s for s in ss if not spread_series(s, h)]
            a = sum(s.above for s in exact)
            n = sum(len(s.runs) for s in exact)
            rate[h][0] += a
            rate[h][1] += n
            above.append(f"{a} of {n}")
            w = max(
                ss,
                key=lambda s: (s.most[0] - s.least[0], s.most[1] - s.least[1]),
                default=None,
            )
            widest.append(f"{spread(w)} ({w.label})" if w else "-")
        cells += [
            " / ".join(counts),
            "/".join(str(r) for r in sorted(runs)),
            " / ".join(above),
            " / ".join(widest),
        ]
        print("| " + " | ".join(cells) + " |")
    print()

    print(
        "| Host | Exact-pin runs | Above the minimum | p | P(3 of 5 fail) at p | P(3 of 5 fail) at 1/100 (R104-corr) |"
    )
    print("| --- | ---: | ---: | ---: | ---: | ---: |")
    for h in HOSTS:
        a, n = rate[h]
        p = a / n if n else 0.0
        print(
            f"| ({h}) | {n} | {a} | {p:.5f} | {fail3of5(p):.2e} | {fail3of5(0.01):.2e} |"
        )
    print()

    encode_table(results)
    decode_table(results)

    if detail:
        print(
            "| Test | Series | Kind | (M) runs, min / max / spread, above | (L) runs, min / max / spread, above |"
        )
        print("| --- | --- | --- | --- | --- |")
        keys = list(dict.fromkeys(list(data["M"]) + list(data["L"])))
        for key in keys:
            cells = [f"`{key[0]}`", key[1]]
            kind = (
                "exact"
                if key in data["M"] and not spread_series(data["M"][key], "M")
                else "spread"
            )
            cells.append(kind)
            for h in HOSTS:
                row = data[h].get(key)
                cells.append(
                    f"{len(row.runs)}, {pair(row.least)} / {pair(row.most)} / {spread(row)}, {row.above}"
                    if row
                    else "-"
                )
            print("| " + " | ".join(cells) + " |")
        print()

    for h in HOSTS:
        print(
            f"Result lines, ({h}), first invocation of each distinct line kind and name:\n"
        )
        seen: dict[str, str] = {}
        for f in FILES:
            for line in results[h][f]:
                seen.setdefault(" ".join(line.split()[:2]), line)
        for line in seen.values():
            print(f"    {line}")
        print()

    lin: dict[str, list[str]] = defaultdict(list)
    for h in HOSTS:
        for line in results[h]["alloc"]:
            if line.startswith("LINEARITY time"):
                m = re.search(r"ratio ([\d.]+)", line)
                if m:
                    lin[h].append(m[1])
    print(
        "AC-P8 time ratios per invocation: "
        + "; ".join(f"({h}) {', '.join(lin[h])}" for h in HOSTS)
    )


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(name)s: %(message)s")
    main()
