# Source references

Reviewed 1 October 2026. Upstream URLs and SQL semantics are source references;
they are not evidence that the supplied library has passed live database tests.
Local results and uncompleted checks are recorded in [VALIDATION.md](VALIDATION.md).

## SQL and language contracts

- [Go 1.27 release notes](https://go.dev/doc/go1.27): target language release;
  the delivered generic facade does not require new generic-method syntax.
- [PostgreSQL 18 SELECT grammar](https://www.postgresql.org/docs/18/sql-select.html):
  relation forms, grouping, windows, ordering, fetching and locking.
- [PostgreSQL WITH queries](https://www.postgresql.org/docs/18/queries-with.html):
  recursion, materialisation and data-modifying CTEs.
- [PostgreSQL INSERT](https://www.postgresql.org/docs/18/sql-insert.html),
  [UPDATE](https://www.postgresql.org/docs/18/sql-update.html),
  [DELETE](https://www.postgresql.org/docs/18/sql-delete.html), and
  [MERGE](https://www.postgresql.org/docs/18/sql-merge.html): native mutation
  grammar, conflict clauses, match categories and returning expressions.
- [PostgreSQL comparisons](https://www.postgresql.org/docs/18/functions-comparison.html)
  and [row/array comparisons](https://www.postgresql.org/docs/18/functions-comparisons.html):
  explicit NULL predicates, three-valued logic and lexicographic row comparisons.
- [PostgreSQL SELECT output type resolution](https://www.postgresql.org/docs/18/typeconv-select.html):
  type context at SELECT/CTE boundaries; Go generic types do not supply SQL casts.
- [PostgreSQL aggregate functions](https://www.postgresql.org/docs/18/functions-aggregate.html):
  result types and NULL results on empty input.
- [PostgreSQL JSON functions](https://www.postgresql.org/docs/18/functions-json.html):
  JSONB operators and SQL/JSON query/table forms.
- [PostgreSQL limits](https://www.postgresql.org/docs/18/limits.html): argument cap.
- [PostgreSQL RLS](https://www.postgresql.org/docs/18/ddl-rowsecurity.html): policy
  and role responsibilities belong to the execution environment, not the builder.
- [pgx v5.11.0 release](https://github.com/jackc/pgx/releases/tag/v5.11.0):
  test dependency used with `-tags postgres`, verified locally on PostgreSQL 18.6.

## PostgreSQL regression corpus

PostgreSQL REL_18_STABLE is pinned to
[`630e607397424196a0a3ebb14a5658c2473ddadf`](https://github.com/postgres/postgres/tree/630e607397424196a0a3ebb14a5658c2473ddadf/src/test/regress).
The regression SQL and expected-output sources provide construction fixtures;
they also deliberately contain invalid queries. The development-only
[pglast 8.4 parser](https://github.com/lelit/pglast/releases/tag/v8.4) uses
PostgreSQL 18 grammar. Parsing/reconstruction evidence is separate from live
execution and plan validation.

JSON selector casts follow PostgreSQL's
[operator resolution](https://www.postgresql.org/docs/18/typeconv-oper.html):
an unknown bound parameter can resolve to text, so integer selectors cast to
integer explicitly. JSON document/extraction methods follow the
[JSON operator contracts](https://www.postgresql.org/docs/18/functions-json.html).
Typed EXPLAIN serialization follows the
[PostgreSQL 17 EXPLAIN grammar](https://www.postgresql.org/docs/17/sql-explain.html).
