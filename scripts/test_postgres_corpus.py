#!/usr/bin/env python3
"""Focused invariants for the PostgreSQL corpus measurement tool."""

from __future__ import annotations

import importlib.util
import contextlib
import hashlib
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


_SPEC = importlib.util.spec_from_file_location("postgres_corpus", Path(__file__).with_name("postgres_corpus.py"))
assert _SPEC and _SPEC.loader
postgres_corpus = importlib.util.module_from_spec(_SPEC)
sys.modules[_SPEC.name] = postgres_corpus
_SPEC.loader.exec_module(postgres_corpus)


_MINI_SQL_SHA256 = "802d85f9e5a4eaa02a3db889effc193488c9408b71d8f4988d9789bb8727d655"


class PostgreSQLCorpusTests(unittest.TestCase):
    def test_extractor_handles_strings_and_psql_query_terminators(self) -> None:
        units = postgres_corpus.extract_units("SELECT 'a\\'; SELECT 2;", "qa.sql")
        self.assertEqual(
            [(unit.kind, unit.sql, unit.start, unit.end) for unit in units],
            [("sql", "SELECT 'a\\'", 0, 11), ("sql", "SELECT 2", 13, 21)],
        )

        units = postgres_corpus.extract_units("SELECT 1\n\\g\nSELECT 2;", "qa.sql")
        self.assertEqual(
            [(unit.kind, unit.sql, unit.start, unit.end) for unit in units],
            [("sql", "SELECT 1", 0, 9), ("psql", "\\g", 9, 11), ("sql", "SELECT 2", 12, 20)],
        )
        self.assertFalse(postgres_corpus._has_psql_variable("SELECT 'a:b', '08:00:2b:ff:fe:01:02:04'"))
        self.assertTrue(postgres_corpus._has_psql_variable("SELECT :query_value"))

    def test_explain_option_spellings_stay_distinct(self) -> None:
        # EXPLAIN is no longer rendered, so no spelling equivalence remains.
        for left, right in (
            ("COSTS OFF", "COSTS FALSE"),
            ("VERBOSE", "VERBOSE TRUE"),
            ("COSTS OFF", "COSTS ON"),
        ):
            with self.subTest(left=left, right=right):
                self.assertFalse(self._same(f"EXPLAIN ({left}) SELECT 1", f"EXPLAIN ({right}) SELECT 1"))

    def test_target_is_the_carried_query_without_locations(self) -> None:
        inner = "SELECT a FROM t WHERE a = 1"
        expected = postgres_corpus.parse_sql(inner)[0].stmt
        for sql in (
            inner,
            f"EXPLAIN (COSTS OFF) {inner}",
            f"EXPLAIN ANALYZE CREATE TABLE x AS {inner}",
            f"CREATE TABLE x AS {inner}",
            f"CREATE MATERIALIZED VIEW x AS {inner}",
            f"DECLARE c CURSOR FOR {inner}",
            f"EXPLAIN DECLARE c CURSOR FOR {inner}",
            "SELECT a INTO x FROM t WHERE a = 1",
        ):
            with self.subTest(sql=sql):
                node, tree = postgres_corpus._parse_statements(sql)[0]
                target, target_tree = postgres_corpus.build_target(node, tree)
                self.assertTrue(postgres_corpus.compare_trees(expected, target))
                self.assertEqual(list(target_tree), ["SelectStmt"])
                self.assertNotIn("intoClause", target_tree["SelectStmt"])
                text = json.dumps(target_tree)
                for field in postgres_corpus._LOCATION_FIELDS:
                    self.assertNotIn(f'"{field}"', text)

        # Only a top-level destination is dropped; a nested one stays for the
        # adapter to classify, and the source node is left untouched.
        node, tree = postgres_corpus._parse_statements("INSERT INTO t SELECT 1 INTO f")[0]
        _, target_tree = postgres_corpus.build_target(node, tree)
        self.assertIn("intoClause", target_tree["InsertStmt"]["selectStmt"]["SelectStmt"])
        node, tree = postgres_corpus._parse_statements("SELECT a INTO x FROM t")[0]
        postgres_corpus.build_target(node, tree)
        self.assertIsNotNone(node.intoClause)
        self.assertIn("intoClause", tree["SelectStmt"])

        # A carried statement that is not a query is passed through.
        node, tree = postgres_corpus._parse_statements("EXPLAIN EXECUTE p(1)")[0]
        _, target_tree = postgres_corpus.build_target(node, tree)
        self.assertEqual(list(target_tree), ["ExecuteStmt"])

    def test_default_window_frame_equivalence_is_narrow(self) -> None:
        self.assertTrue(
            self._same(
                "SELECT sum(x) OVER () FROM t",
                "SELECT sum(x) OVER (RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t",
            )
        )
        for left, right in (
            (
                "SELECT sum(x) OVER (ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t",
                "SELECT sum(x) OVER (RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t",
            ),
            (
                "SELECT sum(x) OVER (ORDER BY a) FROM t",
                "SELECT sum(x) OVER (ORDER BY b) FROM t",
            ),
            (
                "SELECT sum(x) OVER (RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t",
                "SELECT sum(x) OVER (RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW EXCLUDE TIES) FROM t",
            ),
            (
                "SELECT sum(x) OVER (w) FROM t",
                "SELECT sum(x) OVER (v) FROM t",
            ),
        ):
            with self.subTest(left=left, right=right):
                self.assertFalse(self._same(left, right))

    def test_shape_and_threshold_helpers(self) -> None:
        parse = postgres_corpus.parse_sql
        self.assertEqual(
            postgres_corpus.shape_id(parse("SELECT 1")[0].stmt),
            postgres_corpus.shape_id(parse("SELECT 2")[0].stmt),
        )
        self.assertNotEqual(
            postgres_corpus.shape_id(parse("SELECT a FROM t")[0].stmt),
            postgres_corpus.shape_id(parse("SELECT b FROM t")[0].stmt),
        )
        self.assertEqual(postgres_corpus._threshold_fraction(90), 0.9)
        self.assertEqual(postgres_corpus._threshold_fraction(0.9), 0.9)
        with self.assertRaises(SystemExit):
            postgres_corpus._threshold_fraction(101)

    def test_fixed_pinned_coverage_uses_immutable_floors_and_denominators(self) -> None:
        floors = postgres_corpus._PINNED_CORPUS_FLOORS
        metrics = dict(floors)
        metrics.update(out_of_scope=floors["total"] - floors["verified"], gap=0, rejected=0)
        self.assertEqual(
            postgres_corpus._fixed_coverage_failures(
                metrics,
                floors["total"],
                commit=postgres_corpus._PINNED_POSTGRES_COMMIT,
                expected_sql_sha256=postgres_corpus._PINNED_SQL_SHA256,
                parser_version=postgres_corpus._PINNED_PGLAST_VERSION,
                parser_postgres_version=postgres_corpus._PINNED_POSTGRES_VERSION,
            ),
            [],
        )
        for field in (
            "verified",
            "planner_verified",
            "distinct_verified_shapes",
            "planner_distinct_verified_shapes",
        ):
            with self.subTest(field=field):
                lowered = dict(metrics)
                lowered[field] -= 1
                failures = postgres_corpus._fixed_coverage_failures(
                    lowered,
                    floors["total"],
                    commit=postgres_corpus._PINNED_POSTGRES_COMMIT,
                    expected_sql_sha256=postgres_corpus._PINNED_SQL_SHA256,
                    parser_version=postgres_corpus._PINNED_PGLAST_VERSION,
                    parser_postgres_version=postgres_corpus._PINNED_POSTGRES_VERSION,
                )
                self.assertTrue(any(field in failure for failure in failures))
        for field in ("total", "planner_total", "distinct_shapes", "planner_distinct_shapes"):
            with self.subTest(field=field):
                lowered = dict(metrics)
                lowered[field] -= 1
                census = floors["total"] if field != "total" else floors["total"] - 1
                failures = postgres_corpus._fixed_coverage_failures(
                    lowered,
                    census,
                    commit=postgres_corpus._PINNED_POSTGRES_COMMIT,
                    expected_sql_sha256=postgres_corpus._PINNED_SQL_SHA256,
                    parser_version=postgres_corpus._PINNED_PGLAST_VERSION,
                    parser_postgres_version=postgres_corpus._PINNED_POSTGRES_VERSION,
                )
                self.assertTrue(any(field in failure or "census total" in failure for failure in failures))

    def test_pinned_coverage_mode_rejects_partial_or_unrendered_runs(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-pinned-mode-") as directory:
            root = self._mini_root(Path(directory))
            args = self._args(root, Path(directory) / "partial.json", minimum_support=None, limit=1)
            args.require_pinned_coverage = True
            with mock.patch.object(postgres_corpus, "_render", side_effect=self._render_all("SELECT 1")):
                self.assertEqual(self._run_quiet(args), 1)
            report = json.loads((Path(directory) / "partial.json").read_text())
            self.assertFalse(report["thresholds"]["passed"])
            self.assertTrue(
                any("complete unbounded corpus" in failure for failure in report["thresholds"]["failures"])
            )

            args = self._args(root, Path(directory) / "unrendered.json", minimum_support=None)
            args.require_pinned_coverage = True
            args.render = False
            self.assertEqual(self._run_quiet(args), 1)
            report = json.loads((Path(directory) / "unrendered.json").read_text())
            self.assertTrue(
                any("requires the Go adapter" in failure for failure in report["thresholds"]["failures"])
            )

    def test_pinned_coverage_mode_compares_baseline_without_export(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-pinned-baseline-") as directory:
            root = self._mini_root(Path(directory))
            _, occurrences, _ = postgres_corpus.census(root)
            for occurrence in occurrences:
                occurrence.status = "verified"
                occurrence.generated_sql = "SELECT 1"
            baseline = Path(directory) / "baseline"
            self._export(baseline, occurrences, root, sql_sha256=_MINI_SQL_SHA256)
            manifest_path = baseline / "manifest.json"
            manifest = json.loads(manifest_path.read_text())
            manifest["revision"] = "c" * 40
            manifest_path.write_text(json.dumps(manifest) + "\n")

            args = self._args(root, Path(directory) / "report.json", minimum_support=None)
            args.require_pinned_coverage = True
            args.baseline_fixtures = baseline
            with mock.patch.object(
                postgres_corpus, "_render", side_effect=self._render_all("SELECT 1")
            ), mock.patch.object(postgres_corpus, "_fixed_coverage_failures", return_value=[]):
                self.assertEqual(self._run_quiet(args), 1)
            report = json.loads((Path(directory) / "report.json").read_text())
            self.assertTrue(
                any("baseline" in failure for failure in report["thresholds"]["failures"])
            )

    def test_canonical_oracles_cover_literals_operands_and_qualification(self) -> None:
        postgres_corpus._canonical_oracles()
        for left, right in (
            ("SELECT 1", "SELECT 2"),
            ("SELECT a = b FROM t", "SELECT b = a FROM t"),
            ("SELECT 1::pg_catalog.numeric", 'SELECT 1::"numeric"'),
            ("SELECT 1::custom.int4", "SELECT 1::custom.integer"),
            ("SELECT (x).a FROM t", "SELECT (x).b FROM t"),
            ("SELECT a[1:2] FROM t", "SELECT a[1:3] FROM t"),
            ("SELECT (a[1])[1] FROM t", "SELECT a[1][1] FROM t"),
            ("SELECT (a[1]).f FROM t", "SELECT a[1].f FROM t"),
            ("SELECT 1::numeric(4,2)", "SELECT 1::numeric(4,3)"),
            ("SELECT (1)::interval(6)", "SELECT (1)::interval year to month"),
            ("SELECT XMLPARSE(DOCUMENT '<x/>')", "SELECT XMLPARSE(CONTENT '<x/>')"),
            (
                "SELECT * FROM XMLTABLE(XMLNAMESPACES('http://x.y' AS zz), "
                "'/zz:rows/zz:row' PASSING '<rows xmlns=\"http://x.y\"><row><a>10</a></row></rows>' "
                "COLUMNS a int PATH 'zz:a')",
                "SELECT * FROM XMLTABLE(XMLNAMESPACES('http://x.y' AS zz), "
                "'/zz:rows/zz:row' PASSING '<rows xmlns=\"http://x.y\"><row><a>10</a></row></rows>' "
                "COLUMNS a int PATH 'zz:b')",
            ),
            (
                "SELECT * FROM XMLTABLE(XMLNAMESPACES('http://x.y' AS zz), "
                "'/zz:rows/zz:row' PASSING '<rows xmlns=\"http://x.y\"><row><a>10</a></row></rows>' "
                "COLUMNS a int PATH 'zz:a')",
                "SELECT * FROM XMLTABLE(XMLNAMESPACES('http://x.y' AS zz), "
                "'/zz:rows/zz:row' PASSING '<rows xmlns=\"http://x.y\"><row><a>10</a></row></rows>' "
                "COLUMNS a int DEFAULT 1 PATH 'zz:a')",
            ),
            (
                "SELECT JSON_QUERY('{\"a\": 1}', '$.a' WITHOUT WRAPPER)",
                "SELECT JSON_QUERY('{\"a\": 1}', '$.a' WITH WRAPPER)",
            ),
            ("SELECT JSON_OBJECT('a': 1 NULL ON NULL)", "SELECT JSON_OBJECT('a': 1 ABSENT ON NULL)"),
            ("SELECT JSON_OBJECT('a': 1 WITH UNIQUE)", "SELECT JSON_OBJECT('a': 1)"),
            ("SELECT JSON_ARRAYAGG(i ORDER BY i) FROM t", "SELECT JSON_ARRAYAGG(i ORDER BY i DESC) FROM t"),
            (
                "SELECT JSON_ARRAYAGG(i) FILTER (WHERE i > 1) FROM t",
                "SELECT JSON_ARRAYAGG(i) FILTER (WHERE i > 2) FROM t",
            ),
            (
                "SELECT JSON_VALUE('{}', '$.a' DEFAULT 'x' ON EMPTY)",
                "SELECT JSON_VALUE('{}', '$.a' ERROR ON EMPTY)",
            ),
            ("SELECT * FROM t", "SELECT a FROM t"),
            ("INSERT INTO t (a[1]) VALUES (1)", "INSERT INTO t (a[2]) VALUES (1)"),
            ("SELECT * FROM ONLY t", "SELECT * FROM t"),
            ("SELECT * FROM t AS x", "SELECT * FROM t AS y"),
            ("EXPLAIN (SERIALIZE) SELECT 1", "EXPLAIN (SERIALIZE TEXT) SELECT 1"),
            (
                "SELECT sum(x) OVER (ROWS UNBOUNDED PRECEDING) FROM t",
                "SELECT sum(x) OVER (ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t",
            ),
            ("SELECT a FROM t ORDER BY a NULLS FIRST", "SELECT a FROM t ORDER BY a NULLS LAST"),
            # Boolean nesting is part of the tree; qs must reproduce it.
            ("SELECT a AND (b AND c) FROM t", "SELECT (a AND b) AND c FROM t"),
            ("SELECT a AND b FROM t", "SELECT b AND a FROM t"),
        ):
            with self.subTest(left=left, right=right):
                self.assertFalse(self._same(left, right))

    def test_adapter_renderings_reparse_to_their_source(self) -> None:
        # Handwritten boundary cases run through the real Go command.  The
        # source ASTs and parameters are the oracle; nothing here trusts the
        # adapter's own account of what it rendered.
        assignment_sql = 'UPDATE t SET "a.b"=$1, "c.d"=DEFAULT, "q""r"=$2'
        verified = (
            # Join forms and aliases.
            "SELECT * FROM t1 CROSS JOIN t2",
            "SELECT * FROM t1 INNER JOIN t2 ON t1.a <> t2.a",
            "SELECT * FROM t1 JOIN t2 USING (a)",
            "SELECT * FROM t1 NATURAL JOIN t2",
            "SELECT * FROM (t1 CROSS JOIN t2) AS x (a, b)",
            # Unconstrained and qualified varchar types retain their names.
            "SELECT 1::varchar",
            "SELECT 1::character varying",
            "SELECT 1::pg_catalog.varchar",
            "SELECT 1::varchar(12)",
            "SELECT ARRAY[[1,2],[3,4]]::varchar[][]",
            'SELECT 1::"varchar"',
            "SELECT 1::custom.varchar",
            # Scalar and tuple IN/NOT IN retain the subquery width.
            "SELECT * FROM t WHERE (a,b) IN (SELECT x,y FROM u)",
            "SELECT * FROM t WHERE (a,b) NOT IN (SELECT x,y FROM u)",
            "SELECT * FROM t WHERE (a,b,c) IN (SELECT x,y,z FROM u)",
            "SELECT * FROM t WHERE ROW(a) IN (SELECT x FROM u)",
            "SELECT * FROM t WHERE ROW(a,b) IN (SELECT x,y FROM u)",
            "SELECT * FROM t WHERE a IN (SELECT x FROM u)",
            # Assignment names are literal components, including dots and quotes.
            assignment_sql,
            'UPDATE t SET ("a.b","q""r")=ROW($1,$2)',
            'UPDATE t SET ("a.b","q""r")=($1,$2)',
            'UPDATE t SET ("a.b","q""r")=(SELECT $1,$2) WHERE c=$3',
            'UPDATE t SET "a.b"[1]=$1, rec."x.y"=$2',
            'MERGE INTO t USING s ON t.id=s.id WHEN MATCHED THEN UPDATE SET "a.b"=$1, "c.d"=DEFAULT',
            # Preserve EXTRACT aliases and every projection and parameter.
            "SELECT EXTRACT(MICROSECOND FROM $1), EXTRACT(MICROSEC FROM $2), "
            "EXTRACT(MILLISECOND FROM $3), EXTRACT(SECONDS FROM $4), "
            "EXTRACT(TIMEZONE_H FROM $5), EXTRACT(TIMEZONE_M FROM $6), "
            "EXTRACT(JULIAN FROM $7), EXTRACT(DAY FROM $8)",
            # Mutating every row is stated, not inferred.
            "UPDATE t SET a = 1",
            "DELETE FROM t",
            # SQL/JSON forms keep their clauses through the round trip.
            "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a int EXISTS PATH '$.a'))",
            "SELECT JSON_QUERY('{}', '$' RETURNING bytea FORMAT JSON ENCODING UTF8)",
            "SELECT JSON_OBJECT('a': b FORMAT JSON ABSENT ON NULL WITH UNIQUE KEYS RETURNING jsonb) FROM t",
            "SELECT JSON_ARRAYAGG(a ORDER BY b NULL ON NULL) FILTER (WHERE a > 0) FROM t",
            "SELECT JSON_OBJECTAGG(k: v ABSENT ON NULL) OVER (PARTITION BY c) FROM t",
            "SELECT JSON_SERIALIZE(doc RETURNING bytea FORMAT JSON ENCODING UTF8) FROM t",
            "SELECT JSON_VALUE(doc, '$.a ? (@ > $x)' PASSING $1 AS x RETURNING int DEFAULT 0 ON EMPTY ERROR ON ERROR) FROM t",
            "SELECT a FROM t WHERE NOT doc IS JSON OBJECT WITH UNIQUE KEYS",
            # Carriers are verified on the query they carry.
            "EXPLAIN (ANALYZE, COSTS OFF) SELECT a FROM t WHERE b = $1",
            "CREATE TABLE x AS SELECT a FROM t",
            "DECLARE c CURSOR FOR SELECT a FROM t ORDER BY a",
            "SELECT a INTO x FROM t",
        )
        other = (
            ("SELECT * FROM t WHERE (a,b) IN (SELECT x FROM u)", "rejected", "qs: invalid query"),
            ("SELECT * FROM t WHERE (a,b) NOT IN (SELECT x,y,z FROM u)", "rejected", "qs: invalid query"),
            ("SELECT EXTRACT(FORTNIGHT FROM $1)", "rejected", "qs: invalid query"),
            ("SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a int, a text))", "rejected", "qs: invalid query"),
            # PostgreSQL rejects these after parsing; the typed API cannot spell them.
            ("SELECT JSON_VALUE(jsonb '1', '$' EMPTY ARRAY ON ERROR)", "gap", "JSON_VALUE ON ERROR"),
            ("SELECT JSON_QUERY(jsonb '[1]', '$' WITH WRAPPER OMIT QUOTES)", "rejected", "qs: invalid query"),
            ("SELECT JSON_OBJECT(RETURNING text FORMAT JSON ENCODING UTF8)", "gap", "not spelled bytea"),
            ("SELECT JSON_ARRAYAGG(a ORDER BY b) OVER () FROM t", "rejected", "qs: invalid query"),
            ("DELETE FROM t WHERE true", "gap", "WHERE TRUE"),
            ("SELECT xmlelement(name a, xmlforest(b))", "out_of_scope", "XMLELEMENT, XMLFOREST"),
            ("EXPLAIN EXECUTE p(1)", "out_of_scope", "EXECUTE"),
            ("INSERT INTO t SELECT 1 INTO f", "out_of_scope", "SELECT INTO"),
            ("SELECT $2, $1", "gap", "parameter numbers"),
            ("SELECT $1, $1", "gap", "parameter numbers"),
            ("SELECT ROW()", "gap", "ROW()"),
        )
        occurrences = [
            self._occurrence(identifier, sql)
            for identifier, sql in enumerate((*verified, *(sql for sql, _, _ in other)))
        ]
        renderings = postgres_corpus._render(Path(__file__).resolve().parents[1], occurrences)
        for occurrence in occurrences:
            postgres_corpus._classify(occurrence, renderings[occurrence.id])

        for occurrence in occurrences[: len(verified)]:
            with self.subTest(sql=occurrence.source_sql):
                self.assertEqual(occurrence.status, "verified", occurrence.reason)
                self.assertEqual(
                    renderings[occurrence.id]["args"],
                    list(postgres_corpus._expected_arguments(occurrence.target_ast, occurrence.id)),
                )
        for occurrence, (_, status, reason) in zip(occurrences[len(verified) :], other):
            with self.subTest(sql=occurrence.source_sql):
                self.assertEqual(occurrence.status, status, occurrence.reason)
                self.assertIn(reason, occurrence.reason)
                self.assertEqual(occurrence.generated_sql, "")

        assignment = next(item for item in occurrences if item.source_sql == assignment_sql)
        self.assertEqual(
            renderings[assignment.id]["args"],
            [
                f"qs-postgres-corpus-{assignment.id}-param-1",
                f"qs-postgres-corpus-{assignment.id}-param-2",
            ],
        )
        carried = next(item for item in occurrences if item.source_sql.startswith("EXPLAIN (ANALYZE"))
        self.assertEqual(carried.generated_sql, 'SELECT "a" FROM "t" WHERE ("b" = $1)')

        for left, right in (
            ("SELECT * FROM t1 CROSS JOIN t2", "SELECT * FROM t1 INNER JOIN t2 ON TRUE"),
            ("SELECT 1::varchar", "SELECT 1::custom.varchar"),
            ("SELECT * FROM t WHERE (a,b) IN (SELECT x,y FROM u)", "SELECT * FROM t WHERE (b,a) IN (SELECT x,y FROM u)"),
            ("SELECT * FROM t WHERE (a,b) IN (SELECT x,y FROM u)", "SELECT * FROM t WHERE a IN (SELECT x FROM u)"),
            ("DELETE FROM t", "DELETE FROM t WHERE true"),
        ):
            with self.subTest(left=left, right=right):
                self.assertFalse(self._same(left, right))

    def test_source_parameter_order_is_derived_from_the_source(self) -> None:
        for sql, numbers in (
            ('UPDATE t SET ("a.b","q""r")=ROW($1,$2)', (1, 2)),
            ('UPDATE t SET ("a.b","q""r")=($1,$2)', (1, 2)),
            ('UPDATE t SET ("a.b","q""r")=(SELECT $1,$2) WHERE c=$3', (1, 2, 3)),
            ('UPDATE t SET (a,b)=ROW($1,$2), (c,d)=ROW($3,$4) WHERE e=$5', (1, 2, 3, 4, 5)),
            ('SELECT $1,$1', (1, 1)),
        ):
            with self.subTest(sql=sql):
                self.assertEqual(
                    postgres_corpus._source_param_numbers(postgres_corpus.parse_sql(sql)[0].stmt),
                    numbers,
                )
        self.assertEqual(
            list(postgres_corpus._expected_arguments(postgres_corpus.parse_sql("SELECT $1, $2")[0].stmt, 37)),
            ["qs-postgres-corpus-37-param-1", "qs-postgres-corpus-37-param-2"],
        )

    def test_only_a_reparsed_match_is_verified(self) -> None:
        sql = "SELECT a FROM t WHERE b = $1 ORDER BY a"
        rendered = 'SELECT "a" FROM "t" WHERE ("b" = $1) ORDER BY "a" ASC'
        sentinel = "qs-postgres-corpus-5-param-1"

        def classify(rendering: dict) -> postgres_corpus.QueryOccurrence:
            occurrence = self._occurrence(5, sql)
            postgres_corpus._classify(occurrence, rendering)
            return occurrence

        good = classify({"id": 5, "outcome": "built", "sql": rendered, "args": [sentinel]})
        self.assertEqual(good.status, "verified")
        self.assertEqual(good.generated_sql, rendered)
        self.assertEqual(good.normalization, ("default_ascending",))

        for name, rendering, status in (
            ("other column", {"sql": rendered.replace('"a" FROM', '"c" FROM'), "args": [sentinel]}, "ast_mismatch"),
            ("dropped clause", {"sql": 'SELECT "a" FROM "t" WHERE ("b" = $1)', "args": [sentinel]}, "ast_mismatch"),
            ("descending", {"sql": rendered.replace("ASC", "DESC"), "args": [sentinel]}, "ast_mismatch"),
            ("source text echoed with a wrapper", {"sql": "EXPLAIN " + sql, "args": [sentinel]}, "ast_mismatch"),
            ("not SQL", {"sql": "SELECT FROM WHERE", "args": [sentinel]}, "reparse_error"),
            ("two statements", {"sql": rendered + "; SELECT 1", "args": [sentinel]}, "reparse_error"),
            ("empty", {"sql": "", "args": [sentinel]}, "reparse_error"),
            ("missing argument", {"sql": rendered, "args": []}, "arg_mismatch"),
            ("other occurrence's argument", {"sql": rendered, "args": ["qs-postgres-corpus-6-param-1"]}, "arg_mismatch"),
            ("extra argument", {"sql": rendered, "args": [sentinel, sentinel]}, "arg_mismatch"),
        ):
            with self.subTest(name=name):
                occurrence = classify({"id": 5, "outcome": "built", **rendering})
                self.assertEqual(occurrence.status, status)
                self.assertNotIn(occurrence.status, postgres_corpus._STATUSES)
                self.assertTrue(occurrence.reason)

        for outcome in ("out_of_scope", "gap", "rejected"):
            with self.subTest(outcome=outcome):
                occurrence = classify({"id": 5, "outcome": outcome, "detail": "why", "sql": "", "args": []})
                self.assertEqual((occurrence.status, occurrence.reason, occurrence.generated_sql), (outcome, "why", ""))
                with self.assertRaisesRegex(RuntimeError, "no detail"):
                    classify({"id": 5, "outcome": outcome, "detail": "", "sql": "", "args": []})

    def test_render_rejects_inconsistent_adapter_answers(self) -> None:
        occurrence = self._occurrence(11, "SELECT 1")
        repo = Path(__file__).resolve().parents[1]

        def answer(*values: dict, returncode: int = 0, stderr: bytes = b"") -> mock.Mock:
            stdout = "".join(json.dumps(value) + "\n" for value in values).encode()
            return mock.Mock(returncode=returncode, stdout=stdout, stderr=stderr)

        built = {"id": 11, "outcome": "built", "detail": "", "sql": "SELECT 1", "args": []}
        for name, completed, message in (
            ("duplicate", answer(built, built), "duplicate occurrence id"),
            ("extra", answer(built, {**built, "id": 12}), "id set mismatch"),
            ("missing", answer(), "id set mismatch"),
            ("unknown outcome", answer({**built, "outcome": "verified"}), "unknown outcome"),
            ("no outcome", answer({"id": 11}), "invalid line"),
            ("failure", answer(returncode=1, stderr=b"corpusrender: request 1: id=11: boom"), "id=11: boom"),
        ):
            with self.subTest(name=name):
                with mock.patch.object(postgres_corpus.subprocess, "run", return_value=completed) as run:
                    with self.assertRaisesRegex(RuntimeError, message):
                        postgres_corpus._render(repo, [occurrence])
                request = json.loads(run.call_args.kwargs["input"].decode())
                self.assertEqual(request, {"id": 11, "tree": occurrence.target_tree})

        with mock.patch.object(postgres_corpus.subprocess, "run") as run:
            with self.assertRaisesRegex(RuntimeError, "duplicate occurrence ids"):
                postgres_corpus._render(repo, [occurrence, occurrence])
        run.assert_not_called()

    def test_export_rejects_partial_unverified_and_mismatched_renderings(self) -> None:
        sql = "SELECT 1"

        def occurrence(status: str = "verified") -> postgres_corpus.QueryOccurrence:
            value = self._occurrence(0, sql)
            value.status = status
            value.generated_sql = sql if status == "verified" else ""
            value.reason = "" if status == "verified" else "why"
            return value

        built = {"outcome": "built", "sql": sql, "args": []}
        kwargs = {
            "expected_sql_sha256": _MINI_SQL_SHA256,
            "commit": "a" * 40,
        }
        with mock.patch.object(postgres_corpus, "_fixed_coverage_failures", return_value=[]):
            postgres_corpus._validate_export_state([occurrence()], 1, {0: built}, **kwargs)
            postgres_corpus._validate_export_state(
                [occurrence("gap")], 1, {0: {"outcome": "gap", "detail": "why"}}, **kwargs
            )
            for name, arguments, message in (
                ("partial corpus", ([occurrence()], 2, {0: built}), "complete unbounded corpus"),
                ("no rendering", ([occurrence()], 1, {}), "render id set mismatch"),
                ("transient status", ([occurrence("ast_mismatch")], 1, {0: built}), "final status"),
                ("unrendered status", ([occurrence("parsed")], 1, {0: built}), "final status"),
                # Frozen SQL must be the SQL that was reparsed.
                ("other SQL", ([occurrence()], 1, {0: {**built, "sql": "SELECT 2"}}), "does not match its rendering"),
                ("verified without a build", ([occurrence()], 1, {0: {"outcome": "gap", "detail": "x"}}), "does not match its rendering"),
                ("gap that was built", ([occurrence("gap")], 1, {0: built}), "does not match its rendering"),
            ):
                with self.subTest(name=name):
                    with self.assertRaisesRegex(SystemExit, message):
                        postgres_corpus._validate_export_state(*arguments, **kwargs)
            with self.assertRaisesRegex(SystemExit, "--commit"):
                postgres_corpus._validate_export_state(
                    [occurrence()], 1, {0: built}, expected_sql_sha256=_MINI_SQL_SHA256, commit="main"
                )

    def test_export_preserves_frozen_provenance_tree_and_arguments(self) -> None:
        sql = "EXPLAIN (COSTS OFF) SELECT $1"
        occurrence = self._occurrence(0, sql, file="src/test/regress/sql/arguments.sql", line=9)
        occurrence.status = "verified"
        occurrence.generated_sql = "SELECT $1"
        with tempfile.TemporaryDirectory(prefix="qs-postgres-export-") as directory:
            output = Path(directory) / "fixtures"
            self._export(output, [occurrence], Path(directory))
            data = (output / "testdata" / "corpus-00.jsonl").read_bytes()
            record = json.loads(data)
            manifest = json.loads((output / "manifest.json").read_text())
            self.assertEqual(sorted(path.name for path in output.iterdir()), ["manifest.json", "testdata"])
        self.assertEqual(record["id"], 0)
        self.assertEqual(record["source"], "src/test/regress/sql/arguments.sql")
        self.assertEqual(record["line"], 9)
        self.assertEqual(record["statement"], "ExplainStmt")
        self.assertEqual(record["original_sql"], sql)
        self.assertEqual(record["want_sql"], "SELECT $1")
        self.assertEqual(record["want_args"], ["qs-postgres-corpus-0-param-1"])
        # The tree is the carried query, exactly as the adapter received it.
        self.assertEqual(record["tree"], occurrence.target_tree)
        self.assertEqual(list(record["tree"]), ["SelectStmt"])
        self.assertEqual(manifest["schema"], "qs-postgres-corpus-v3")
        self.assertEqual(manifest["counts"]["total"], 1)
        self.assertEqual(
            manifest["files"],
            [
                {
                    "path": "testdata/corpus-00.jsonl",
                    "sha256": hashlib.sha256(data).hexdigest(),
                    "first_id": 0,
                    "count": 1,
                    "verified": 1,
                    "out_of_scope": 0,
                    "gap": 0,
                    "rejected": 0,
                }
            ],
        )

    def test_export_rejects_argument_mismatch_from_rendered_run(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-export-args-") as directory:
            root = Path(directory)
            sql_path = root / "src" / "test" / "regress" / "sql" / "params.sql"
            sql_path.parent.mkdir(parents=True)
            sql_path.write_text("SELECT $1;\n")
            digest = hashlib.sha256()
            digest.update(b"src/test/regress/sql/params.sql\0")
            digest.update(sql_path.read_bytes())
            digest.update(b"\0")
            args = self._args(root, root / "report.json", minimum_support=None)
            args.expected_sql_sha256 = digest.hexdigest()
            args.export_fixtures = root / "fixtures"

            with mock.patch.object(
                postgres_corpus, "_render", side_effect=self._render_all("SELECT $1", args=["wrong"])
            ):
                with self.assertRaisesRegex(SystemExit, "final status.*arg_mismatch"):
                    self._run_quiet(args)
            self.assertFalse(args.export_fixtures.exists())

    def test_fixture_baseline_rejects_verified_downgrade_with_equal_aggregate_totals(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-baseline-ids-") as directory:
            root = Path(directory)
            baseline = root / "baseline"
            self._export(baseline, self._baseline_occurrences(), root)

            after = self._baseline_occurrences()
            # Keep the aggregate verified count unchanged while moving support
            # from an old gap onto a previously verified ID.
            after[0].status = "rejected"
            after[0].reason = "synthetic"
            after[0].generated_sql = ""
            after[2].status = "verified"
            after[2].reason = ""
            after[2].generated_sql = "SELECT 1"
            with self.assertRaisesRegex(SystemExit, "lost verified coverage"):
                self._compare(after, baseline, root)

            # A gap may become verified; it may not become another failure.
            improved = self._baseline_occurrences()
            improved[2].status = "verified"
            improved[2].reason = ""
            improved[2].generated_sql = "SELECT 1"
            self._compare(improved, baseline, root)
            moved = self._baseline_occurrences()
            moved[2].status = "out_of_scope"
            with self.assertRaisesRegex(SystemExit, "status regressed"):
                self._compare(moved, baseline, root)

            # A query that enters scope may take any in-scope status.
            for status in ("gap", "rejected"):
                entered = self._baseline_occurrences()
                entered[3].status = status
                self._compare(entered, baseline, root)

    def test_fixture_baseline_rejects_oracle_provenance_pins_and_inventory_drift(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-baseline-drift-") as directory:
            root = Path(directory)
            baseline = root / "baseline"
            before = self._baseline_occurrences()
            self._export(baseline, before, root)
            self._compare(before, baseline, root)

            changed = self._baseline_occurrences()
            changed[0].generated_sql = "SELECT 2"
            with self.assertRaisesRegex(SystemExit, "want_sql changed"):
                self._compare(changed, baseline, root)

            changed = self._baseline_occurrences()
            changed[1].target_tree = postgres_corpus.build_target(
                *postgres_corpus._parse_statements("SELECT 2")[0]
            )[1]
            with self.assertRaisesRegex(SystemExit, "id 1 tree changed"):
                self._compare(changed, baseline, root)

            with self.assertRaisesRegex(SystemExit, "pinned source"):
                self._compare(before, baseline, root, commit="c" * 40)
            with self.assertRaisesRegex(SystemExit, "occurrence count changed"):
                self._compare(before[:-1], baseline, root)
            with self.assertRaisesRegex(SystemExit, "occurrence order changed"):
                self._compare([before[1], before[0], *before[2:]], baseline, root)

            # An edited fixture no longer authenticates as the baseline.
            fixture = baseline / "testdata" / "corpus-00.jsonl"
            fixture.write_bytes(fixture.read_bytes().replace(b"SELECT 1", b"SELECT 3", 1))
            with self.assertRaisesRegex(SystemExit, "hash mismatch"):
                self._compare(before, baseline, root)

    def test_checked_in_baseline_keeps_defaults_carriers_and_refusals(self) -> None:
        manifest, records = postgres_corpus._read_baseline_fixtures(
            Path(__file__).resolve().parents[1] / "internal" / "postgrescorpus"
        )
        floors = postgres_corpus._PINNED_CORPUS_FLOORS
        for field, value in floors.items():
            self.assertEqual(manifest["counts"][field], value, field)
        defaults = [
            item
            for item in records
            if item["status"] == "verified"
            and item["statement"] in {"InsertStmt", "UpdateStmt", "MergeStmt"}
            and "DEFAULT" in item["original_sql"].upper()
        ]
        self.assertGreaterEqual(len(defaults), 95)
        self.assertEqual(records[26577]["status"], "verified")
        for identifier in (8209, 8212):
            self.assertEqual(records[identifier]["status"], "rejected")
            self.assertIn("values", records[identifier]["reason"])
        # No fixture asks the adapter to build a carrier statement.
        carriers = set(postgres_corpus._CARRIER_FIELDS)
        self.assertFalse([item["id"] for item in records if set(item["tree"]) & carriers])
        explained = [item for item in records if item["origin"] == "explain" and item["status"] == "verified"]
        self.assertGreater(len(explained), 2000)
        self.assertFalse([item["id"] for item in explained if item["want_sql"].upper().startswith("EXPLAIN")])
        # A mutation of every row is verified on SQL that still has no WHERE.
        unconditional = [
            item
            for item in records
            if item["status"] == "verified"
            and set(item["tree"]) & {"UpdateStmt", "DeleteStmt"}
            and "whereClause" not in next(iter(item["tree"].values()))
        ]
        self.assertGreater(len(unconditional), 500)
        self.assertFalse(
            [
                item["id"]
                for item in unconditional
                if postgres_corpus.parse_sql(item["want_sql"])[0].stmt.whereClause is not None
            ]
        )

    def test_run_checks_baseline_before_touching_existing_export_directory(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-baseline-export-") as directory:
            root = self._mini_root(Path(directory))
            _, occurrences, _ = postgres_corpus.census(root)
            self.assertEqual(len(occurrences), 10)
            for occurrence in occurrences:
                occurrence.status = "verified"
                occurrence.generated_sql = "SELECT 1"
            baseline = Path(directory) / "baseline"
            self._export(baseline, occurrences, root, sql_sha256=_MINI_SQL_SHA256)
            output = Path(directory) / "fixtures"
            output.mkdir()
            sentinel = output / "sentinel"
            sentinel.write_text("preserve")
            args = self._args(root, Path(directory) / "report.json", minimum_support=None)
            args.export_fixtures = output
            args.baseline_fixtures = baseline
            args.commit = "c" * 40
            with mock.patch.object(postgres_corpus, "_render", side_effect=self._render_all("SELECT 1")):
                with self.assertRaisesRegex(SystemExit, "source commit"):
                    self._run_quiet(args)
            self.assertEqual(sentinel.read_text(), "preserve")
            self.assertEqual(sorted(path.name for path in output.iterdir()), ["sentinel"])

    def _baseline_occurrences(self) -> list[postgres_corpus.QueryOccurrence]:
        result: list[postgres_corpus.QueryOccurrence] = []
        statuses = ["verified", "verified", "gap", "out_of_scope", "verified", "verified", "rejected", "verified"]
        for identifier, status in enumerate(statuses):
            occurrence = self._occurrence(
                identifier,
                "SELECT 1",
                file=f"src/test/regress/sql/baseline-{identifier}.sql",
                line=identifier + 1,
                planner=identifier % 2 == 0,
            )
            occurrence.status = status
            occurrence.reason = "synthetic baseline" if status != "verified" else ""
            occurrence.generated_sql = "SELECT 1" if status == "verified" else ""
            result.append(occurrence)
        return result

    def test_export_writes_contiguous_files_and_removes_stale_ones(self) -> None:
        cases = (
            ("SELECT $1", "verified"),
            ("SELECT 1", "gap"),
            ("SELECT $1", "rejected"),
            ("SELECT 1", "verified"),
            ("SELECT 2", "out_of_scope"),
            ("SELECT 3", "verified"),
            ("SELECT 4", "verified"),
            ("SELECT 5", "verified"),
            ("SELECT $1", "verified"),
        )
        occurrences = []
        for identifier, (sql, status) in enumerate(cases):
            occurrence = self._occurrence(identifier, sql, planner=identifier % 2 == 0)
            occurrence.status = status
            occurrence.reason = "" if status == "verified" else "not in the mini fixture"
            occurrence.generated_sql = sql if status == "verified" else ""
            occurrences.append(occurrence)
        with tempfile.TemporaryDirectory(prefix="qs-postgres-export-files-") as directory, mock.patch.object(
            postgres_corpus, "_FIXTURE_RECORDS", 4
        ):
            output = Path(directory) / "fixtures"
            (output / "testdata").mkdir(parents=True)
            stale = output / "testdata" / "corpus-07.jsonl"
            stale.write_text("{}\n")
            self._export(output, occurrences, Path(directory))
            manifest = json.loads((output / "manifest.json").read_text())
            names = sorted(path.name for path in (output / "testdata").iterdir())
            files = [
                [json.loads(line) for line in (output / "testdata" / name).read_text().splitlines()]
                for name in names
            ]
            # The export is what the next run authenticates as its baseline.
            reread, records = postgres_corpus._read_baseline_fixtures(output)

        self.assertEqual(names, ["corpus-00.jsonl", "corpus-01.jsonl", "corpus-02.jsonl"])
        self.assertEqual([[item["id"] for item in file] for file in files], [[0, 1, 2, 3], [4, 5, 6, 7], [8]])
        self.assertEqual(
            [(entry["first_id"], entry["count"]) for entry in manifest["files"]], [(0, 4), (4, 4), (8, 1)]
        )
        self.assertEqual(
            [[entry[status] for status in postgres_corpus._STATUSES] for entry in manifest["files"]],
            [[2, 0, 1, 1], [3, 1, 0, 0], [1, 0, 0, 0]],
        )
        self.assertEqual(files[0][0]["want_args"], ["qs-postgres-corpus-0-param-1"])
        self.assertEqual(files[2][0]["want_args"], ["qs-postgres-corpus-8-param-1"])
        # Only a verified record carries an expectation.
        self.assertEqual((files[0][2]["want_sql"], files[0][2]["want_args"]), ("", []))
        self.assertEqual(files[0][1]["reason"], "not in the mini fixture")
        counts = manifest["counts"]
        self.assertEqual(
            [counts[key] for key in ("total", "verified", "out_of_scope", "gap", "rejected")], [9, 6, 1, 1, 1]
        )
        # Literal payloads are removed from the shape: the five SELECT <n>
        # share one shape, and one unverified occurrence disqualifies it.
        self.assertEqual(files[0][0]["shape"], files[2][0]["shape"])
        self.assertEqual((counts["distinct_shapes"], counts["distinct_verified_shapes"]), (2, 0))
        self.assertEqual(reread, manifest)
        self.assertEqual(records, [item for file in files for item in file])

    def test_thresholds_use_real_census_and_reject_partial_or_unrendered_runs(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-corpus-test-") as directory:
            root = self._mini_root(Path(directory))

            def failing_after(count: int):
                def render(_, occurrences):
                    return {
                        item.id: (
                            {"id": item.id, "outcome": "built", "detail": "", "sql": "SELECT 1", "args": []}
                            if index < count
                            else {"id": item.id, "outcome": "rejected", "detail": "synthetic failure", "sql": "", "args": []}
                        )
                        for index, item in enumerate(occurrences)
                    }

                return render

            args = self._args(root, Path(directory) / "exact.json", minimum_support=90)
            with mock.patch.object(postgres_corpus, "_render", side_effect=failing_after(9)):
                self.assertEqual(self._run_quiet(args), 0)
            report = json.loads((Path(directory) / "exact.json").read_text())
            self.assertEqual(report["counts"]["query_occurrences"], 10)
            self.assertEqual(report["counts"]["verified"], 9)
            self.assertEqual(report["counts"]["statuses"], {"verified": 9, "rejected": 1})
            self.assertEqual(report["counts"]["census_query_occurrences"], 10)
            self.assertTrue(report["thresholds"]["passed"])

            # All ten queries share one normalized shape.  Nine verified
            # occurrences must not be mistaken for a verified shape group.
            args = self._args(root, Path(directory) / "shape.json", minimum_support=90)
            args.minimum_shape_support = 90
            with mock.patch.object(postgres_corpus, "_render", side_effect=failing_after(9)):
                self.assertEqual(self._run_quiet(args), 1)
            shape = json.loads((Path(directory) / "shape.json").read_text())
            self.assertEqual(shape["counts"]["distinct_query_shapes"], 1)
            self.assertEqual(shape["counts"]["distinct_verified_shapes"], 0)
            self.assertTrue(any("distinct-shape rate" in value for value in shape["thresholds"]["failures"]))

            args = self._args(root, Path(directory) / "shape-pass.json", minimum_support=90)
            args.minimum_shape_support = 90
            with mock.patch.object(postgres_corpus, "_render", side_effect=self._render_all("SELECT 1")):
                self.assertEqual(self._run_quiet(args), 0)
            shape_pass = json.loads((Path(directory) / "shape-pass.json").read_text())
            self.assertTrue(shape_pass["thresholds"]["passed"])

            # A rendering that does not reparse to the source earns nothing.
            args = self._args(root, Path(directory) / "wrong.json", minimum_support=1)
            with mock.patch.object(postgres_corpus, "_render", side_effect=self._render_all("SELECT 2")):
                self.assertEqual(self._run_quiet(args), 1)
            wrong = json.loads((Path(directory) / "wrong.json").read_text())
            self.assertEqual(wrong["counts"]["verified"], 0)

            # Planner shape coverage is a separate gate.  Nine successful
            # instances of one planner shape still leave that shape unverified.
            args = self._args(root, Path(directory) / "planner-shape.json", minimum_support=0)
            args.minimum_planner_shape_support = 90
            with mock.patch.object(postgres_corpus, "_render", side_effect=failing_after(9)):
                self.assertEqual(self._run_quiet(args), 1)
            planner_shape = json.loads((Path(directory) / "planner-shape.json").read_text())
            self.assertEqual(planner_shape["counts"]["planner_distinct_shapes"], 1)
            self.assertEqual(planner_shape["counts"]["planner_distinct_verified_shapes"], 0)
            self.assertTrue(
                any("planner distinct-shape rate" in value for value in planner_shape["thresholds"]["failures"])
            )

            args = self._args(root, Path(directory) / "planner-shape-pass.json", minimum_support=0)
            args.minimum_planner_shape_support = 90
            with mock.patch.object(postgres_corpus, "_render", side_effect=self._render_all("SELECT 1")):
                self.assertEqual(self._run_quiet(args), 0)
            planner_shape_pass = json.loads((Path(directory) / "planner-shape-pass.json").read_text())
            self.assertEqual(planner_shape_pass["counts"]["planner_distinct_rate"], 1.0)
            self.assertTrue(planner_shape_pass["thresholds"]["passed"])

            args = self._args(root, Path(directory) / "below.json", minimum_support=90)
            with mock.patch.object(postgres_corpus, "_render", side_effect=failing_after(8)):
                self.assertEqual(self._run_quiet(args), 1)
            below = json.loads((Path(directory) / "below.json").read_text())
            self.assertFalse(below["thresholds"]["passed"])
            self.assertTrue(any("below 0.9000" in value for value in below["thresholds"]["failures"]))

            args = self._args(root, Path(directory) / "partial.json", minimum_support=0, limit=1)
            with mock.patch.object(postgres_corpus, "_render", side_effect=self._render_all("SELECT 1")):
                self.assertEqual(self._run_quiet(args), 1)
            partial = json.loads((Path(directory) / "partial.json").read_text())
            self.assertTrue(partial["scope"]["partial"])
            self.assertEqual(partial["counts"]["census_query_occurrences"], 10)
            self.assertEqual(partial["counts"]["query_occurrences"], 1)
            self.assertIn("--limit produces a partial corpus", partial["thresholds"]["failures"])

            args = self._args(root, Path(directory) / "unrendered.json", minimum_support=0)
            args.render = False
            args.export_fixtures = Path(directory) / "fixtures"
            with mock.patch.object(postgres_corpus, "_render", side_effect=AssertionError("adapter must not run")):
                with self.assertRaisesRegex(SystemExit, "requires the Go adapter"):
                    self._run_quiet(args)
            self.assertFalse((Path(directory) / "fixtures").exists())

    def test_source_hash_rejects_before_render(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-corpus-hash-") as directory:
            root = self._mini_root(Path(directory))
            args = self._args(root, Path(directory) / "mismatch.json", minimum_support=None)
            args.expected_sql_sha256 = "0" * 64
            args.export_fixtures = Path(directory) / "fixtures"
            with mock.patch.object(postgres_corpus, "_render", side_effect=AssertionError("adapter must not run")):
                with self.assertRaisesRegex(SystemExit, "hash mismatch"):
                    self._run_quiet(args)

    @staticmethod
    def _occurrence(
        identifier: int,
        sql: str,
        *,
        file: str | None = None,
        line: int = 1,
        planner: bool = False,
    ) -> postgres_corpus.QueryOccurrence:
        """Build an occurrence the way the census does, from source SQL."""

        statement, tree = postgres_corpus._parse_statements(sql)[0]
        target_ast, target_tree = postgres_corpus.build_target(statement, tree)
        unit = postgres_corpus.SQLUnit(
            file=file or f"case-{identifier}.sql",
            start=0,
            end=len(sql),
            line=line,
            sql=sql,
        )
        return postgres_corpus.QueryOccurrence(
            id=identifier,
            unit=unit,
            node=statement,
            statement_type=type(statement).__name__,
            origin="direct",
            wrapper_type=None,
            source_sql=sql,
            target_ast=target_ast,
            target_tree=target_tree,
            expected_error=False,
            expected_hint="",
            planner=planner,
            families=("select",),
        )

    @staticmethod
    def _render_all(sql: str, *, args: tuple | list = ()):
        """Return a stand-in adapter that answers every tree with one SQL."""

        def render(_, occurrences):
            return {
                item.id: {"id": item.id, "outcome": "built", "detail": "", "sql": sql, "args": list(args)}
                for item in occurrences
            }

        return render

    @staticmethod
    def _export(output: Path, occurrences, root: Path, *, sql_sha256: str = "b" * 64) -> None:
        postgres_corpus._export_fixtures(
            output,
            occurrences,
            source_root=root,
            commit="a" * 40,
            sql_sha256=sql_sha256,
            parser_version=postgres_corpus.pglast.__version__,
            parser_postgres_version=tuple(postgres_corpus.pglast.get_postgresql_version()),
        )

    @staticmethod
    def _compare(occurrences, baseline: Path, root: Path, *, commit: str = "a" * 40) -> None:
        postgres_corpus._compare_fixture_baseline(
            occurrences,
            baseline,
            source_root=root,
            commit=commit,
            sql_sha256="b" * 64,
            parser_version=postgres_corpus.pglast.__version__,
            parser_postgres_version=tuple(postgres_corpus.pglast.get_postgresql_version()),
        )

    @staticmethod
    def _mini_root(directory: Path) -> Path:
        path = directory / "src" / "test" / "regress" / "sql" / "select.sql"
        path.parent.mkdir(parents=True)
        path.write_text("SELECT 1;\n" * 10)
        return directory

    @staticmethod
    def _args(root: Path, report: Path, *, minimum_support: float | None, limit: int | None = None):
        return type(
            "Args",
            (),
            {
                "postgres_root": root,
                "repo": Path(__file__).resolve().parents[1],
                "report": report,
                "commit": "a" * 40,
                "expected_sql_sha256": _MINI_SQL_SHA256,
                "export_fixtures": None,
                "limit": limit,
                "render": True,
                "minimum_support": minimum_support,
                "minimum_planner_support": None,
                "minimum_shape_support": None,
                "minimum_planner_shape_support": None,
            },
        )()

    @staticmethod
    def _run_quiet(args) -> int:
        with contextlib.redirect_stdout(io.StringIO()):
            return postgres_corpus.run(args)

    @staticmethod
    def _same(left: str, right: str) -> bool:
        parse = postgres_corpus.parse_sql
        return postgres_corpus.compare_trees(parse(left)[0].stmt, parse(right)[0].stmt)


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
