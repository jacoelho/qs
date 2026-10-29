# Projection composition measurement

`projection.Concat` removes the caller's expression/metadata extraction and
positional reconstruction when composing existing projections. In this cold
8+8 column fixture it saves 1024 bytes and three allocations per operation,
including when followed by owned extraction and `qs.Select` construction.

| Workload | Median ns/op, baseline → candidate | Paired CPU effect (95% interval) | B/op; allocs/op, baseline → candidate |
|---|---:|---:|---:|
| Concat | 632.65 → 275.35 | -56.25% (-57.79% to -54.66%) | 2176; 4 → 1152; 1 |
| Concat and Select | 1290.50 → 922.95 | -28.14% (-29.64% to -26.60%) | 4704; 8 → 3680; 5 |

The allocation target and predeclared downstream CPU upper-bound guard of +5%
both passed. Ten alternating pairs at 250 ms per case were the complete timing
plan. No samples were removed and no additional timing round was run. Absolute
times are medians; the paired effects use geometric mean ratios and Student's t
95% intervals over log ratios, so the two need not divide to the same result.
Benchstat is supplementary and uses a different comparison method.

## Equivalent callers and lifecycle

Both source projections are prepared before timing. Baseline composition uses
existing public methods: extract only the right projection's expressions and
metadata, rebuild its Columns by index, and call `left.With`. The candidate uses
`projection.Concat(left, right)`. Neither caller starts with reusable scratch
buffers. This is cold composition, not a comparison against a caller maintaining
three scratch buffers across operations.

The ConcatAndSelect case also extracts the combined expressions and metadata and
constructs `qs.Select`, including the builder's own expression-position copy.
Independent fixed SQL and metadata expectations are checked outside the timed
subcases. The resulting ordered expression/metadata pairs are equivalent.
Ownership, empty input and shallow-reference behavior are covered by the
projection package's separate contract tests.

The baseline caller needs an index zip loop and three intermediate buffers; the
candidate caller needs none. Those are source obligations, not a usability score.
These two measured cases do not establish warm rendering, database execution,
retention, larger-list scaling, or cross-library performance.

## Provenance and reproduction

The baseline is the dirty tree after the identifier, unary-payload and projection
append changes, not bare HEAD. `baseline-manifest.json` and `baseline.patch`
identify that starting source. The initially broader query/list API proposal was
withdrawn before timing because weakening required-first signatures conflicted
with the explicit invalid-state prevention requirement. Its original benchmark
fixture is retained as `baseline_benchmark_test.go.txt` but was never timed.

Both measured harnesses were pruned to the two projection cases and differ only
in the composition adapter; their exact source is retained in
`baseline_projection_benchmark_test.go.txt` and
`candidate_projection_benchmark_test.go.txt`. `candidate.patch` is relative to
the frozen baseline with that pruned harness. `candidate-source-manifest.json`
hashes the final Go/module source. Only Concat adds production runtime behavior;
other production changes in this comparison are documentation.

`metadata.json` records the distinct binaries, SHA256 hashes before and after
timing, build settings, original unused binary and fixture hashes, acceptance
criteria and limits. Build info is retained for each measured binary. Go 1.27.3,
darwin/arm64, Apple M2 Max, PGO off, and GOMAXPROCS/test CPU 1 were used. Task-owned
builds, tests, lint and PostgreSQL were stopped during timing. Desktop activity
remains uncontrolled.

Build the baseline and candidate in their respective source directories:

```sh
GOCACHE=/private/tmp/qs-review-go-cache go test -c -pgo=off -o /private/tmp/qs-ergonomics-review/baseline-projection.test .
GOCACHE=/private/tmp/qs-review-go-cache go test -c -pgo=off -o /private/tmp/qs-ergonomics-review/candidate-projection.test .
```

From the candidate source, run the fixed comparison:

```sh
python3 internal/performance/write_clone/compare.py /private/tmp/qs-ergonomics-review/baseline-projection.test /private/tmp/qs-ergonomics-review/candidate-projection.test internal/performance/ergonomics/concat --bench '^BenchmarkArchitectureProjection$' --pairs 10 --benchtime 250ms
```

`concat/` retains every raw sample, `pairs.json` and `summary.json`.
`baseline-concat.txt` and `candidate-concat.txt` aggregate all ten runs;
`benchstat.txt` is generated from those complete aggregates. No diagnostic
profiling was needed for this bounded comparison of removed copies.
