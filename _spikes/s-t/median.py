#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Aggregate RESULT lines from repeated spike runs (go test -count=N).

Groups lines by every non-numeric field whose value is the same in all runs
of a case (spike, case, variant, ...) and prints, per group, the median and
the range of each numeric field, and the distinct values of every other
field with their counts.

Usage: median.py results/m-st1.txt [more files...]
"""

import shlex
import statistics
import sys
from collections import Counter, defaultdict

GROUP_KEYS = ("spike", "case", "variant", "mode", "scope")


def parse(line: str) -> dict[str, str]:
    """Parse one RESULT line into its key/value pairs."""
    fields: dict[str, str] = {}
    for token in shlex.split(line.removeprefix("RESULT ")):
        key, _, value = token.partition("=")
        fields[key] = value
    return fields


def number(value: str) -> float | None:
    """Return value as a float, or None when it is not a plain number."""
    try:
        return float(value)
    except ValueError:
        return None


def main(paths: list[str]) -> None:
    """Print the per-group aggregation of every RESULT line in paths."""
    groups: dict[tuple[str, ...], list[dict[str, str]]] = defaultdict(list)
    for path in paths:
        with open(path, encoding="utf-8") as handle:
            for line in handle:
                if line.startswith("RESULT "):
                    fields = parse(line.strip())
                    key = tuple(f"{k}={fields[k]}" for k in GROUP_KEYS if k in fields)
                    groups[key].append(fields)
    for key, runs in groups.items():
        print(f"{' '.join(key)} runs={len(runs)}")
        names = [k for k in runs[0] if k not in GROUP_KEYS]
        for name in names:
            values = [run.get(name, "") for run in runs]
            nums = [n for v in values if (n := number(v)) is not None]
            if len(nums) == len(values):
                med = statistics.median(nums)
                print(f"  {name}: median={med:g} min={min(nums):g} max={max(nums):g}")
            else:
                counts = Counter(values)
                shown = "; ".join(f"{n}x {v}" for v, n in counts.most_common())
                print(f"  {name}: {shown}")


if __name__ == "__main__":
    main(sys.argv[1:])
