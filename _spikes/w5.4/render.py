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
and sdk's own three values. It then prints K7 under ruling R109: the counted
runs (successful push runs on main from K7_FIRST_RUN on) per CPU model, with
the spread, (largest - smallest) / smallest, of BenchmarkCall/sdk's mean on
each model, then per model and AVX-512 exposure (the amendment proposed
beside R109), and beside it the spread of the sdk/naive mean ratio over the
counted runs of any host. Last, for comparison, both spreads over every run
in the file, in total and per host.
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


def model(run: Run) -> str:
    """Return the run's CPU model name, ruling R109's K7 group.

    Args:
        run: The run's rows.

    Returns:
        The model name the runner reports.
    """
    return run["sdk"]["cpu"]


def host(run: Run) -> str:
    """Return the run's CPU model name plus its AVX-512 exposure.

    One model name covers VMs with and without AVX-512, which run about
    28 % apart, so this is the K7 group proposed beside R109's.

    Args:
        run: The run's rows.

    Returns:
        The model name, with " + AVX-512" when the host exposes avx512f.
    """
    return model(run) + (" + AVX-512" if run["sdk"]["avx512"] == "yes" else "")


def print_spreads(title: str, runs: Iterable[Run], value: Callable[[Run], float]) -> None:
    """Print the spread of value over runs, in total and per host.

    Args:
        title: The quantity's name.
        runs: The runs to take it over.
        value: Returns the quantity for one run.
    """
    by_host: dict[str, list[float]] = {}
    total: list[float] = []
    for run in runs:
        v = value(run)
        total.append(v)
        by_host.setdefault(host(run), []).append(v)
    per_host = "; ".join(f"{h}: {spread(v)}" for h, v in sorted(by_host.items()))
    print(f"- {title}: {spread(total)} ({per_host})")


def print_k7(title: str, runs: list[Run], key: Callable[[Run], str]) -> None:
    """Print K7's count and BenchmarkCall/sdk mean spread per group.

    Args:
        title: The grouping's name.
        runs: The counted runs.
        key: Returns a run's group.
    """
    groups: dict[str, list[float]] = {}
    for r in runs:
        groups.setdefault(key(r), []).append(float(r["sdk"]["mean"]))
    print(f"{title}:")
    for g, v in sorted(groups.items()):
        print(f"- {g}: {len(v)} of 20 runs; BenchmarkCall/sdk mean spread {spread(v)}")


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
        cpu = host(r).removeprefix("AMD EPYC ")
        print(f"| {sdk['commit']} | {sdk['event']} {sdk['branch']} | {g} | {sdk['codspeed_run']} | {cpu} | {ratios} | {values} |")
    counted = [r for g, r in complete.items() if int(g) >= K7_FIRST_RUN and r["sdk"]["event"] == "push" and r["sdk"]["branch"] == "main"]

    def sdk_mean(r: Run) -> float:
        return float(r["sdk"]["mean"])

    def mean_ratio(r: Run) -> float:
        return float(r["sdk"]["mean"]) / float(r["naive"]["mean"])

    print()
    print(f"K7, successful push runs on main from {K7_FIRST_RUN} on; blocking needs 20 runs in one group")
    print("with BenchmarkCall/sdk's mean within 5 %.")
    print_k7("Per CPU model (ruling R109 as written)", counted, model)
    print_k7("Per CPU model and AVX-512 exposure (proposed amendment)", counted, host)
    ratio_spread = spread([mean_ratio(r) for r in counted])
    print(f"Beside it, the sdk/naive mean ratio over those runs, any host (no threshold set): {ratio_spread}")
    print()
    print("Every run above, K7 or not, for comparison:")
    print_spreads("BenchmarkCall/sdk mean", complete.values(), sdk_mean)
    print_spreads("sdk/naive mean ratio", complete.values(), mean_ratio)
    return 1 if len(complete) != len(runs) else 0


if __name__ == "__main__":
    sys.exit(main())
