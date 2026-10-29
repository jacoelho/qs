# Project review and implementation plan

Reviewed 2026-10-29 against `cd8bf94`. qs owns query construction and rendering;
applications own execution, transactions, encoding and query plans. The review
covered public composition, rendering/ownership checks, existing tests, and the
production query patterns below. It is not an exhaustive audit or a migration
of the sampled applications.

## Production evidence

GitHub popularity is rounded, observed on the review date. Links pin the inspected
source, independently of later changes to the repositories.

| Project | Stars | Inspected pattern | Existing qs composition |
|---|---:|---|---|
| [River](https://github.com/riverqueue/river) | 5.8k | [Job claiming](https://github.com/riverqueue/river/blob/5c2d873192b4fb41c4b1320341f28408751532d5/riverdriver/riverpgxv5/internal/dbsqlc/river_job.sql#L203-L242) and [array-fed bulk upsert](https://github.com/riverqueue/river/blob/5c2d873192b4fb41c4b1320341f28408751532d5/riverdriver/riverpgxv5/internal/dbsqlc/river_job.sql#L278-L336) | CTE, SKIP LOCKED, UPDATE FROM; UNNEST, ArrayParam, INSERT SELECT, partial conflict inference |
| [Coder](https://github.com/coder/coder) | 17.2k | [Paginate sessions before correlated lateral enrichment](https://github.com/coder/coder/blob/1acacc9c307e5d30e8b85fd4b821770a40422d85/coderd/database/queries/aibridge.sql#L354-L559) | CTE, tuple comparison, LATERAL, aggregate FILTER/order, array indexing and ANY |
| [Temporal](https://github.com/temporalio/temporal) | 24.1k | [Composite-key upsert and dynamic-list deletion](https://github.com/temporalio/temporal/blob/0e9d1cc6ffaa1c05a0096c6ed5dd9e10c3a481fa/common/persistence/sql/sqlplugin/postgresql/execution_maps.go#L23-L54) | ColumnsSlice, ValuesSlice, ConflictColumns, SetAllExcluded and typed In |

These patterns need no new public primitive. Existing recipes already cover
bounded job claiming; the README already explains typed IN and empty-list FALSE.
The new examples adapt query shapes to small independent fixtures, without
copying application SQL or claiming application-specific query-plan performance.

## Findings and selected plan

1. **Correctness:** SELECT rejects row locking whenever a named WINDOW exists,
   including unused definitions. PostgreSQL 18.6 accepted the direct reproducer.
   Permit unused definitions while rejecting structural inline or named OVER
   in the statement being locked, including ORDER BY expressions. Keep nested
   scalar queries and CTEs in separate validation scopes. Retain DISTINCT/GROUP/
   HAVING checks. Opaque SQL, aggregate classification and propagated locks into
   FROM subqueries remain server-owned. Protect the boundary with rendering,
   append rollback and PostgreSQL execution tests.
2. **Bulk-write ergonomics:** teach columnar array inputs with UNNEST and partial
   upserts. Add an executable example and database checks for active conflicts,
   archived duplicates and new rows. Bound arguments scale with columns, but
   data volume still scales with rows. Callers own equal array lengths and batch
   limits; shorter arrays are padded with NULL by PostgreSQL.
3. **Composition coverage:** exercise a page CTE followed by two independent
   lateral aggregates. Check exact parents and aggregate values, including a
   childless parent, to expose pagination and join fan-out errors.
4. **Grouping ergonomics:** every Param occurrence binds separately, even when
   the same Expr is reused. Teach GROUP BY Ordinal(1) for a parameterized SELECT
   expression; verify one binding and independent database group counts.
5. **Rendering performance:** profile existing workloads, then compare chunked
   identifier quoting against the byte-at-a-time implementation. Preserve quote
   escaping and UTF-8/NUL validation. Retain only with measured improvement and
   an opposing quote-dense workload. Evidence belongs in [PERFORMANCE.md](PERFORMANCE.md).

No parameter deduplication or bulk-insert framework is planned: either would add
identity, encoding or lifecycle policy beyond qs's current ownership boundary.
The existing render traversal tracks OVER use per statement; no separate AST
walker or persistent validation cache is needed.

## Validation and review

QA reviewed the behavior and fixtures before test edits. Verification includes
fixed SQL/argument examples, existing identifier fuzzing, root/corpus tests,
race tests, lint, generated-code checks and PostgreSQL integration where locally
available. PostgreSQL 12–18 CI remains the cross-version execution matrix.
Performance measurements run without task-owned tests or a database server.
A separate critic reviews the final diff and verification evidence; material
findings are resolved before completion.

## Outcome

All five items are implemented. The cumulative change set also adds
`projection.Concat`, `Projection.AppendExpressions` and
`Projection.AppendMetadata`, described in the [query designs](QUERY_DESIGNS.md)
and [composition decision](COLLECTION_DESIGN.md). The core query-building API
retains its signatures. The initial lateral
fixture was expanded to two child collections following QA review. Identifier
quoting passed its predeclared performance gate: warm SELECT rendering improved
8.71% (paired 95% interval: 7.91–9.51% improvement), with unchanged allocations.
Construction plus rendering remains inconclusive; no database or production
throughput gain is claimed.

| Check | Result |
|---|---|
| Focused lock regression and executable examples | Pass |
| `make test`, including all corpus shards | Pass |
| `make generate-check` | Pass |
| `make golangci-lint`, root and tagged integration module | Pass, zero issues |
| Full PostgreSQL 18.6 integration suite with `-race -count=1` | Pass, 134 test/subtest completions, no skips |
| Python corpus tooling tests with pinned pglast 8.4 | Pass, 22 tests |
| Identifier fuzzing | Pass, 52,647 executions |
| `make race` and `make vet` | Pass |
| Independent critic | No material issues; reviewed diff and measurement evidence |

The cumulative tree was tested with Go 1.27.3 and a writable temporary Go cache.
The PostgreSQL run used the integration module with
`go test -tags postgres -race -count=1 -p=2 -parallel=8 -timeout=5m -json ./...`.
It includes the unused-window and nested scalar/CTE lock cases, columnar partial
upserts, lateral pagination and parameterized grouping in
`integration/production_patterns_test.go`. PostgreSQL accepted both nested-scope
lock cases; the result rows matched the independent fixture expectations.

gopls reports an unsupported generic method in unchanged `fluent.go` and a stale
reference to the removed node-based clone test. The on-disk compiler, tests and
linter provide semantic verification. The critic inspected code and evidence
without independently executing checks. Identifier fuzz counts above refer to
the original identifier experiment; ordinary suite runs also execute its seeds.

The temporary PostgreSQL server was stopped after testing. PostgreSQL 12–17
execution and large-query/production performance were not measured locally.
