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
