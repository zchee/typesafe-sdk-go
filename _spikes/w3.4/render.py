#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Render the W3.4 ledger tables from the raw files in results/.

Reads, per host (M) and (L):

- alloc-{M,L}-x20.txt: TestAllocWholeCall, TestMemStatsCap and
  TestAllocLoggedCall, 20 invocations of 5 runs each;
- probe-{M,L}.txt: TestW34Probe (probe_test.go.txt), 10 invocations;
- call-M-2.txt and call-L.txt, the rows of record, and call-M.txt, an
  earlier (M) run under more load: BenchmarkCall of internal/benchmark,
  -count=10, with W5.1's call-{M,L}.txt beside them.

Every allocation series is a "runs of <label> mallocs/bytes: m/b ..." line
that testsupport.StableMin or testsupport.Spread logs. For each series the
tables give the number of runs, the minimum, the maximum and the spread
(maximum minus minimum), each taken per counter over every run of every
invocation, as testsupport.Spread takes them over one invocation's runs, and
how many runs equal the minimum. The script exits non-zero when a series'
minimum differs between the hosts, since AC-P6 is frozen from a count both
hosts share.
"""

import logging
import re
import statistics
import sys
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path

logger = logging.getLogger("render")

HERE = Path(__file__).parent
RESULTS = HERE / "results"
W51 = HERE.parent / "w5.1" / "results"
HOSTS = ("M", "L")
CALL_OF_RECORD = {"M": "call-M-2.txt", "L": "call-L.txt"}
CALL_EARLIER = {"M": "call-M.txt", "L": "call-L.txt"}
RUN = re.compile(r"^=== RUN\s+(?P<test>Test\w+)")
RUNS = re.compile(r"runs of (?P<label>.+?)\s+mallocs/bytes:(?P<runs>(?: \d+/\d+)+)\s*$")
BENCH = re.compile(r"^Benchmark(?P<name>\S+?)-\d+\s+\d+\s+(?P<rest>.*)$")
MEASURE = re.compile(r"([\d.]+) (\S+)")


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
    def at_min(self) -> int:
        """How many runs equal the minimum in both counters."""
        return sum(1 for run in self.runs if run == self.least)


def parse_series(path: Path) -> dict[tuple[str, str], Series]:
    """Return every allocation series of path, keyed by (test, label).

    Args:
        path: a raw go test -v output file written by _spikes/s-c1/run.sh.

    Returns:
        (test, label) -> Series, in the order the series first appear.
    """
    out: dict[tuple[str, str], Series] = {}
    test = ""
    for line in path.read_text().splitlines():
        if m := RUN.match(line):
            test = m["test"]
            continue
        if m := RUNS.search(line):
            key = (test, m["label"])
            series = out.setdefault(key, Series(test, m["label"]))
            for run in m["runs"].split():
                mallocs, nbytes = run.split("/")
                series.runs.append((int(mallocs), int(nbytes)))
    return out


def parse_bench(path: Path) -> dict[str, dict[str, list[float]]]:
    """Return every metric of every benchmark in path, by name and unit."""
    out: dict[str, dict[str, list[float]]] = defaultdict(lambda: defaultdict(list))
    for line in path.read_text().splitlines():
        if m := BENCH.match(line):
            for value, unit in MEASURE.findall(m["rest"]):
                out[m["name"]][unit].append(float(value))
    return out


def pair(v: tuple[int, int]) -> str:
    """Render a (mallocs, bytes) pair as m/b."""
    return f"{v[0]}/{v[1]}"


def spread(s: Series) -> str:
    """Render a series' spread as +m/+b."""
    lo, hi = s.least, s.most
    return f"+{hi[0] - lo[0]}/+{hi[1] - lo[1]}"


def ns(v: float) -> str:
    """Format a duration in ns with a unit that keeps four digits."""
    if v >= 1e3:
        return f"{v / 1e3:.3f} µs"
    return f"{v:.1f} ns"


def series_table(data: dict[str, dict[tuple[str, str], Series]], title: str) -> bool:
    """Print one host-by-host table of every series; return whether the minima agree."""
    print(f"{title}\n")
    print("| Test | Series | Runs (M) / (L) | (M) min / max / spread | (L) min / max / spread | At the minimum (M) / (L) |")
    print("| --- | --- | ---: | --- | --- | ---: |")
    agree = True
    for key, sm in data["M"].items():
        sl = data["L"].get(key)
        if sl is None:
            logger.error("series %s is missing on (L)", key)
            sys.exit(1)
        if sm.least != sl.least:
            agree = False
            logger.error("series %s: minimum %s on (M), %s on (L)", key, pair(sm.least), pair(sl.least))
        print(
            f"| `{sm.test}` | {sm.label} | {len(sm.runs)} / {len(sl.runs)} "
            f"| {pair(sm.least)} / {pair(sm.most)} / {spread(sm)} "
            f"| {pair(sl.least)} / {pair(sl.most)} / {spread(sl)} "
            f"| {sm.at_min} / {sl.at_min} |"
        )
    print()
    return agree


def bench_table(files: dict[str, str], ratios_only: bool = False) -> None:
    """Print B5 on both hosts beside W5.1's rows, and AC-P6's time ratios.

    Args:
        files: the raw file of each host, in results/.
        ratios_only: print the ratio table alone.
    """
    now = {h: parse_bench(RESULTS / files[h]) for h in HOSTS}
    then = {h: parse_bench(W51 / f"call-{h}.txt") for h in HOSTS}
    names = [f"Call/{impl}{suffix}" for suffix in ("", "-q20") for impl in ("sdk", "floor", "naive", "naive-json")]
    if ratios_only:
        ratio_table(now, files)
        return
    print(f"B5, {files['M']} (M) and {files['L']} (L) (-count=10), against W5.1's call-{{M,L}}.txt (-count=10):\n")
    print(
        "| Benchmark | (M) ns/op min / median | (M) W5.1 median, W3.4 / W5.1 "
        "| (L) ns/op min / median | (L) W5.1 median, W3.4 / W5.1 | allocs/op (M) / (L) | B/op median (M) / (L) |"
    )
    print("| --- | --- | --- | --- | --- | ---: | ---: |")
    for name in names:
        cells = [f"`{name}`"]
        for h in HOSTS:
            ns_now = now[h][name]["ns/op"]
            ns_then = then[h][name]["ns/op"]
            if not ns_now or not ns_then:
                logger.error("no ns/op samples for %s on (%s)", name, h)
                sys.exit(1)
            med = statistics.median(ns_now)
            med_then = statistics.median(ns_then)
            cells.append(f"{ns(min(ns_now))} / {ns(med)}")
            cells.append(f"{ns(med_then)}, {med / med_then:.3f}")
        cells.append(" / ".join(f"{min(now[h][name]['allocs/op']):.0f}" for h in HOSTS))
        cells.append(" / ".join(f"{statistics.median(now[h][name]['B/op']):.0f}" for h in HOSTS))
        print("| " + " | ".join(cells) + " |")
    print()
    ratio_table(now, files)


def ratio_table(now: dict[str, dict[str, dict[str, list[float]]]], files: dict[str, str]) -> None:
    """Print AC-P6's time ratios of the parsed files, one column per host."""
    print(f"| Ratio | (M) {files['M']} | (L) {files['L']} |\n| --- | ---: | ---: |")
    for suffix, label in (("", "q3"), ("-q20", "q20")):
        for stat, fn in (("median", statistics.median), ("min", min)):
            cells = []
            for h in HOSTS:
                sdk = fn(now[h][f"Call/sdk{suffix}"]["ns/op"])
                naive = fn(now[h][f"Call/naive{suffix}"]["ns/op"])
                cells.append(f"{sdk / naive:.3f}")
            print(f"| {label} `call/sdk` / `call/naive`, {stat} ns/op | {cells[0]} | {cells[1]} |")
        cells = []
        for h in HOSTS:
            sdk = statistics.median(now[h][f"Call/sdk{suffix}"]["ns/op"])
            json = statistics.median(now[h][f"Call/naive-json{suffix}"]["ns/op"])
            cells.append(f"{sdk / json:.3f}")
        print(f"| {label} `call/sdk` / `call/naive-json`, median ns/op | {cells[0]} | {cells[1]} |")
    print()


def main() -> None:
    """Print the W3.4 tables; exit 1 when the hosts' minima differ."""
    alloc = {h: parse_series(RESULTS / f"alloc-{h}-x20.txt") for h in HOSTS}
    probe = {h: parse_series(RESULTS / f"probe-{h}.txt") for h in HOSTS}
    ok = series_table(alloc, "Allocation series, alloc-{M,L}-x20.txt (20 invocations × 5 runs):")
    ok = series_table(probe, "Probe series, probe-{M,L}.txt (10 invocations × 5 runs):") and ok
    bench_table(CALL_OF_RECORD)
    print("The earlier (M) run, under more load:\n")
    bench_table(CALL_EARLIER, ratios_only=True)
    if not ok:
        logger.error("the hosts' minima differ: AC-P6 cannot be frozen from these files")
        sys.exit(1)
    print("Every series' minimum is the same on (M) and (L).")


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(name)s: %(message)s")
    main()
