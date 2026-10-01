# Native feature coverage

Coverage has four distinct meanings:

- Native construction tests assert fixed SQL and argument order.
- The pinned PostgreSQL corpus measures construction fidelity across source
  queries and distinct query shapes.
- Tagged integration tests execute selected queries against PostgreSQL.
- Go statement coverage measures exercised implementation branches.

These measures do not establish universal schema validity or equivalent query
plans. Trusted raw SQL is not counted as native feature support.
[POSTGRES.md](POSTGRES.md) owns corpus scope, provenance, denominators and gaps;
[VALIDATION.md](VALIDATION.md) records completed local checks.

## Tested families

| Capability | Native evidence |
|---|---|
| SELECT, projections, subqueries, CTEs, grouping and set operations | Full frozen corpus; select_api_test.go, empty_projection_test.go, grammar_boundary_test.go |
| Ordinary, natural, lateral and derived joins; ON and USING | Full frozen corpus; integration/support_families_test.go, integration/query_forms_test.go |
| INSERT, UPDATE, DELETE, MERGE, conflict handling and RETURNING | Full frozen corpus; typed_test.go, native_composition_test.go, ownership_test.go |
| Conditions, NULL, arrays, ranges, full text and row comparisons | postgres_test.go, typed_test.go, width_depth_test.go, safety_test.go |
| Aggregates, window inheritance, frames and exclusions | Full frozen corpus; window_fidelity_test.go, errors_test.go |
| JSON/JSONB operators, typed extraction and document updates | json_test.go, json_constructor_test.go, integration/postgres_test.go |
| SQL/JSON constructors, aggregates, queries and tables | postgres_test.go, json_constructor_test.go, integration/json_constructor_test.go |
| SQL/XML and XMLTABLE | xml_test.go, integration/xml_test.go |
| Composite fields, expansion and assignment indirection | composite_test.go, integration/composite_test.go |
| SQL grammar expressions, literals and type modifiers | sql_syntax_test.go, literal_test.go, numeric_token_test.go, named_type_test.go |
| EXPLAIN, TRUNCATE and query utility statements | explain_test.go, query_utility_test.go, integration/query_utility_test.go |
| Bounded mutation and nested collections through composition | recipes_test.go |
| Identifier quoting, bound values and placeholder styles | ownership_test.go, render_test.go, fuzz_test.go |
| Ownership, cloning, reuse, depth and atomic errors | construction_ownership_test.go, ownership_test.go, errors_test.go, width_depth_test.go |
| Generic field, tuple, document and assignment contracts | typed_test.go, safety_test.go; external-client compile checks |

The eight packages under `internal/postgrescorpus/` preserve all 28,197 source
occurrences, including 28,097 verified construction cases and 100 gaps. Their
SQL and argument expectations are frozen; ordinary tests run offline.
Integration files require the `postgres` build tag and `QX_TEST_DSN`.

## Boundaries

The library constructs PostgreSQL statements. Connection management, execution,
scanning, schema discovery, migrations and automatic dialect translation belong
to consumers. Composition recipes do not imply automatic SQL emulation.

Generics constrain Go operands and typed assignments. They do not infer database
schemas, SQL nullability, server parameter types or widths hidden by raw SQL.
Known projection and row-width mismatches fail during rendering; explicit typed
tuples cover degrees two through four. PostgreSQL remains authoritative for
schema-dependent semantics and custom function/operator behavior.

Version gates cover selected features; the tested live baseline is PostgreSQL
18.6, rather than a complete supported-version matrix. Remaining corpus gaps
stay in the denominator and are listed in [POSTGRES.md](POSTGRES.md).
