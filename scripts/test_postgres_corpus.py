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

    def test_explain_boolean_spellings_are_semantic_only(self) -> None:
        positive = (
            ("COSTS OFF", "COSTS FALSE"),
            ("COSTS 0", "COSTS FALSE"),
            ("COSTS ON", "COSTS TRUE"),
            ("COSTS 1", "COSTS TRUE"),
            ("VERBOSE", "VERBOSE TRUE"),
        )
        for left, right in positive:
            with self.subTest(left=left, right=right):
                self.assertTrue(self._same(f"EXPLAIN ({left}) SELECT 1", f"EXPLAIN ({right}) SELECT 1"))

        negative = (
            ("COSTS OFF", "COSTS ON"),
            ("COSTS 2", "COSTS TRUE"),
            ("COSTS OFF", "VERBOSE FALSE"),
            ("COSTS OFF, VERBOSE", "VERBOSE TRUE, COSTS FALSE"),
            ("FORMAT TEXT", "FORMAT JSON"),
            ("SERIALIZE TEXT", "SERIALIZE BINARY"),
        )
        for left, right in negative:
            with self.subTest(left=left, right=right):
                sql_left = f"EXPLAIN ({left}) SELECT 1"
                sql_right = f"EXPLAIN ({right}) SELECT 1"
                self.assertFalse(self._same(sql_left, sql_right))

        self.assertFalse(
            self._same(
                "EXPLAIN (COSTS OFF) SELECT 1",
                "EXPLAIN (COSTS FALSE) SELECT 2",
            )
        )
        self.assertFalse(
            self._same(
                "EXPLAIN (COSTS OFF) SELECT a FROM t WHERE a = 1",
                "EXPLAIN (COSTS FALSE) SELECT a FROM t WHERE a = 2",
            )
        )

        # DefElem is also used outside EXPLAIN; its tokens must remain exact.
        self.assertFalse(
            self._same(
                "CREATE INDEX i ON t (x) WITH (costs=false)",
                "CREATE INDEX i ON t (x) WITH (costs=0)",
            )
        )

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
        ):
            with self.subTest(left=left, right=right):
                self.assertFalse(self._same(left, right))

    def test_adapter_families_roundtrip_through_compiled_probe(self) -> None:
        # These are handwritten boundary cases for adapters whose qs
        # constructors enforce structural invariants. The probe compiles and
        # renders every builder; assertions compare only the reparsed AST.
        sql_cases = (
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
            # JSON_TABLE EXISTS defaults and explicit error results.
            "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a int EXISTS PATH '$.a'))",
            "SELECT * FROM JSON_TABLE('[1,2]', '$[*]' COLUMNS (n FOR ORDINALITY, a int PATH '$'))",
            "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a int EXISTS PATH '$.a' ERROR ON ERROR))",
            "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a int EXISTS PATH '$.a' TRUE ON ERROR))",
            "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a int EXISTS PATH '$.a' FALSE ON ERROR))",
            "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a int EXISTS PATH '$.a' UNKNOWN ON ERROR))",
            "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a text PATH '$' WITH WRAPPER))",
            "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a text PATH '$' WITH CONDITIONAL WRAPPER))",
            "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a text PATH '$' WITHOUT WRAPPER OMIT QUOTES))",
            "SELECT * FROM JSON_TABLE('{}', '$' COLUMNS (a bytea FORMAT JSON ENCODING UTF8 PATH '$'))",
            "SELECT JSON_QUERY('{}', '$' RETURNING bytea FORMAT JSON ENCODING UTF8)",
            # Scalar and tuple IN/NOT IN retain the subquery width.
            "SELECT * FROM t WHERE (a,b) IN (SELECT x,y FROM u)",
            "SELECT * FROM t WHERE (a,b) NOT IN (SELECT x,y FROM u)",
            "SELECT * FROM t WHERE (a,b,c) IN (SELECT x,y,z FROM u)",
            "SELECT * FROM t WHERE (a,b,c) NOT IN (SELECT x,y,z FROM u)",
            "SELECT * FROM t WHERE ROW(a) IN (SELECT x FROM u)",
            "SELECT * FROM t WHERE ROW(a,b) IN (SELECT x,y FROM u)",
            "SELECT * FROM t WHERE a IN (SELECT x FROM u)",
        )
        error_cases = (
            "SELECT * FROM t WHERE (a,b) IN (SELECT x FROM u)",
            "SELECT * FROM t WHERE (a,b) NOT IN (SELECT x,y,z FROM u)",
        )
        occurrences = []
        for identifier, sql in enumerate((*sql_cases, *error_cases)):
            statement = postgres_corpus.parse_sql(sql)[0].stmt
            unit = postgres_corpus.SQLUnit(
                file=f"adapter-{identifier}.sql",
                start=0,
                end=len(sql),
                line=1,
                sql=sql,
            )
            occurrence = postgres_corpus.QueryOccurrence(
                id=identifier,
                unit=unit,
                node=statement,
                statement_type=type(statement).__name__,
                origin="direct",
                wrapper_type=None,
                source_sql=sql,
                source_ast=statement,
                expected_error=identifier >= len(sql_cases),
                expected_hint="",
                planner=False,
                families=(),
            )
            occurrence.builder = postgres_corpus.GoEmitter().statement(statement)
            occurrences.append(occurrence)

        with tempfile.TemporaryDirectory(prefix="qs-postgres-adapter-probe-") as directory:
            results = postgres_corpus._go_probe(
                Path(__file__).resolve().parents[1], occurrences, Path(directory)
            )

        for occurrence in occurrences:
            with self.subTest(sql=occurrence.source_sql):
                value = results.get(occurrence.id)
                self.assertIsNotNone(value)
                if occurrence.expected_error:
                    self.assertIn("subquery projection width differs from the left operand", (value or {}).get("error", ""))
                    continue
                self.assertNotIn("error", value or {})
                rendered = str((value or {}).get("sql", ""))
                equal, _ = postgres_corpus.compare_trees(
                    occurrence.source_ast, postgres_corpus._target_ast(rendered)
                )
                self.assertTrue(equal, rendered)

        for left, right in (
            ("SELECT * FROM t1 CROSS JOIN t2", "SELECT * FROM t1 INNER JOIN t2 ON TRUE"),
            ("SELECT 1::varchar", "SELECT 1::custom.varchar"),
            ("SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a text PATH '$' WITH WRAPPER))", "SELECT * FROM JSON_TABLE(jsonb '1', '$' COLUMNS (a text PATH '$' WITH CONDITIONAL WRAPPER))"),
            ("SELECT * FROM t WHERE (a,b) IN (SELECT x,y FROM u)", "SELECT * FROM t WHERE (b,a) IN (SELECT x,y FROM u)"),
            ("SELECT * FROM t WHERE (a,b) IN (SELECT x,y FROM u)", "SELECT * FROM t WHERE a IN (SELECT x FROM u)"),
        ):
            with self.subTest(left=left, right=right):
                self.assertFalse(self._same(left, right))

    def test_probe_arguments_follow_source_paramref_order(self) -> None:
        sql = "SELECT $1, $2"
        statement = postgres_corpus.parse_sql(sql)[0].stmt
        unit = postgres_corpus.SQLUnit(
            file="arguments.sql",
            start=0,
            end=len(sql),
            line=1,
            sql=sql,
        )
        occurrence = postgres_corpus.QueryOccurrence(
            id=37,
            unit=unit,
            node=statement,
            statement_type="SelectStmt",
            origin="direct",
            wrapper_type=None,
            source_sql=sql,
            source_ast=statement,
            expected_error=False,
            expected_hint="",
            planner=False,
            families=("select",),
        )
        occurrence.builder = postgres_corpus.GoEmitter(occurrence.id).statement(statement)
        expected = list(postgres_corpus._expected_arguments(statement, occurrence.id))
        self.assertEqual(
            expected,
            [
                "qs-postgres-corpus-37-param-1",
                "qs-postgres-corpus-37-param-2",
            ],
        )
        with tempfile.TemporaryDirectory(prefix="qs-postgres-arguments-") as directory:
            results = postgres_corpus._go_probe(
                Path(__file__).resolve().parents[1], [occurrence], Path(directory)
            )
        self.assertEqual(results[37]["args"], expected)

    def test_probe_id_integrity_rejects_duplicate_and_extra_results(self) -> None:
        sql = "SELECT 1"
        statement = postgres_corpus.parse_sql(sql)[0].stmt
        unit = postgres_corpus.SQLUnit(
            file="probe-ids.sql",
            start=0,
            end=len(sql),
            line=1,
            sql=sql,
        )
        occurrence = postgres_corpus.QueryOccurrence(
            id=11,
            unit=unit,
            node=statement,
            statement_type="SelectStmt",
            origin="direct",
            wrapper_type=None,
            source_sql=sql,
            source_ast=statement,
            expected_error=False,
            expected_hint="",
            planner=False,
            families=("select",),
            builder="qs.Select(qs.LiteralInt(1))",
        )
        duplicate = json.dumps(
            [
                {"id": 11, "sql": sql, "args": []},
                {"id": 11, "sql": sql, "args": []},
            ]
        )
        extra = json.dumps([{"id": 12, "sql": sql, "args": []}])
        for response, message in ((duplicate, "duplicate occurrence id"), (extra, "id set mismatch")):
            with self.subTest(message=message), tempfile.TemporaryDirectory(prefix="qs-postgres-probe-ids-") as directory:
                completed = mock.Mock(returncode=0, stdout=response, stderr="")
                with mock.patch.object(postgres_corpus.subprocess, "run", return_value=completed):
                    with self.assertRaisesRegex(RuntimeError, message):
                        postgres_corpus._go_probe(
                            Path(__file__).resolve().parents[1], [occurrence], Path(directory)
                        )

    def test_export_rejects_partial_unverified_and_probe_id_mutations(self) -> None:
        sql = "SELECT 1"
        statement = postgres_corpus.parse_sql(sql)[0].stmt
        unit = postgres_corpus.SQLUnit(
            file="export.sql",
            start=0,
            end=len(sql),
            line=7,
            sql=sql,
        )

        def occurrence(status: str = "verified") -> postgres_corpus.QueryOccurrence:
            value = postgres_corpus.QueryOccurrence(
                id=0,
                unit=unit,
                node=statement,
                statement_type="SelectStmt",
                origin="direct",
                wrapper_type=None,
                source_sql=sql,
                source_ast=statement,
                expected_error=False,
                expected_hint="",
                planner=False,
                families=("select",),
                status=status,
                generated_sql=sql if status == "verified" else "",
                builder="qs.Select(qs.LiteralInt(1))" if status == "verified" else "",
            )
            return value

        kwargs = {
            "expected_sql_sha256": _MINI_SQL_SHA256,
            "commit": "a" * 40,
        }
        with self.assertRaises(SystemExit):
            postgres_corpus._validate_export_state([occurrence()], 2, {0: {"sql": sql}}, **kwargs)
        with self.assertRaises(SystemExit):
            postgres_corpus._validate_export_state([occurrence()], 1, {}, **kwargs)
        with self.assertRaises(SystemExit):
            postgres_corpus._validate_export_state(
                [occurrence("ast_mismatch")], 1, {0: {"sql": sql}}, **kwargs
            )

        raw = occurrence()
        raw.builder = 'qs.UnsafeSQL("SELECT 1")'
        with self.assertRaisesRegex(SystemExit, "forbidden raw constructors"):
            postgres_corpus._validate_export_state([raw], 1, {0: {"sql": sql}}, **kwargs)

    def test_export_preserves_frozen_provenance_and_arguments(self) -> None:
        sql = "SELECT $1"
        statement = postgres_corpus.parse_sql(sql)[0].stmt
        unit = postgres_corpus.SQLUnit(
            file="src/test/regress/sql/arguments.sql",
            start=0,
            end=len(sql),
            line=9,
            sql=sql,
        )
        occurrence = postgres_corpus.QueryOccurrence(
            id=0,
            unit=unit,
            node=statement,
            statement_type="SelectStmt",
            origin="direct",
            wrapper_type=None,
            source_sql=sql,
            source_ast=statement,
            expected_error=False,
            expected_hint="",
            planner=False,
            families=("select",),
            status="verified",
            builder='qs.Select(qs.Param[any]("qs-postgres-corpus-0-param-1"))',
            generated_sql="SELECT $1",
        )
        with tempfile.TemporaryDirectory(prefix="qs-postgres-export-") as directory:
            output = Path(directory) / "generated"
            postgres_corpus._export_go(
                output,
                [occurrence],
                source_root=Path(directory),
                commit="a" * 40,
                sql_sha256="b" * 64,
                parser_version="v8.4",
                parser_postgres_version=(18, 4),
            )
            generated = (output / "shard00" / "corpus_generated_test.go").read_text()
            record = json.loads((output / "shard00" / "corpus.jsonl").read_text())
            manifest = json.loads((output / "manifest.json").read_text())
        self.assertIn("ID: 0", generated)
        self.assertIn('Status: "verified"', generated)
        self.assertIn("Build: build00000", generated)
        self.assertEqual(record["id"], 0)
        self.assertEqual(record["source"], "src/test/regress/sql/arguments.sql")
        self.assertEqual(record["line"], 9)
        self.assertEqual(record["original_sql"], "SELECT $1")
        self.assertEqual(record["want_sql"], "SELECT $1")
        self.assertEqual(record["want_args"], ["qs-postgres-corpus-0-param-1"])
        self.assertEqual(manifest["schema"], "qs-postgres-corpus-v2")
        self.assertEqual(manifest["counts"]["total"], 1)
        self.assertEqual(manifest["shards"][0]["first_id"], 0)
        self.assertEqual(manifest["shards"][0]["last_id"], 0)

    def test_export_rejects_argument_mismatch_from_compiled_run(self) -> None:
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
            args.export_go = root / "generated"

            def wrong_probe(_, occurrences, __):
                return {item.id: {"sql": "SELECT $1", "args": ["wrong"]} for item in occurrences}

            with mock.patch.object(postgres_corpus, "_go_probe", side_effect=wrong_probe):
                with self.assertRaisesRegex(SystemExit, "probe-classified"):
                    self._run_quiet(args)
            self.assertFalse(args.export_go.exists())

    def test_export_uses_stride_shards_and_explicit_status_associations(self) -> None:
        def make_case(identifier: int, sql: str, status: str) -> postgres_corpus.QueryOccurrence:
            statement = postgres_corpus.parse_sql(sql)[0].stmt
            unit = postgres_corpus.SQLUnit(
                file=f"src/test/regress/sql/shard-{identifier}.sql",
                start=0,
                end=len(sql),
                line=identifier + 1,
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
                source_ast=statement,
                expected_error=False,
                expected_hint="",
                planner=identifier % 2 == 0,
                families=("select",),
                status=status,
                reason="unsupported in the mini fixture" if status == "unsupported" else "",
                generated_sql=sql if status == "verified" else "",
                builder=(
                    "qs.Select(qs.Param[any](\"qs-postgres-corpus-%d-param-1\"))" % identifier
                    if status != "unsupported"
                    else ""
                ),
            )

        occurrences = [
            make_case(0, "SELECT $1", "verified"),
            make_case(1, "SELECT 1", "unsupported"),
            make_case(2, "SELECT $1", "construction_error"),
            make_case(3, "SELECT 1", "verified"),
            make_case(4, "SELECT 2", "verified"),
            make_case(5, "SELECT 3", "verified"),
            make_case(6, "SELECT 4", "verified"),
            make_case(7, "SELECT 5", "verified"),
            make_case(8, "SELECT $1", "verified"),
        ]
        with tempfile.TemporaryDirectory(prefix="qs-postgres-export-shards-") as directory:
            output = Path(directory) / "generated"
            postgres_corpus._export_go(
                output,
                occurrences,
                source_root=Path(directory),
                commit="a" * 40,
                sql_sha256="b" * 64,
                parser_version="v8.4",
                parser_postgres_version=(18, 4),
            )
            manifest = json.loads((output / "manifest.json").read_text())
            shard0 = [
                json.loads(line)
                for line in (output / "shard00" / "corpus.jsonl").read_text().splitlines()
            ]
            shard1 = [
                json.loads(line)
                for line in (output / "shard01" / "corpus.jsonl").read_text().splitlines()
            ]
            shard2_test = (output / "shard02" / "corpus_generated_test.go").read_text()

        self.assertEqual([item["id"] for item in shard0], [0, 8])
        self.assertEqual([item["id"] for item in shard1], [1])
        self.assertEqual(shard0[0]["want_args"], ["qs-postgres-corpus-0-param-1"])
        self.assertEqual(shard0[1]["want_args"], ["qs-postgres-corpus-8-param-1"])
        self.assertEqual(shard1[0]["status"], "unsupported")
        self.assertEqual(manifest["counts"]["total"], 9)
        self.assertEqual(manifest["counts"]["verified"], 7)
        self.assertEqual(manifest["counts"]["construction_error"], 1)
        self.assertEqual(manifest["counts"]["unsupported"], 1)
        self.assertIn('ID: 2, Status: "construction_error", Build: build00002', shard2_test)
        # Literal payloads are removed from the shape, so these repeated
        # SELECT $1 occurrences retain one shared identity in the fixture.
        self.assertEqual(shard0[0]["shape"], shard0[1]["shape"])

    def test_json_path_adapter_rejects_unsupported_policies(self) -> None:
        for encoding in ("UTF16", "UTF32"):
            for sql in (
                f"SELECT * FROM JSON_TABLE('{{}}', '$' COLUMNS (a bytea FORMAT JSON ENCODING {encoding} PATH '$'))",
                f"SELECT JSON_QUERY('{{}}', '$' RETURNING bytea FORMAT JSON ENCODING {encoding})",
            ):
                with self.subTest(sql=sql):
                    statement = postgres_corpus.parse_sql(sql)[0].stmt
                    with self.assertRaisesRegex(postgres_corpus.Unsupported, "encoding"):
                        postgres_corpus.GoEmitter().statement(statement)

        # These combinations cannot be spelled by the grammar. Reject them
        # at the adapter boundary rather than silently ignoring AST metadata.
        for field, policy in (
            ("wrapper", postgres_corpus.pgenums.JsonWrapper.JSW_UNCONDITIONAL),
            ("wrapper", postgres_corpus.pgenums.JsonWrapper.JSW_CONDITIONAL),
            ("quotes", postgres_corpus.pgenums.JsonQuotes.JS_QUOTES_KEEP),
            ("quotes", postgres_corpus.pgenums.JsonQuotes.JS_QUOTES_OMIT),
        ):
            with self.subTest(field=field, policy=policy):
                statement = postgres_corpus.parse_sql(
                    "SELECT * FROM JSON_TABLE('{}', '$' COLUMNS (a bool EXISTS PATH '$'))"
                )[0].stmt
                setattr(statement.fromClause[0].columns[0], field, policy)
                with self.assertRaisesRegex(postgres_corpus.Unsupported, f"EXISTS {field}"):
                    postgres_corpus.GoEmitter().statement(statement)

        statement = postgres_corpus.parse_sql(
            "SELECT * FROM JSON_TABLE('{}', '$' COLUMNS (a text PATH '$'))"
        )[0].stmt
        statement.fromClause[0].columns[0].format.encoding = postgres_corpus.pgenums.JsonEncoding.JS_ENC_UTF8
        with self.assertRaisesRegex(postgres_corpus.Unsupported, "encoding without FORMAT JSON"):
            postgres_corpus.GoEmitter().statement(statement)

    def test_thresholds_use_real_census_and_reject_partial_or_unprobed_runs(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-corpus-test-") as directory:
            root = self._mini_root(Path(directory))

            def probe(_, occurrences, __):
                return {
                    item.id: ({"sql": "SELECT 1"} if index < 9 else {"error": "synthetic failure"})
                    for index, item in enumerate(occurrences)
                }

            args = self._args(root, Path(directory) / "exact.json", minimum_support=90)
            with mock.patch.object(postgres_corpus, "_go_probe", side_effect=probe):
                self.assertEqual(self._run_quiet(args), 0)
            report = json.loads((Path(directory) / "exact.json").read_text())
            self.assertEqual(report["counts"]["query_occurrences"], 10)
            self.assertEqual(report["counts"]["supported"], 9)
            self.assertEqual(report["counts"]["census_query_occurrences"], 10)
            self.assertTrue(report["thresholds"]["passed"])

            # All ten queries share one normalized shape.  Nine verified
            # occurrences must not be mistaken for a verified shape group.
            args = self._args(root, Path(directory) / "shape.json", minimum_support=90)
            args.minimum_shape_support = 90
            with mock.patch.object(postgres_corpus, "_go_probe", side_effect=probe):
                self.assertEqual(self._run_quiet(args), 1)
            shape = json.loads((Path(directory) / "shape.json").read_text())
            self.assertEqual(shape["counts"]["distinct_query_shapes"], 1)
            self.assertEqual(shape["counts"]["distinct_supported_shapes"], 0)
            self.assertTrue(any("distinct-shape rate" in value for value in shape["thresholds"]["failures"]))

            args = self._args(root, Path(directory) / "shape-pass.json", minimum_support=90)
            args.minimum_shape_support = 90
            with mock.patch.object(
                postgres_corpus,
                "_go_probe",
                side_effect=lambda _, occurrences, __: {item.id: {"sql": "SELECT 1"} for item in occurrences},
            ):
                self.assertEqual(self._run_quiet(args), 0)
            shape_pass = json.loads((Path(directory) / "shape-pass.json").read_text())
            self.assertTrue(shape_pass["thresholds"]["passed"])

            # Planner shape coverage is a separate gate.  Nine successful
            # instances of one planner shape still leave that shape unverified.
            args = self._args(root, Path(directory) / "planner-shape.json", minimum_support=0)
            args.minimum_planner_shape_support = 90
            with mock.patch.object(postgres_corpus, "_go_probe", side_effect=probe):
                self.assertEqual(self._run_quiet(args), 1)
            planner_shape = json.loads((Path(directory) / "planner-shape.json").read_text())
            self.assertEqual(planner_shape["counts"]["planner_distinct_shapes"], 1)
            self.assertEqual(planner_shape["counts"]["planner_distinct_supported_shapes"], 0)
            self.assertTrue(
                any("planner distinct-shape rate" in value for value in planner_shape["thresholds"]["failures"])
            )

            args = self._args(root, Path(directory) / "planner-shape-pass.json", minimum_support=0)
            args.minimum_planner_shape_support = 90
            with mock.patch.object(
                postgres_corpus,
                "_go_probe",
                side_effect=lambda _, occurrences, __: {item.id: {"sql": "SELECT 1"} for item in occurrences},
            ):
                self.assertEqual(self._run_quiet(args), 0)
            planner_shape_pass = json.loads((Path(directory) / "planner-shape-pass.json").read_text())
            self.assertEqual(planner_shape_pass["counts"]["planner_distinct_rate"], 1.0)
            self.assertTrue(planner_shape_pass["thresholds"]["passed"])

            args = self._args(root, Path(directory) / "below.json", minimum_support=90)
            args.minimum_shape_support = None

            def below_probe(_, occurrences, __):
                return {
                    item.id: ({"sql": "SELECT 1"} if index < 8 else {"error": "synthetic failure"})
                    for index, item in enumerate(occurrences)
                }

            with mock.patch.object(postgres_corpus, "_go_probe", side_effect=below_probe):
                self.assertEqual(self._run_quiet(args), 1)
            below = json.loads((Path(directory) / "below.json").read_text())
            self.assertFalse(below["thresholds"]["passed"])
            self.assertTrue(any("below 0.9000" in value for value in below["thresholds"]["failures"]))

            args = self._args(root, Path(directory) / "partial.json", minimum_support=0, limit=1)
            with mock.patch.object(postgres_corpus, "_go_probe", side_effect=lambda *_: {0: {"sql": "SELECT 1"}}):
                self.assertEqual(self._run_quiet(args), 1)
            partial = json.loads((Path(directory) / "partial.json").read_text())
            self.assertTrue(partial["scope"]["partial"])
            self.assertEqual(partial["counts"]["census_query_occurrences"], 10)
            self.assertEqual(partial["counts"]["query_occurrences"], 1)
            self.assertIn("--limit produces a partial corpus", partial["thresholds"]["failures"])

            args = self._args(root, Path(directory) / "unprobed.json", minimum_support=0)
            args.probe = False
            args.export_go = Path(directory) / "generated"
            with mock.patch.object(postgres_corpus, "_go_probe", side_effect=AssertionError("probe must not run")):
                with self.assertRaisesRegex(SystemExit, "compiled Go probe"):
                    self._run_quiet(args)
            self.assertFalse((Path(directory) / "generated").exists())

    def test_source_hash_rejects_before_probe(self) -> None:
        with tempfile.TemporaryDirectory(prefix="qs-postgres-corpus-hash-") as directory:
            root = self._mini_root(Path(directory))
            args = self._args(root, Path(directory) / "mismatch.json", minimum_support=None)
            args.expected_sql_sha256 = "0" * 64
            args.export_go = Path(directory) / "generated"
            called = False

            def probe(*_):
                nonlocal called
                called = True
                return {}

            with mock.patch.object(postgres_corpus, "_go_probe", side_effect=probe):
                with self.assertRaises(SystemExit):
                    self._run_quiet(args)
            self.assertFalse(called)

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
                "probe_dir": report.parent / "probe",
                "export_go": None,
                "limit": limit,
                "probe": True,
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
        return postgres_corpus.compare_trees(parse(left)[0].stmt, parse(right)[0].stmt)[0]


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
