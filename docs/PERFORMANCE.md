# Measured performance

## Modernization measurements

Go 1.27.0, darwin/arm64, Apple M2 Max; measured on 1 October 2026. The targeted
construction fixtures use identical inputs and sinks before and after the
changes. Each ran five times with a 200 ms target. The final complete suite ran
three times with a 200 ms target and uses `testing.B.Loop`.

| Construction operation | Allocations/op before → after | Bytes/op before → after |
|---|---:|---:|
| SELECT fixture | 10 → 8 | 1,488 → 1,296 |
| SELECT fixture plus owned ToSQL | 13 → 11 | 2,016 → 1,824 |
| Extend a window partition | 2 → 1 | 192 → 144 |
| Compare a row with values | 4 → 3 | 312 → 216 |
| Row membership | 3 → 2 | 288 → 192 |
| Extend a condition | 3 → 2 | 312 → 168 |
| XMLForestExpr | 3 → 2 | 672 → 528 |
| Capture an XML element | 3 → 1 | 512 → 384 |
| Extend and capture a JSON array | 6 → 3 | 680 → 328 |
| Qualify a TableIdent column | 4 → 2 | 168 → 72 |
| Mixed JSON/XML query | 36 → 34 | 3,976 → 3,848 |

SELECT reserves known batches before conversion. Immutable descriptors copy
directly into combined storage and safely share their private lists when
captured. Row and predicate construction consume their freshly owned expression
lists, avoiding a second defensive copy. These changes preserve caller-list
ownership, independent branches and explicit Clone behavior.

Reusable SELECT, CTE, typed JSON and mixed JSON/XML rendering still report zero
allocations and zero bytes per operation. Owned ToSQL costs are unchanged:
three allocations / 528 bytes for SELECT, four / 848 for typed JSON and five /
1,504 for the mixed fixture. An attempted bulk identifier/string escaping change
was removed because the rendering benchmarks did not justify its complexity.
No general rendering latency improvement is claimed.

Evidence: [construction before](validation/modernization-construction-before.txt),
[construction after](validation/modernization-construction-after.txt),
[SELECT baseline](validation/modernization-baseline.txt), and
[final complete suite](validation/modernization-benchmarks.txt). Timing variation
and the SELECT benchmark's migration from `b.N` to `b.Loop` make allocation
counts the primary comparison.

## Earlier code review recheck

The complete benchmark suite passed three repetitions on Go 1.27.0,
darwin/arm64, Apple M2 Max on 1 October 2026. Reusable SELECT, CTE, typed JSON
and mixed JSON/XML fixtures still report zero allocations and zero bytes per
operation. The mixed query now constructs with 36 allocations and 3,976 bytes;
owned ToSQL output uses five allocations and 1,504 bytes. Defined enum fields
reduce the construction footprint without adding renderer allocation.

[Raw recheck results](validation/code-review-benchmarks.txt) include all
fixtures. Fuzz and corpus checks ran concurrently, so their timings are not a
controlled comparison with the earlier measurements below.

## Earlier typed JSON measurements

Go 1.27.0, darwin/arm64, Apple M2 Max; three 150 ms runs on 1 October 2026.
The fixture selects a JSON path and combines key existence, containment and a
negative-index text predicate. It uses the same renderer as other queries.

| Operation | Allocations/op | Bytes/op | Observed ns/op range |
|---|---:|---:|---:|
| Prebuilt JSON query, reusable AppendWith storage, Dollar | 0 | 0 | 494–568 |
| Prebuilt JSON query, reusable AppendWith storage, Question | 0 | 0 | 444–447 |
| Construct JSON query | 29 | 2,532 | 876–973 |
| ToSQL on preconstructed JSON query, owned output | 4 | 848 | 645–671 |

`TestJSONReusableAppendAllocations` checks exact SQL and arguments before
measuring the warmed renderer. `TestPlaceholderReusableAppendAllocations`
enforces zero allocations for both styles after checking their SQL and arguments.
Selector construction owns its path expression
list without an unnecessary second copy. Zero allocation applies to successful
rendering with enough buffer capacity and stable, preconstructed bound values.
It does not apply to constructing the query or producing owned output.

## Earlier native syntax composition measurements

The mixed fixture combines a JSON aggregate's ordering/FILTER/window, composite
access, keyword SUBSTRING, a reused scalar query and XMLTABLE namespaces/defaults.
It renders 13 arguments in one traversal. Three 150 ms Go 1.27 runs on the same
host measured:

| Operation | Allocations/op | Bytes/op | Observed ns/op range |
|---|---:|---:|---:|
| Reusable AppendWith, Dollar | 0 | 0 | 683–692 |
| Reusable AppendWith, Question | 0 | 0 | 624–638 |
| Construct mixed query | 36 | 3,992 | 1,329–1,354 |
| ToSQL on preconstructed mixed query | 5 | 1,504 | 931–968 |

`TestAppendWithNativeComposition` checks independently specified SQL and argument
order for both styles before enforcing warmed zero allocations. Raw benchmark
output is in [native-syntax-benchmarks.txt](validation/native-syntax-benchmarks.txt).
Separate [construction/owned-output results](validation/native-syntax-owned-benchmarks.txt)
show their allocation costs. Driver work is outside these measurements.

## Historical delivery measurements

These are **Go 1.23.2 linux/amd64** measurements from the delivery container,
using an external alternate modfile. They are not Go 1.27 results and do not
include network calls, PostgreSQL execution or driver codecs. The exact fixture
is in `benchmark_test.go`; raw output is in `validation/benchmarks.txt`.

Three runs, 150 ms target per benchmark, shared Intel Xeon Platinum 8573C host:

| Fixture | Allocations/op | Bytes/op | Observed ns/op range |
|---|---:|---:|---:|
| Build an existing statement into owned output | 3 | 528 | 1,335–2,237 |
| AppendSQL, prebuilt statement and sufficient reusable buffers | 0 | 0 | 872–987 |
| Construct the benchmark statement | 10 | 1,488 | 1,756–3,418 |
| Construct and Build | 13 | 2,016 | 2,917–3,353 |
| Clone the benchmark statement | 8 | 1,296 | 1,898–2,571 |
| AppendSQL with CTE, prebuilt and preallocated | 0 | 0 | 1,267–1,871 |

Host scheduling makes these timings noisy. The allocation measurements are more
useful here than a single headline latency. No comparative benchmark against
another library was run.

The zero-allocation result requires the AST and bound interface values to have
already been constructed, sufficient destination capacity, and successful
rendering. It does not apply to building expressions, cloning, creating an
owned string, an error path, arbitrary driver argument conversion or every
possible future compiler version. `TestReusableAppendAllocations` in ownership_test.go enforces the warm fixture's allocation budget.

Run on the actual deployment compiler and hardware:

```sh
go test -run='^$' -bench=. -benchmem -count=5 .
```

Practical rules: use structural ordered clauses instead of maps, build stable
metadata once, use ArrayParam when one driver-encoded array is appropriate,
and use AppendSQL only when buffer lifetime can be controlled. Clear old
argument references before shortening/reusing argument slices. Prefer ordinary
ToSQL when ownership simplicity matters more than a small number of allocations.
