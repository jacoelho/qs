# Rendering and function construction

Reusable rendering remains allocation-free in the established warm fixtures.
The private owned-argument constructors remove one redundant argument-list
allocation while preserving caller slice ownership. Diagnostics CPU acceptance
remains unestablished against the correctness-fixed baseline; zero allocations
do not establish CPU equivalence.

# Write API and generated clone measurements

This report records the acceptance measurements for concrete write values,
completed INSERT/conflict roles and generated clone traversal. The architecture
entry point is [ARCHITECTURE.md](ARCHITECTURE.md).

## Method

The baseline is the tracked implementation before this change. Equivalent
benchmark fixtures check SQL and argument order before timing. Construction,
reusable rendering and graph copying are separate operations. The workloads
cover ordinary, mixed and bulk INSERTs; tuple assignments; conflict completion;
and flat, nested, shared and cyclic clone graphs, including XML and JSON.

Each comparison alternates baseline/candidate order over ten pairs, with one
second per benchmark, one benchmark CPU and `GOMAXPROCS=1`. A second bounded
ten-pair round is allowed when the first round is inconclusive. Effect sizes are
geometric mean paired ratios; 95% confidence intervals use Student's t on log
ratios. An inconclusive interval does not establish equivalence.

Generator-only copying must retain identical bytes and allocation counts, with
the complete CPU confidence interval inside -2% to +2%. API changes permit at
most 5% CPU overhead on affected operations, with no added per-value allocation,
element-buffer copy or boxing. Reusable rendering must remain allocation-free.

## Results

Clone-only comparison: 20 pairs. Every workload retains exactly the baseline
bytes and allocations. Strict CPU equivalence is established only for nested XML.
The cycle workload is faster with its entire interval below -2%; the remaining
intervals cross an equivalence boundary. This does **not** establish overall
CPU parity. The predeclared additional round is exhausted.

| Clone workload | CPU effect | 95% interval | B/op; allocs/op (unchanged) |
|---|---:|---:|---:|
| flat_xml | -1.82% | -2.84% to -0.80% | 1040; 5 |
| nested_xml | +0.90% | +0.17% to +1.63% | 1728; 6 |
| shared_cte_xml | +1.51% | -0.08% to +3.13% | 1688; 9 |
| flat_json | +1.71% | +0.95% to +2.47% | 888; 5 |
| nested_json | -1.90% | -2.79% to -1.00% | 976; 7 |
| shared_cte_json | +1.36% | +0.43% to +2.29% | 1472; 10 |
| cycle | -4.07% | -5.67% to -2.45% | 624; 3 |
| flat SELECT | -1.54% | -2.71% to -0.35% | 1296; 8 |

The first API candidate used source-first columns and exceeded the 5% CPU limit
on several operations. Its measurements are retained below, rather than counted
as acceptance evidence. Investigation found that parameter validation and direct
write rendering had crossed the compiler's inlining budget. Combining parameter
descriptor rejection with the existing expression/statement boundary restores
parameter inlining; removing a redundant direct-write error guard restores that
helper's inlining. Conflict completion writes directly into the private optional
payload instead of returning a large payload through the generic call.

The final API selects columns before the source. Column selection owns one exactly
sized buffer, which source completions share without copying. This removes growth
allocations in ordinary and bulk column lists. The columns-first comparison used
20 pairs. Every measured operation retains or reduces bytes and allocation counts.
Several CPU intervals cross +5%, so the complete API CPU gate is **not established**.
No samples were removed. The predeclared extra round for these operations is exhausted.
The later assignment-growth correction affects a separate source operation. Fresh
conflict capture also drops its unused append-prefix path; both construction paths
are measured below. Other exhausted workload comparisons are not repeated.

| API workload | CPU effect | 95% interval | B/op baseline → candidate; allocs/op |
|---|---:|---:|---:|
| InsertConstruction/ordinary | +5.75% | -7.73% to +21.21% | 840 → 728; 11 → 10 |
| InsertConstruction/mixed | +16.41% | +3.61% to +30.80% | 824 → 760; 6 → 6 |
| InsertConstruction/bulk | -2.22% | -7.29% to +3.13% | 2320 → 2064; 22 → 20 |
| InsertAppendSQLWarm/ordinary | +0.93% | -2.68% to +4.68% | 0 → 0; 0 → 0 |
| InsertAppendSQLWarm/mixed | +2.46% | -1.77% to +6.87% | 0 → 0; 0 → 0 |
| InsertAppendSQLWarm/bulk | +2.11% | -3.65% to +8.22% | 0 → 0; 0 → 0 |
| TupleUpdateConstruction/bound | +0.54% | -6.83% to +8.49% | 824 → 824; 7 → 7 |
| TupleUpdateConstruction/tuple_default | -3.86% | -6.97% to -0.64% | 824 → 824; 7 → 7 |
| TupleUpdateAppendSQLWarm/bound | -7.48% | -16.73% to +2.79% | 0 → 0; 0 → 0 |
| TupleUpdateAppendSQLWarm/tuple_default | -6.15% | -18.06% to +7.51% | 0 → 0; 0 → 0 |
| ConflictConstruction/single_assignment | -2.01% | -22.17% to +23.38% | 1288 → 1176; 16 → 15 |
| ConflictConstruction/multi_assignment | -2.83% | -23.07% to +22.72% | 1144 → 1032; 12 → 11 |
| ConflictAppendSQLWarm/single_assignment | -6.96% | -25.48% to +16.15% | 0 → 0; 0 → 0 |
| ConflictAppendSQLWarm/multi_assignment | -4.63% | -19.23% to +12.60% | 0 → 0; 0 → 0 |

Mutable assignment INSERTs now use amortized growth. Fresh conflict completion
captures one exact-sized list. This removes the intermediate candidate's repeated
full-prefix copies without adding per-element allocations. Assignment-source
comparison used 20 pairs, including its one additional bounded round. Allocation
counts match the baseline for assignments, with 64 fewer bytes per operation.
Conflict construction saves one allocation and 112 bytes. None of these CPU
intervals establishes compliance with the 5% upper bound.

| Final construction workload | CPU effect | 95% interval | B/op baseline → candidate; allocs/op |
|---|---:|---:|---:|
| InsertAssignmentsConstruction/chained | +10.43% | -5.14% to +28.55% | 2704 → 2640; 5 → 5 |
| InsertAssignmentsConstruction/one_call | -4.74% | -16.99% to +9.32% | 1632 → 1568; 2 → 2 |
| ConflictConstruction/single_assignment | +1.19% | -14.37% to +19.58% | 1288 → 1176; 16 → 15 |
| ConflictConstruction/multi_assignment | +6.47% | -13.21% to +30.62% | 1144 → 1032; 12 → 11 |

The functional API and corpus requirements pass. Full acceptance under the plan
remains unmet: the measured CPU budgets are not established. Allocation savings
and compile-time guarantees do not substitute for those CPU gates. Further
unchanged-candidate timing or outlier removal is outside the declared experiment.

## Bound-write convenience

`Value(value)` replaces the caller spelling `Write(Param(value))` for bound
writes. This comparison uses the completed columns-first implementation above
as its baseline; it isolates the convenience constructor and does not establish
the outstanding acceptance limits against main.

Twenty alternating pairs retain identical bytes and allocations for every
construction workload. Seven reusable-rendering fixtures check fixed SQL and
argument expectations and retain zero bytes and allocations per operation.

| Construction workload | CPU effect | 95% confidence interval | Unchanged bytes / allocations |
|---|---:|---:|---:|
| InsertConstruction/ordinary | +0.60% | -3.16% to +4.51% | 728 / 10 |
| InsertConstruction/mixed | -1.76% | -4.27% to +0.80% | 760 / 6 |
| InsertConstruction/bulk | +6.77% | -0.45% to +14.50% | 2064 / 20 |
| InsertAssignmentsConstruction/chained | +3.03% | -10.20% to +18.21% | 2640 / 5 |
| InsertAssignmentsConstruction/one_call | -0.04% | -9.81% to +10.78% | 1568 / 2 |
| TupleUpdateConstruction/bound | -0.81% | -6.58% to +5.32% | 824 / 7 |
| TupleUpdateConstruction/tuple_default | -0.29% | -3.01% to +2.51% | 824 / 7 |
| ConflictConstruction/single_assignment | +1.07% | -0.93% to +3.10% | 1176 / 15 |
| ConflictConstruction/multi_assignment | +0.59% | -1.88% to +3.12% | 1032 / 11 |

Five workloads establish the 5% overhead limit. Bulk INSERT, chained and
one-call assignments, and bound tuples remain inconclusive. In particular,
bulk INSERT's point estimate exceeds 5%, but its interval includes both no
regression and a material regression. The convenience constructor passes
functional and allocation checks; complete CPU acceptance remains unestablished.
The predeclared additional round is exhausted. No samples were removed.

[Source/binary hashes and validation](performance/value/metadata.json),
[baseline fixtures](performance/value/baseline_bench_test.go.txt),
[all paired samples](performance/value/pairs.json),
[final intervals](performance/value/summary.json),
[first-round intervals](performance/value/first_round_summary.json), and
[raw output](performance/value/raw/) preserve the evidence. The same
[comparison script](performance/write_clone/compare.py) computes the intervals.

## Evidence

The baseline is commit `ea67ecc841775bc93bdf486b5c0ead1ea2304e62`.
Measurements ran with Go 1.27.0 on darwin/arm64, Apple M2 Max.
No task-owned tests, builds, linters or database server ran during timing.
Normal desktop activity remains a source of uncertainty.

Source and binary hashes are recorded in [metadata](performance/write_clone/metadata.json).
The [initial baseline fixture](performance/write_clone/baseline_initial_bench_test.go.txt),
[expanded assignment baseline fixture](performance/write_clone/baseline_bench_test.go.txt)
and [paired comparison script](performance/write_clone/compare.py) preserve the comparisons.
Metadata distinguishes the initial, columns-first and final assignment binaries.
The final assignment comparison does not replace the exhausted API and clone
comparisons or establish their acceptance.
Clone [paired samples](performance/write_clone/clone_pairs.json),
[final summary](performance/write_clone/clone_summary.json), and
[first-round summary](performance/write_clone/clone_first_round_summary.json)
retain effect sizes, intervals, medians and allocation sets.
The rejected first API candidate's [paired samples](performance/write_clone/api_preoptimization_pairs.json)
and [summary](performance/write_clone/api_preoptimization_summary.json) retain
the regressions that prompted the changes. The columns-first [samples](performance/write_clone/api_columns_pairs.json),
[final summary](performance/write_clone/api_columns_summary.json) and
[first-round summary](performance/write_clone/api_columns_first_round_summary.json)
preserve the inconclusive CPU result. The final assignment/conflict
[samples](performance/write_clone/assignment_pairs.json),
[summary](performance/write_clone/assignment_summary.json) and
[first-round summary](performance/write_clone/assignment_first_round_summary.json)
record the final separate comparison.

## Identifier rendering, October 2026

Appending identifier spans instead of individual bytes reduces warm SELECT
rendering time by 8.71% in the measured fixture. Quoting and validation are
unchanged: embedded double quotes are doubled, and empty, NUL-containing or
invalid UTF-8 identifiers fail. No cache, pooled buffer or extra allocation is
introduced.

A three-second CPU profile of `BenchmarkAppendCTEWarm` attributed 33.94% of
sampled CPU cumulatively to `identifierPart`. This supported replacing repeated
single-byte buffer writes with `strings.IndexByte` and bulk appends. Existing
tests passed, including 52,647 executions of `FuzzIdentifier`; its expected SQL
uses `strings.ReplaceAll`, independently of the rendering implementation.

The comparison uses commit `cd8bf9457fcd2bd3c597af0a13667cdc63521329`, Go 1.27.3,
darwin/arm64, Apple M2 Max, PGO disabled, and one benchmark CPU. Both binaries
contain the same additional fixed-SQL benchmark for identifiers with embedded,
adjacent, leading and trailing quotes. Candidate production code differs only
in `identifier.go`. Ten alternating pairs use 250 ms per workload, with no
task-owned builds, tests or PostgreSQL server running during confirmation.
Normal desktop activity remains uncontrolled.

Acceptance required at least a 5% representative rendering improvement,
unchanged allocations, and a paired 95% interval upper bound below 3% regression
for the opposing bulk INSERT and quoted-identifier workloads. The change meets
those criteria. The existing comparison script computes geometric mean paired
ratios and Student's t intervals over log ratios; absolute times below are
medians, so their ratio need not equal the reported paired effect.

| Workload | Baseline → candidate, ns/op | Paired effect | 95% interval |
|---|---:|---:|---:|
| Warm SELECT | 365.50 → 333.45 | -8.71% | -9.51% to -7.91% |
| Warm CTE | 503.40 → 489.35 | -2.40% | -3.33% to -1.47% |
| Ordinary INSERT | 218.25 → 207.35 | -4.56% | -5.23% to -3.89% |
| Mixed INSERT | 245.90 → 224.20 | -8.39% | -9.56% to -7.20% |
| Eight-row INSERT | 699.20 → 698.75 | -0.83% | -2.39% to +0.75% |
| Quoted identifiers | 204.95 → 200.05 | -1.79% | -3.01% to -0.54% |
| Owned ToSQL | 554.40 → 548.15 | -1.18% | -4.35% to +2.10% |
| Construction and ToSQL | 1157.00 → 1153.50 | +0.17% | -2.83% to +3.27% |

All warm paths retain zero bytes and allocations per operation. Owned `ToSQL`
retains 528 bytes and three allocations; construction plus `ToSQL` retains
1824 bytes and eleven allocations. The latter two timing results are
inconclusive; construction plus rendering does not establish a 3% overhead
bound. These small synthetic fixtures establish neither production throughput
nor PostgreSQL execution performance. Large query scaling was not measured.
No samples were removed and no additional timing round was run.

[Metadata and commands](performance/identifiers/metadata.json),
[candidate patch](performance/identifiers/candidate.patch),
[profile attribution](performance/identifiers/profile-top.txt),
[paired results](performance/identifiers/raw/pairs.json),
[paired summary](performance/identifiers/raw/summary.json), and
[benchstat output](performance/identifiers/benchstat.txt) retain the evidence.
The raw directory includes every confirmation run. The separate screening files
are exploratory and are excluded from acceptance.

## Query and projection design comparison

Right-sized unary payloads reduce construction of the eight-column named
projection from 1280 to 896 bytes (30%) and reduce measured CPU time by 18.48%.
Allocation count remains nine. Caller-owned projection extraction removes two
intermediate allocations; extraction followed by `Select` retains the builder's
own copy and saves 512 bytes and two allocations per operation.

The baseline is the **dirty tree after the preceding project review**, including
the identifier-rendering and row-lock fixes. It is not bare HEAD. The
[source manifest](performance/designs/baseline-manifest.json) and
[baseline patch](performance/designs/baseline.patch) identify that state.
Measurements use Go 1.27.3, darwin/arm64, Apple M2 Max, PGO disabled, and one
benchmark CPU. Each comparison has ten alternating pairs at 250 ms per workload,
with task-owned builds, tests and the database stopped. Desktop load remains
uncontrolled. Confidence intervals use the same paired log-ratio method above.

The first candidate made `Expressions` and `Metadata` call append-with-nil. That
regressed owned extraction by 9.66% (95% interval +6.77% to +12.62%), despite
unchanged allocations. It failed the 5% CPU guardrail. Restoring the exact-sized
owned extraction loops removed that regression; the final projection comparison
is a fresh ten-pair run against the original dirty baseline. Short copying loops
remain separate because owned allocation and destination reuse have different
costs. The paired column list remains the sole projection representation.

| Final projection workload | Median ns/op, baseline → final | Paired CPU effect, 95% interval | Bytes / allocations, baseline → final |
|---|---:|---:|---:|
| Construct eight named columns | 525.25 → 429.90 | -18.48%, -20.73% to -16.16% | 1280 / 9 → 896 / 9 |
| Owned expressions and metadata | 205.25 → 203.35 | +1.76%, -0.75% to +4.33% | 512 / 2 unchanged |
| Owned extraction and Select | 439.70 → 438.60 | +2.29%, -0.03% to +4.66% | 1376 / 4 unchanged |
| Append to reused slices | 303.25 → 30.67 | -89.76%, -89.94% to -89.58% | 512 / 2 → 0 / 0 |
| Append and Select | 486.50 → 316.80 | -34.50%, -36.27% to -32.68% | 1376 / 4 → 864 / 2 |
| Render prepared projection | 398.00 → 397.40 | -0.24%, -1.47% to +1.01% | 0 / 0 unchanged |

Baseline append callers use `append(dst, p.Expressions()...)` and the equivalent
metadata call; final callers use the new append methods. These produce equivalent
owned positions, but the baseline materializes an intermediate slice. The
nil-destination append case is retained in the raw results; ordinary
`Expressions()` is already the cheaper baseline spelling when no prefix or reuse
is needed. Absolute times are medians; their ratios need not equal paired effects.

The first comparison also covered CTE queries and 100-row, three-column INSERTs.
Those production paths were unchanged by the final extraction correction:

| Query workload | Median ns/op, baseline → candidate | Paired CPU effect, 95% interval | Bytes / allocations, baseline → candidate |
|---|---:|---:|---:|
| Construct filtered CTE | 1317 → 1315 | -2.10%, -6.95% to +3.01% | 2912 / 26 → 2816 / 26 |
| Construct and render CTE | 2733 → 2728 | +0.56%, -2.47% to +3.69% | 4192 / 30 → 4096 / 30 |
| Render prepared CTE | 861.20 → 853.55 | +0.15%, -2.52% to +2.89% | 0 / 0 unchanged |
| Construct and render 100-row INSERT | 23663.50 → 23695.50 | -1.54%, -3.03% to -0.03% | 38864 / 128 unchanged |
| Render prepared 100-row INSERT | 6837 → 6899.50 | +0.40%, -0.18% to +0.97% | 0 / 0 unchanged |

The selected design meets its allocation targets and 5% CPU upper-bound guards
for owned extraction and warm CTE/projection rendering. CTE construction and
owned extraction do not establish a CPU improvement. INSERT storage was not
changed; its small timing difference does not justify a general speed claim.
These are local synthetic workloads, not production throughput or database
execution measurements.

[Metadata and commands](performance/designs/metadata.json),
[initial paired results](performance/designs/initial/summary.json),
[final projection results](performance/designs/final-projection/summary.json),
[final benchstat output](performance/designs/final-projection-benchstat.txt), and
the complete raw run directories preserve both the rejected and accepted
implementations. Sampled allocation profiles and compiler escape observations
are diagnostic evidence only; the metadata records their provenance limits.


## Projection composition

`projection.Concat` combines existing projections without the caller extracting
expressions and metadata and rebuilding their association. Composing two
prebuilt eight-column projections saves 1024 bytes and three allocations per
operation. The saving remains when composition is followed by owned extraction
and `Select`, including the builder's expression-position copy.

| Workload | Median ns/op, baseline → candidate | Paired CPU effect, 95% interval | Bytes / allocations, baseline → candidate |
|---|---:|---:|---:|
| Compose two projections | 632.65 → 275.35 | -56.25%, -57.79% to -54.66% | 2176 / 4 → 1152 / 1 |
| Compose, extract and Select | 1290.50 → 922.95 | -28.14%, -29.64% to -26.60% | 4704 / 8 → 3680 / 5 |

The baseline is the dirty tree after the unary payload and projection append
changes above. It extracts only the right projection, rebuilds its columns by
index, then calls `left.With`. Both callers prepare source projections outside
timing and start without reusable scratch buffers. This measures cold
composition with equivalent ordered expression/metadata pairs, not rendering
or database execution. It makes no cross-library performance claim.

The allocation target and predeclared downstream CPU upper-bound guard of +5%
passed. The complete comparison used ten alternating pairs at 250 ms per case,
Go 1.27.3 on Apple M2 Max, PGO disabled, and one benchmark CPU. Task-owned builds,
tests and PostgreSQL were stopped; desktop activity remains uncontrolled. No
samples were removed or additional timing rounds run. Confidence intervals use
the paired log-ratio method above.

[Evidence and reproduction](performance/ergonomics/README.md),
[metadata and binary hashes](performance/ergonomics/metadata.json),
[paired results](performance/ergonomics/concat/summary.json), and
[benchstat output](performance/ergonomics/benchstat.txt) retain the comparison.
The broader collection-signature proposal was withdrawn before timing; the
measured harnesses contain only these two projection cases.


## One expression payload and window-lock guard

Removing the separate `Expr.node` pointer leaves one kind-specific payload slot.
On this target Expr, Condition and WriteValue shrink from 48 to 40 bytes;
Assignment shrinks from 144 to 128 bytes and projection.Column[string] from 64
to 56 bytes. Binary/subscript consumers read their typed payload once, and clone
policies traverse the same canonical representation. Normalization copies before
rebuilding a target, preserving ownership.

The baseline is the dirty tree after projection.Concat above. Identical cleaned
benchmark harnesses were compiled into both source snapshots. The initial
13-case comparison used ten alternating pairs at 250 ms, Go 1.27.3 on Apple M2
Max, PGO disabled and one benchmark CPU. Representative allocation reductions
were stable across all samples:

| Construction workload | Bytes / allocations, baseline → one-slot candidate |
|---|---:|
| Filtered CTE | 2816 / 26 → 2560 / 26 |
| 100-row INSERT | 22480 / 116 → 20864 / 116 |
| Eight-column named projection | 896 / 9 → 832 / 9 |
| Binary/subscript query | 1392 / 12 → 1248 / 12 |
| Clone binary/subscript query | 1392 / 12 → 1248 / 12 |

The initial binary/subscript warm CPU guard was inconclusive: -2.34%, 95%
interval -12.18% to +8.60%, exceeding the +5% upper-bound limit. Root then
requested one bounded follow-up using the same binaries and ten alternating
1 s samples. It passed that guard: median 310.45 → 308.80 ns, paired -5.31%,
95% interval -13.24% to +3.35%, with zero allocations. This supports the
regression bound for that follow-up; it does not establish a CPU speedup. Both
stages remain separate in the evidence.

The cumulative window-lock correctness fix was measured separately against the
one-slot candidate. Its first four-case comparison did not establish the +5%
upper bound; the 100-row INSERT showed +6.96% (95% +0.23% to +14.14%). A small
revision merged the statement lifecycle cleanup into one defer, preserving its
order. The separately authorized final comparison used ten pairs at 500 ms:

| Warm workload, one-slot baseline → revised window guard | Median ns/op | Paired effect, 95% interval |
|---|---:|---:|
| Filtered CTE | 996.75 → 967.35 | -13.86%, -30.65% to +7.00% |
| 100-row INSERT | 8458.50 → 8040.50 | -14.16%, -33.50% to +10.81% |
| Projection | 463.60 → 432.85 | -24.74%, -47.21% to +7.29% |
| Binary/subscript query | 404.70 → 365.60 | -16.30%, -31.90% to +2.87% |

The final window guard's CPU bound remains **unresolved on three of four
workloads**. All remain at zero bytes and allocations. The correctness fix and
simpler cleanup are retained with that limitation; no CPU improvement or
no-regression guarantee is claimed. No further timing followed. The observed
variability has no established cause: task-owned competing load was excluded,
but desktop activity, thermal state and scheduling were not independently
measured. Paired effects and median ratios differ, and these stages are not
pooled.

The existing exact-sized owned extraction loops remain. Their earlier
append-with-nil replacement regressed owned extraction; this task did not
repeat or replace that rejected implementation.

[Full sequence and reproduction](performance/payload/README.md),
[metadata and binary hashes](performance/payload/metadata.json),
[initial layout results](performance/payload/raw/summary.json),
[bounded opposing confirmation](performance/payload/binary-confirmation/summary.json),
[initial cumulative results](performance/payload/cumulative/summary.json), and
[final cumulative results](performance/payload/revised/summary.json) retain all
completed pairs. The evidence also records a corrected benchmark SQL literal
and the failed-oracle attempt; production behavior was unchanged by that fix.
