#!/usr/bin/env -S uv run --script
# /// script
# dependencies = ["pyyaml>=6.0.2"]
# ///
"""Extract ci.yaml's allocation-budget step and a mutant with one name removed."""
import sys
from pathlib import Path

import yaml

wf, out = Path(sys.argv[1]), Path(sys.argv[2])
workflow = yaml.safe_load(wf.read_text())
for job in workflow["jobs"].values():
    for step in job.get("steps", []):
        if step.get("name") == "go test without -race (allocation budgets)":
            run = step["run"]
            (out / "k38-step.sh").write_text(run)
            line = "  TestAllocRequestID       # a logged request id's cost (R103 revert)\n"
            mutant = "".join(l for l in run.splitlines(keepends=True) if "TestAllocRequestID" not in l)
            if mutant == run:
                sys.exit("mutant equals the step")
            (out / "k38-step-mutant.sh").write_text(mutant)
