#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Estimate the raw results a CodSpeed walltime job leaves in its profile folder.

Reads the log of a Benchmarks job (`gh run view <id> --log`) and, per package,
sums the bytes CodSpeed's `testing` overlay (codspeed-go 1.3.0) writes for each
row: one sample per b.Loop iteration, marshalled with json.MarshalIndent, so
each iteration adds a "    <ns>,\\n" element to codspeed_time_per_round_ns and
a "    1,\\n" element to codspeed_iters_per_round. A row whose raw file could
not be written ("failed to write raw results") prints its iteration count on
the next line; it is counted and marked. Risk K35, ledger rows W5.1-22/-23.
"""

import re
import sys
from dataclasses import dataclass

MODULE = "github.com/zchee/typesafe-sdk-go"
ROW = re.compile(r"Z (Benchmark\S+)\s+(\d+)\s+([0-9.]+) ns/op")
SPLIT_ROW = re.compile(r"Z\s*(\d+)\s+([0-9.]+) ns/op")
FAILED = re.compile(r"Z (Benchmark\S+)\s+failed to write raw results")
PKG = re.compile(r"Z pkg: (\S+)")


@dataclass
class Package:
    """Rows, failed rows and estimated raw bytes of one package."""

    rows: int = 0
    failed: int = 0
    raw_bytes: int = 0


def row_bytes(iterations: int, ns_per_op: float) -> int:
    """Return the indented-JSON bytes of one row's two sample arrays.

    Args:
        iterations: The row's iteration count, one sample each.
        ns_per_op: The row's ns/op, which sets the digits of each time sample.

    Returns:
        The estimated size of the row's raw results file, headers excluded.
    """
    digits = len(str(max(1, round(ns_per_op))))
    return iterations * ((4 + digits + 2) + (4 + 1 + 2))


def summarize(path: str) -> dict[str, Package]:
    """Return per-package totals for the Benchmarks job log at path."""
    packages: dict[str, Package] = {}
    package = "."
    failed_row = False
    with open(path, encoding="utf-8", errors="replace") as log:
        for line in log:
            if match := PKG.search(line):
                package = "." + match.group(1).removeprefix(MODULE)
                continue
            if FAILED.search(line):
                failed_row = True
                continue
            if match := ROW.search(line):
                iterations, ns_per_op = int(match.group(2)), float(match.group(3))
            elif failed_row and (match := SPLIT_ROW.search(line)):
                iterations, ns_per_op = int(match.group(1)), float(match.group(2))
            else:
                continue
            totals = packages.setdefault(package, Package())
            totals.rows += 1
            totals.failed += failed_row
            totals.raw_bytes += row_bytes(iterations, ns_per_op)
            failed_row = False
    return packages


def main(paths: list[str]) -> None:
    """Print the per-package summary of every log named on the command line."""
    for path in paths:
        packages = summarize(path)
        print(f"== {path}")
        for name, totals in packages.items():
            gib = totals.raw_bytes / 2**30
            print(f"{name:22s} rows={totals.rows:3d} failed={totals.failed:2d} raw~{gib:5.2f} GiB")
        rows = sum(p.rows for p in packages.values())
        failed = sum(p.failed for p in packages.values())
        raw = sum(p.raw_bytes for p in packages.values())
        print(f"{'total':22s} rows={rows:3d} failed={failed:2d} raw~{raw / 2**30:5.2f} GiB")


if __name__ == "__main__":
    main(sys.argv[1:])
