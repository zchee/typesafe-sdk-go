#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Render the W5.4 ledger tables from results/codspeed-call-stats.tsv.

The TSV holds CodSpeed's walltime statistics of BenchmarkCall/sdk and
BenchmarkCall/naive, one line per run and row. The script prints, per run,
AC-P7's sdk/naive ratio by minimum (the value CodSpeed's report shows), by
median and by mean (go test's ns/op, the one AC-P7 reads under ruling R108),
and sdk's own three values. It then prints K7's spread, (largest - smallest)
/ smallest, of BenchmarkCall/sdk's mean and of the sdk/naive mean ratio:
over the counted runs (successful push runs on main from K7_FIRST_RUN on)
and, for comparison, over every run, each in total and per CPU model.
"""

import csv
import sys
from collections.abc import Callable, Iterable
from pathlib import Path

RESULTS = Path(__file__).parent / "results" / "codspeed-call-stats.tsv"
STATS = ("min", "median", "mean")
# K7's count restarts at main's run of aaa9698, the K35 fix (D-K35-land).
K7_FIRST_RUN = 36221839206

Run = dict[str, dict[str, str]]


def load(path: Path) -> list[dict[str, str]]:
    """Return the TSV's data lines as dictionaries keyed by the header.

    Args:
        path: The TSV file; lines starting with '#' are comments.

    Returns:
        One dictionary per data line, in file order.
    """
    with path.open(newline="") as f:
        return list(csv.DictReader((line for line in f if not line.startswith("#")), delimiter="\t"))


def spread(values: list[float]) -> str:
    """Format (largest - smallest) / smallest of values as a percentage.

    Args:
        values: The per-run values.

    Returns:
        The spread with the run count, or a note when there are fewer than two runs.
    """
    if len(values) < 2:
        return f"n/a ({len(values)} run)"
    return f"{(max(values) - min(values)) / min(values) * 100:.2f} % over {len(values)} runs"


def print_spreads(title: str, runs: Iterable[Run], value: Callable[[Run], float]) -> None:
    """Print the spread of value over runs, in total and per CPU model.

    Args:
        title: The quantity's name.
        runs: The runs to take it over.
        value: Returns the quantity for one run.
    """
    by_cpu: dict[str, list[float]] = {}
    total: list[float] = []
    for run in runs:
        v = value(run)
        total.append(v)
        by_cpu.setdefault(run["sdk"]["cpu"], []).append(v)
    per_cpu = "; ".join(f"{cpu}: {spread(v)}" for cpu, v in sorted(by_cpu.items()))
    print(f"- {title}: {spread(total)} ({per_cpu})")


def main() -> int:
    """Print the per-run AC-P7 table and K7's spreads; 1 if a run lacks a row.

    Returns:
        The process exit status.
    """
    runs: dict[str, Run] = {}
    for line in load(RESULTS):
        runs.setdefault(line["gh_run"], {})[line["row"]] = line
    complete = {g: r for g, r in runs.items() if set(r) == {"sdk", "naive"}}
    for g in runs.keys() - complete.keys():
        print(f"run {g}: rows {sorted(runs[g])}, want sdk and naive", file=sys.stderr)
    print("| Commit | Event | GitHub run | CodSpeed run | CPU | sdk/naive min | median | mean | sdk min µs | median µs | mean µs |")
    print("| --- | --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |")
    for g, r in complete.items():
        sdk, naive = r["sdk"], r["naive"]
        ratios = " | ".join(f"{float(sdk[s]) / float(naive[s]):.3f}" for s in STATS)
        values = " | ".join(f"{float(sdk[s]) * 1e6:.3f}" for s in STATS)
        cpu = sdk["cpu"].removeprefix("AMD EPYC ")
        print(f"| {sdk['commit']} | {sdk['event']} {sdk['branch']} | {g} | {sdk['codspeed_run']} | {cpu} | {ratios} | {values} |")
    counted = [r for g, r in complete.items() if int(g) >= K7_FIRST_RUN and r["sdk"]["event"] == "push" and r["sdk"]["branch"] == "main"]

    def sdk_mean(r: Run) -> float:
        return float(r["sdk"]["mean"])

    def mean_ratio(r: Run) -> float:
        return float(r["sdk"]["mean"]) / float(r["naive"]["mean"])

    print()
    print(f"K7: {len(counted)} counted main runs from {K7_FIRST_RUN} on (20 needed)")
    print_spreads("counted runs, BenchmarkCall/sdk mean", counted, sdk_mean)
    print_spreads("counted runs, sdk/naive mean ratio", counted, mean_ratio)
    print_spreads("every run above, BenchmarkCall/sdk mean", complete.values(), sdk_mean)
    print_spreads("every run above, sdk/naive mean ratio", complete.values(), mean_ratio)
    return 1 if len(complete) != len(runs) else 0


if __name__ == "__main__":
    sys.exit(main())
