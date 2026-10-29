# One expression payload and window-lock validation

The one-slot representation is retained. `Expr` now stores all kind-specific
payloads in `value`, removing the separate `node` pointer. On this target it
shrinks Expr, Condition and WriteValue from 48 to 40 bytes, Assignment from 144
to 128 bytes, and projection.Column[string] from 64 to 56 bytes. Representative
construction allocates fewer bytes without adding allocations.

The final window-lock correctness fix is also retained, with one statement
cleanup defer. Its CPU regression bound remains **unresolved on three of four
workloads**. Correctness justifies retaining the guard; these measurements do
not establish a CPU improvement or a no-regression guarantee. Every measured
warm path retains zero bytes and zero allocations per operation.

## Representation, behavior and ownership

Binary and subscript expressions store `*expression` in the same payload slot
already used by unary and other expression kinds. Each consumer extracts the
typed pointer once. Assignment target normalization copies the payload before
rebuilding it. Pattern Escape decomposition and both clone-policy maps follow
the same representation; generated clone walkers recurse through both children.
Expression kinds, diagnostics, traversal depth and public behavior are preserved.
There is no cached or parallel expression representation.

Root, projection and clone-generator tests passed before timing. The new
binary/subscript fixture checks both the original and Clone against fixed SQL
and arguments outside timed loops. Existing tests cover Escape, target
normalization, live children, clone isolation and depth. Full final verification
belongs to the root task. The final statement-defer revision also passed focused
window, depth, lock and cycle tests before its binary was built.

Owned projection extraction keeps its exact-sized allocation loops. The earlier
append-with-nil wrapper rejected in `../designs/` is unchanged evidence: owned
extraction regressed 9.66% (95% +6.77% to +12.62%) without allocation savings.
That rejects the wrapper, not every possible shared-loop implementation. No
second extraction-loop implementation was investigated in this task.

## Comparisons and decisions

Each stage below retained all ten alternating pairs. Absolute times are medians;
paired effects use geometric mean ratios and Student's t 95% intervals over log
ratios, so median ratios can differ. Stages use different sample durations and
are not pooled. Benchstat output is supplementary, with a different comparison
method. The host showed substantial variability; no telemetry identifies its
cause. Task-owned competing builds, tests and PostgreSQL were excluded.

### Initial one-slot comparison

The baseline includes all earlier project changes, including projection.Concat.
The isolated candidate contains only the one-slot production changes. The
13-case, 250 ms comparison meets the allocation objective. Its binary/subscript
warm CPU interval reaches +8.60%, so the original +5% upper-bound gate was
inconclusive. All other selected warm-render and Clone upper bounds passed.

| Workload | Median ns/op, baseline → candidate | Paired effect (95% interval) | B/op; allocs/op, baseline → candidate |
|---|---:|---:|---:|
| Queries / CTE / Construct | 2601.50 → 2039.50 | -22.97% (-37.12% to -5.64%) | 2816; 26 → 2560; 26 |
| Queries / CTE / ConstructAndToSQL | 5518.50 → 4775.00 | -17.48% (-34.02% to +3.20%) | 4096; 30 → 3840; 30 |
| Queries / CTE / AppendWarm | 1005.65 → 972.20 | -9.42% (-14.81% to -3.68%) | 0; 0 → 0; 0 |
| Queries / Bulk100 / Construct | 20824.50 → 17875.00 | -21.94% (-36.02% to -4.76%) | 22480; 116 → 20864; 116 |
| Queries / Bulk100 / ConstructAndToSQL | 50029.50 → 44123.00 | -19.12% (-33.79% to -1.20%) | 38864; 128 → 37248; 128 |
| Queries / Bulk100 / AppendWarm | 8197.00 → 7728.50 | -8.11% (-15.12% to -0.51%) | 0; 0 → 0; 0 |
| Projection / Construct | 845.60 → 803.50 | -3.10% (-7.28% to +1.26%) | 896; 9 → 832; 9 |
| Projection / Owned | 372.60 → 344.25 | -6.18% (-11.60% to -0.42%) | 512; 2 → 448; 2 |
| Projection / AppendWarm | 32.92 → 30.62 | -7.43% (-10.22% to -4.55%) | 0; 0 → 0; 0 |
| Projection / AppendSQLWarm | 433.45 → 421.60 | -0.57% (-4.38% to +3.40%) | 0; 0 → 0; 0 |
| BinarySubscript / Construct | 1225.00 → 1066.00 | -16.19% (-25.55% to -5.65%) | 1392; 12 → 1248; 12 |
| BinarySubscript / AppendWarm | 371.05 → 360.05 | -2.34% (-12.18% to +8.60%) | 0; 0 → 0; 0 |
| BinarySubscript / Clone | 1744.50 → 1387.00 | -22.88% (-30.44% to -14.50%) | 1392; 12 → 1248; 12 |

The initial opposing warm values ranged from 296.8 to 508.4 ns on the baseline
and 300.1 to 504.1 ns on the candidate; individual pair effects ranged from
-28.40% to +26.15%. Thermal state, scheduler behavior and desktop activity were
not independently observed; none is assigned as the explanation.

### Bounded opposing confirmation

After the inconclusive first stage, root authorized exactly one follow-up on
binary/subscript AppendWarm: the same frozen binaries, ten alternating pairs,
1 s per sample, with the same +5% upper-bound and zero-allocation gates. Failure
or another inconclusive result would have ended layout work. This longer-sample
comparison passed: median 310.45 → 308.80 ns, paired -5.31%, 95% interval -13.24%
to +3.35%, with zero allocations. It establishes the requested regression bound
for that follow-up, not a CPU speedup. The initial result remains separate.

### Initial cumulative window guard

The next stage compares the one-slot binary with the same source plus the
window-lock correctness fix. It used four warm cases, ten pairs at 250 ms. The
Bulk100 comparison showed a slowdown; the other CPU effects were inconclusive.
None established the +5% upper bound. No samples were discarded.

| Workload | Median ns/op, baseline → candidate | Paired effect (95% interval) | B/op; allocs/op, baseline → candidate |
|---|---:|---:|---:|
| Queries / CTE / AppendWarm | 974.90 → 1005.95 | +3.24% (-1.34% to +8.03%) | 0; 0 → 0; 0 |
| Queries / Bulk100 / AppendWarm | 8058.50 → 8490.00 | +6.96% (+0.23% to +14.14%) | 0; 0 → 0; 0 |
| Projection / AppendSQLWarm | 448.15 → 438.15 | +1.07% (-6.29% to +9.00%) | 0; 0 → 0; 0 |
| BinarySubscript / AppendWarm | 366.50 → 380.25 | -0.02% (-7.43% to +7.97%) | 0; 0 → 0; 0 |

### Revised cumulative window guard

The measured slowdown justified one small implementation revision: merge the
two statement cleanup defers while preserving the cleanup order. Root authorized
a separate fixed ten-pair, 500 ms comparison of the revised window guard against
the same one-slot baseline, with a 60 s timing budget and a hard stop afterward.
This compares the revised candidate with its baseline; it does not prove that
defer bookkeeping caused the previous slowdown.

| Workload | Median ns/op, baseline → candidate | Paired effect (95% interval) | B/op; allocs/op, baseline → candidate |
|---|---:|---:|---:|
| Queries / CTE / AppendWarm | 996.75 → 967.35 | -13.86% (-30.65% to +7.00%) | 0; 0 → 0; 0 |
| Queries / Bulk100 / AppendWarm | 8458.50 → 8040.50 | -14.16% (-33.50% to +10.81%) | 0; 0 → 0; 0 |
| Projection / AppendSQLWarm | 463.60 → 432.85 | -24.74% (-47.21% to +7.29%) | 0; 0 → 0; 0 |
| BinarySubscript / AppendWarm | 404.70 → 365.60 | -16.30% (-31.90% to +2.87%) | 0; 0 → 0; 0 |

Only the binary/subscript upper bound passes in this final comparison. The other
three remain inconclusive. Root explicitly retained the simpler lifecycle cleanup
and the required window-lock validation with this unresolved CPU limitation. No
further measurements were run.

## Fixture correction and source provenance

The first baseline process aborted because the new benchmark's SQL literal
omitted the parentheses around the matrix indirection base. Restoring
`("matrix")[$3][$4]` matches the independently approved oracle. Only the expected
literal changed; no production code or measured operation changed. All three
initial binaries then passed one-iteration fixture validation before the fixed
comparison restarted. Those one-iteration outputs are correctness evidence only.

`failed-fixture-validation.txt`, failed-oracle harness/source manifests and
binary hashes preserve the failed attempt. The existing comparison runner drops
partial stdout when its subprocess fails, so partial timings from that aborted
process were not retained or used. No complete pair had been accepted.

`baseline-manifest.json` and `baseline.patch` freeze the original dirty source.
Measured-source manifests record the identical cleaned harness used on every
revision. Benchmark cleanup reuses existing setup/check helpers and removes
forwarding wrappers; it is applied to both sides, so it is not a comparison
variable. The exact harness is retained in the two `*_test.go.txt` files.

`candidate.patch` applies the one-slot change to that baseline; `cumulative.patch`
adds the initial window guard to the one-slot candidate; `revised.patch` adds
only the single-defer revision. Distinct binaries and all before/after SHA256
hashes are recorded in `metadata.json`. Failed-oracle binaries were renamed,
not overwritten. Final binary build info is retained for every revision.

All binaries used Go 1.27.3, darwin/arm64 on Apple M2 Max, PGO disabled,
GOMAXPROCS/test CPU 1, and the same runtime environment settings. No process-wide
runtime or machine power settings were changed. These are synthetic construction,
rendering and clone workloads, not database execution, production throughput,
concurrency or retained-heap measurements. Struct sizes do not directly predict
heap bytes because allocator size classes also matter.

## Reproduction and artifacts

Build each binary from its recorded source tree:

```sh
GOCACHE=/private/tmp/qs-review-go-cache go test -c -pgo=off -o /private/tmp/qs-payload-review/<revision>.test .
```

`metadata.json` records the exact commands and selectors for all four comparisons.
`raw/`, `binary-confirmation/`, `cumulative/` and `revised/` each retain all twenty
raw process outputs, ten paired records and a summary. Aggregate outputs and
benchstat reports are also retained for each stage. `layout.go.txt` and the
layout outputs record the direct struct-size observation.
