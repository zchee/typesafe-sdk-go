#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Render the W5.1 ledger tables from the raw benchmark files.

Reads results/{call,bench}-{M,L}.txt (go test -bench output with run.sh's
header) and prints Markdown tables of B1-B3, B5 and B6 with the minimum and
median ns/op per host, B/op and allocs/op, and the ratios the acceptance
criteria read: AC-P6's call/sdk over call/naive and AC-P2's decode
allocations over the naive decode's.
"""

import logging
import re
import statistics
import sys
from collections import defaultdict
from collections.abc import Callable
from pathlib import Path

logger = logging.getLogger("render")

RESULTS = Path(__file__).parent / "results"
HOSTS = ("M", "L")
LINE = re.compile(r"^Benchmark(?P<name>\S+?)-\d+\s+\d+\s+(?P<rest>.*)$")
MEASURE = re.compile(r"([\d.]+) (\S+)")

Samples = dict[str, dict[str, list[float]]]


def parse(path: Path) -> Samples:
    """Return every metric of every benchmark in path, by name and unit.

    Args:
        path: a raw go test -bench output file.

    Returns:
        name -> unit -> samples, in file order.
    """
    out: Samples = defaultdict(lambda: defaultdict(list))
    for line in path.read_text().splitlines():
        m = LINE.match(line)
        if m is None:
            continue
        for value, unit in MEASURE.findall(m["rest"]):
            out[m["name"]][unit].append(float(value))
    return out


def ns(v: float) -> str:
    """Format a duration in ns with a unit that keeps four digits."""
    if v >= 1e6:
        return f"{v / 1e6:.3f} ms"
    if v >= 1e3:
        return f"{v / 1e3:.3f} µs"
    return f"{v:.1f} ns"


def cell(s: Samples, name: str, unit: str, fn: Callable[[list[float]], float] = statistics.median) -> float:
    """Return fn over the samples of name's unit; fail loudly if absent."""
    values = s.get(name, {}).get(unit)
    if not values:
        logger.error("no %s samples for %s", unit, name)
        sys.exit(1)
    return float(fn(values))


def row(data: dict[str, Samples], name: str, extra: str = "") -> str:
    """Return one table row of name on both hosts."""
    cells = [f"`{name}`"]
    for h in HOSTS:
        s = data[h]
        cells.append(f"{ns(cell(s, name, 'ns/op', min))} / {ns(cell(s, name, 'ns/op'))}")
    for h in HOSTS:
        s = data[h]
        cells.append(f"{cell(s, name, 'B/op'):.0f} / {cell(s, name, 'allocs/op', min):.0f}")
    if extra:
        cells.append(" / ".join(f"{cell(data[h], name, extra):.3f}" for h in HOSTS))
    return "| " + " | ".join(cells) + " |"


def header(extra: str = "") -> str:
    """Return the header and rule of a row() table."""
    cols = ["Benchmark", "(M) ns/op min / median", "(L) ns/op min / median", "(M) B/op / allocs", "(L) B/op / allocs"]
    if extra:
        cols.append(f"{extra} (M) / (L)")
    return "| " + " | ".join(cols) + " |\n" + "| " + " | ".join(["---"] + ["---:"] * (len(cols) - 1)) + " |"


def ratio(data: dict[str, Samples], num: str, den: str, unit: str) -> str:
    """Return the median ratio num/den of unit on both hosts."""
    return " / ".join(f"{cell(data[h], num, unit) / cell(data[h], den, unit):.3f}" for h in HOSTS)


def main() -> None:
    """Print the W5.1 tables."""
    call = {h: parse(RESULTS / f"call-{h}.txt") for h in HOSTS}
    bench = {h: parse(RESULTS / f"bench-{h}.txt") for h in HOSTS}

    print("B5, call-{M,L}.txt (-count=10):\n")
    print(header())
    for suffix in ("", "-q20"):
        for impl in ("sdk", "floor", "naive", "naive-json"):
            print(row(call, f"Call/{impl}{suffix}"))
    print()
    print("| Ratio (median ns/op) | (M) / (L) |\n| --- | ---: |")
    for suffix, label in (("", "q3"), ("-q20", "q20")):
        print(f"| {label} sdk / naive (AC-P6) | {ratio(call, 'Call/sdk' + suffix, 'Call/naive' + suffix, 'ns/op')} |")
        print(f"| {label} sdk / naive-json | {ratio(call, 'Call/sdk' + suffix, 'Call/naive-json' + suffix, 'ns/op')} |")
        print(
            f"| {label} allocs sdk / naive | {ratio(call, 'Call/sdk' + suffix, 'Call/naive' + suffix, 'allocs/op')} |"
        )

    print("\nB2, bench-{M,L}.txt (-count=5): decode vs naive, median ns/op and allocs/op:\n")
    print(
        "| Fixture | (M) sdk / sonic-map / json-map | (L) sdk / sonic-map / json-map | allocs (M) sdk / sonic / json | allocs (L) sdk / sonic / json | time sdk/sonic (M) / (L) | allocs sdk/sonic (M) / (L) |"
    )
    print("| --- | --- | --- | --- | --- | ---: | ---: |")
    fixtures = [n.removeprefix("Decode/") for n in bench["M"] if n.startswith("Decode/")]
    for fx in fixtures:
        cells = [f"`{fx}`"]
        names = (f"Decode/{fx}", f"DecodeNaiveSonic/{fx}", f"DecodeNaiveJSON/{fx}")
        present = all(n in bench[h] for n in names for h in HOSTS)
        for h in HOSTS:
            cells.append(" / ".join(ns(cell(bench[h], n, "ns/op")) if n in bench[h] else "–" for n in names))
        for h in HOSTS:
            cells.append(
                " / ".join(f"{cell(bench[h], n, 'allocs/op', min):.0f}" if n in bench[h] else "–" for n in names)
            )
        if present:
            cells.append(ratio(bench, names[0], names[1], "ns/op"))
            cells.append(ratio(bench, names[0], names[1], "allocs/op"))
        else:
            cells += ["no naive row", "no naive row"]
        print("| " + " | ".join(cells) + " |")

    print("\nB1, bench-{M,L}.txt (-count=5):\n")
    print(header())
    for name in sorted(n for n in bench["M"] if n.startswith("EncodeBody/")):
        print(row(bench, name))

    print("\nB3, bench-{M,L}.txt (-count=5):\n")
    print(header())
    for name in ("Assembly/request", "Assembly/prepare-and-request"):
        print(row(bench, name))

    print("\nB6, bench-{M,L}.txt (-count=5):\n")
    print(header("metric"))
    print(row(bench, "Loopback/call", "new-conns"))
    print(row(bench, "Loopback/cold-fanout-64", "conns/op"))

    b46_files = [RESULTS / f"b46-{h}.txt" for h in HOSTS]
    if not all(f.exists() for f in b46_files):
        return
    b46 = {h: parse(RESULTS / f"b46-{h}.txt") for h in HOSTS}
    print("\nB4 at 1ca60e1 (on W3.2's landing), b46-{M,L}.txt (-count=10):\n")
    print(header())
    for name in b46["M"]:
        if name.startswith(("RetryAfter/", "Backoff/")):
            print(row(b46, name))

    print("\nB6 re-run at 1ca60e1 (after b3fd5db's graceful close), b46-{M,L}.txt (-count=10):\n")
    print(header("metric"))
    print(row(b46, "Loopback/call", "new-conns"))
    print(row(b46, "Loopback/cold-fanout-64", "conns/op"))
    print("\n| `Loopback/cold-fanout-64` metric, median | (M) / (L) |\n| --- | ---: |")
    for metric in ("conns/op", "leaders/op", "firstholds/op"):
        print(f"| {metric} | {' / '.join(f'{cell(b46[h], "Loopback/cold-fanout-64", metric):.3f}' for h in HOSTS)} |")
    print("\n| B6 median ns/op, 1ca60e1 / 6afc8a3 | (M) / (L) |\n| --- | ---: |")
    for name in ("Loopback/call", "Loopback/cold-fanout-64"):
        moved = " / ".join(f"{cell(b46[h], name, 'ns/op') / cell(bench[h], name, 'ns/op'):.3f}" for h in HOSTS)
        print(f"| `{name}` | {moved} |")


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    main()
