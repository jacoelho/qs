#!/usr/bin/env python3
"""Run boundary probes against an explicit pre-breaking-change qs checkout."""

import argparse
import os
from pathlib import Path
import re
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("baseline", type=Path)
args = parser.parse_args()
baseline = args.baseline.resolve()
source = Path(__file__).with_name("boundary_probes.go.txt").read_text()
if not (baseline / "go.mod").is_file():
    parser.error("baseline must contain the pre-change qs module")

with tempfile.TemporaryDirectory(prefix="qs-boundary-probes-") as directory:
    work = Path(directory)
    # Go's quoted replacement path supports spaces without shell interpolation.
    quoted = '"' + str(baseline).replace('\\', '\\\\').replace('"', '\\"') + '"'
    (work / "go.mod").write_text(
        "module boundary-probes\n\ngo 1.27.0\n\n"
        "require github.com/jacoelho/qs v0.0.0\n\n"
        f"replace github.com/jacoelho/qs => {quoted}\n"
    )
    (work / "main.go").write_text(source)
    env = os.environ.copy()
    env["GOCACHE"] = str(work / "cache")
    subprocess.run(["go", "run", "."], cwd=work, env=env, check=True)

print("\nSource counts (nonblank, noncomment lines, including braces):")
for name in ["callerA", "callerB", "adapterB", "callerC", "adapterC", "callerD", "adapterD"]:
    body = source.split("// BEGIN " + name + "\n")[1].split("// END " + name)[0]
    lines = sum(bool(line.strip()) and not line.lstrip().startswith("//") for line in body.splitlines())
    branches = len(re.findall(r"\bif\b", body))
    splits = len(re.findall(r"\[(?:0|1:)\]", body))
    print(f"{name}: {lines} source lines; {branches} if branches; {splits} first/rest index or slice expressions")
