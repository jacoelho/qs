import argparse
import json
import math
import os
import re
import statistics
import subprocess
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("baseline")
parser.add_argument("candidate")
parser.add_argument("output")
parser.add_argument("--bench", required=True)
parser.add_argument("--pairs", type=int, default=10)
parser.add_argument("--benchtime", default="1s")
parser.add_argument("--append", action="store_true")
args = parser.parse_args()
output = Path(args.output)
output.mkdir(parents=True, exist_ok=True)
rows = json.loads((output / "pairs.json").read_text()) if args.append else []
pattern = re.compile(r"^(Benchmark\S+)\s+\d+\s+([0-9.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op$")
env = dict(os.environ, GOMAXPROCS="1")
for pair in range(len(rows), len(rows) + args.pairs):
    record = {}
    order = [("baseline", args.baseline), ("candidate", args.candidate)]
    if pair % 2:
        order.reverse()
    for label, binary in order:
        result = subprocess.run(
            [binary, "-test.run=^$", "-test.bench=" + args.bench,
             "-test.benchmem", "-test.benchtime=" + args.benchtime, "-test.cpu=1"],
            env=env, text=True, capture_output=True, check=True,
        )
        (output / f"{pair:02d}-{label}.txt").write_text(result.stdout)
        record[label] = {
            match[1]: [float(match[2]), int(match[3]), int(match[4])]
            for line in result.stdout.splitlines()
            if (match := pattern.match(line))
        }
        if not record[label]:
            raise RuntimeError("no benchmark results: " + result.stdout)
    if record["baseline"].keys() != record["candidate"].keys():
        raise RuntimeError("benchmark names differ")
    rows.append(record)
    (output / "pairs.json").write_text(json.dumps(rows, indent=2) + "\n")
    print(f"completed pair {pair + 1}", flush=True)

critical = {10: 2.262157, 20: 2.093024}.get(len(rows))
if critical is None:
    raise RuntimeError("predeclared confidence calculation supports 10 or 20 pairs")
summary = {}
for name in rows[0]["baseline"]:
    ratios = [math.log(row["candidate"][name][0] / row["baseline"][name][0]) for row in rows]
    effect = statistics.mean(ratios)
    margin = critical * statistics.stdev(ratios) / math.sqrt(len(ratios))
    item = {
        "effect_percent": 100 * math.expm1(effect),
        "ci95_percent": [100 * math.expm1(effect - margin), 100 * math.expm1(effect + margin)],
        "baseline_ns": statistics.median(row["baseline"][name][0] for row in rows),
        "candidate_ns": statistics.median(row["candidate"][name][0] for row in rows),
        "baseline_bytes_allocs": sorted({tuple(row["baseline"][name][1:]) for row in rows}),
        "candidate_bytes_allocs": sorted({tuple(row["candidate"][name][1:]) for row in rows}),
    }
    summary[name] = item
    print(name, json.dumps(item), flush=True)
(output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
