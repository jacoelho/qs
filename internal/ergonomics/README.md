# Query boundary experiment

These are retained experiment artifacts, not a second production API. The `.go.txt`
suffix keeps the adapter implementations out of package builds.

The four implementations build the same dynamic upsert from column, row, conflict
key and assignment slices:

| Prototype | Caller source lines | Caller branches | Caller first/rest operations | Additional adapter lines |
| --- | ---: | ---: | ---: | ---: |
| A: existing staged API and Slice methods | 7 | 1 | 2 | 0 |
| B: uniform variadic stages | 4 | 0 | 0 | 39 |
| C: validated nonempty collections | 19 | 4 | 0 | 14 |
| D: concrete operation spec | 4 | 0 | 0 | 14 |

Counts are nonblank, noncomment source lines after `gofmt`, including braces.
Shared fixture and runner code is excluded equally. The complete helper bodies
are retained and counted: B moves first/rest adaptation behind a wrapper, it does
not make that implementation disappear. Its wrapper also carries deferred errors
because the old core cannot construct empty conflict keys. These adapter costs
are not estimates of implementing B directly in the core. C copies input at its
validation boundary and the old builder copies again. D has one operation shape;
it does not establish the cost of a complete spec-based SQL API.

These counts describe one caller. They do not measure learning time, subjective
usability, CPU, allocations or compile time.

`boundary-results.txt` records 49 passing executable probes: each design receives
the same independently written SQL/argument oracle, mutations of all four input
slices after construction, nil and empty versions of each collection, an invalid
assignment, and a row-width mismatch. C also rejects zero-value collection
wrappers. Invalid inputs must produce an error without SQL or arguments. The
probes do not establish schema correctness, every grammar transition, live-child
behavior, concurrency safety, or PostgreSQL acceptance.

Reproduce against the frozen checkout that still provides `ColumnsSlice`,
`ValuesSlice`, `DoUpdateSlice` and required-first `ConflictColumns`:

```sh
python3 internal/ergonomics/reproduce.py /private/tmp/qs-ergonomics-review/baseline
```

The script creates an isolated module and build cache in a temporary directory.
It runs correctness probes only. The selected production API needs its own core
tests and performance measurements; these wrappers cannot establish either.

[Primary-source comparisons](sources.md) inform the architecture choice. The
canonical architecture entry point and decision record live outside this
experiment directory.
