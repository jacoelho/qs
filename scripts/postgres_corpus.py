#!/usr/bin/env python3
"""Measure PostgreSQL regression query construction with qs.

This is a development-time tool.  It keeps PostgreSQL and ``pglast`` out of
the qs runtime.  Each query occurrence is parsed here, its parse tree is sent
to the Go adapter (``internal/postgrescorpus/cmd/corpusrender``), and the SQL
the adapter renders through public qs constructors is parsed again.  A query
counts as verified only when the reparsed tree matches the source tree after
the small normalisations documented in ``canonical_ast``.  The adapter never
grades its own output.

Statements that only carry another query (EXPLAIN, CREATE TABLE AS, DECLARE
CURSOR, SELECT INTO) are outside qs; their carried query is the one built
and compared.

The extractor is independent of pglast's statement splitter.  PostgreSQL's
regression files contain psql commands, COPY stdin data, nested comments,
dollar quoted bodies and intentionally malformed statements; those must be
reported rather than silently discarded.

Typical invocation (from the qs repository):

    python scripts/postgres_corpus.py \
      /path/to/postgres-<commit> \
      --repo . --report postgres-coverage.json

``--export-fixtures`` writes the JSONL fixtures and manifest after complete
verification and a comparison with the existing fixture baseline.
``--require-pinned-coverage`` applies the fixed full-corpus denominators and
verified floors without relying on percentage thresholds.  The command does
not contact the network.  Install the pinned parser from
``scripts/requirements-postgres.txt`` in a development virtualenv.
"""

from __future__ import annotations

import argparse
import copy
import dataclasses
import enum
import hashlib
import json
import re
import subprocess
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any, Iterator, Sequence

try:
    import pglast
    from pglast import ast as pgast
    from pglast import parse_sql
    from pglast.parser import parse_sql_json
except ImportError as exc:  # pragma: no cover - exercised by the CLI
    raise SystemExit(
        "pglast is required for PostgreSQL corpus measurement; "
        "install scripts/requirements-postgres.txt"
    ) from exc


REPOSITORY = "https://github.com/postgres/postgres"
RENDER_COMMAND = ("go", "run", "./internal/postgrescorpus/cmd/corpusrender")
RENDER_TIMEOUT_SECONDS = 600
SQL_ROOT = Path("src/test/regress/sql")
EXPECTED_ROOT = Path("src/test/regress/expected")
QUERY_TYPES = {
    "SelectStmt",
    "InsertStmt",
    "UpdateStmt",
    "DeleteStmt",
    "MergeStmt",
    "ExplainStmt",
}
# File-name selection is intentionally explicit in the report.  It identifies
# the planner-heavy slice without claiming that a regression file contains only
# planner behaviour.
PLANNER_FILE_TOKENS = (
    "aggregate",
    "analyze",
    "bitmap",
    "equivclass",
    "explain",
    "incremental_sort",
    "join",
    "limit",
    "memoize",
    "parallel",
    "partition",
    "predtest",
    "prepare",
    "select",
    "sort",
    "statistics",
    "subselect",
    "union",
    "window",
    "with",
)
LITERAL_QUERY_HARNESSES = {
    "explain_filter",
    "explain_filter_to_json",
}
WRAPPER_FIELDS = {
    "PrepareStmt": ("query", "prepare_body"),
    "ViewStmt": ("query", "view_body"),
    "CreateTableAsStmt": ("query", "ctas_body"),
    "DeclareCursorStmt": ("query", "cursor_body"),
}

_COMMIT_RE = re.compile(r"(?:postgres[-_])?([0-9a-f]{40})(?:$|[-_.])", re.I)
_EXPECTED_HINT_RE = re.compile(
    r"\b(?:should|must|will|would|expected?|expecting|intentionally)\s+"
    r"(?:fail|error|be invalid|raise)\b|"
    r"\b(?:fails?|failed|error|invalid|errors?)\b",
    re.I,
)
_VARIABLE_RE = re.compile(r"(?<!:):(?:[A-Za-z_][A-Za-z_0-9]*|['\"][^'\"]+['\"])")
_COPY_STDIN_RE = re.compile(r"\bCOPY\b[\s\S]*\bFROM\s+STDIN\s*$", re.I)
_PINNED_PGLAST_VERSION = "v8.4"
_PINNED_POSTGRES_VERSION = (18, 4)
_PINNED_POSTGRES_COMMIT = "630e607397424196a0a3ebb14a5658c2473ddadf"
_PINNED_SQL_SHA256 = "ddec651ca5f78d1ae9b721476efe586daf9220d80d5284f6fccd8329ed66218d"
_PINNED_CORPUS_FLOORS = {
    "total": 28197,
    "verified": 27887,
    "planner_total": 5150,
    "planner_verified": 5062,
    "distinct_shapes": 13722,
    "distinct_verified_shapes": 13517,
    "planner_distinct_shapes": 3893,
    "planner_distinct_verified_shapes": 3831,
}
_CORPUS_SCHEMA = "qs-postgres-corpus-v3"
_FIXTURE_DIRECTORY = "testdata"
# Contiguous IDs per fixture file; keeps each file to a few megabytes.
_FIXTURE_RECORDS = 2048
# verified: the rendering reparsed to the source tree.
# out_of_scope: the query uses syntax qs deliberately does not model.
# gap: the adapter has no public qs construction for an in-scope query.
# rejected: the adapter built the query and qs refused to render it.
_STATUSES = ("verified", "out_of_scope", "gap", "rejected")
_LOCATION_FIELDS = frozenset(
    {
        "location",
        "stmt_location",
        "stmt_len",
        "rexpr_list_start",
        "rexpr_list_end",
        "list_start",
        "list_end",
        "name_location",
    }
)
# Statements that only carry another query.  qs models the carried query.
_CARRIER_FIELDS = {
    "ExplainStmt": "query",
    "CreateTableAsStmt": "query",
    "DeclareCursorStmt": "query",
}
# "built" is a candidate rendering; the other outcomes are final statuses.
_RENDER_OUTCOMES = frozenset({"built", "out_of_scope", "gap", "rejected"})


@dataclasses.dataclass(frozen=True)
class SQLUnit:
    """One extracted SQL command or non-SQL psql/COPY item."""

    file: str
    start: int
    end: int
    line: int
    sql: str
    leading_comments: tuple[str, ...] = ()
    trailing_comments: tuple[str, ...] = ()
    kind: str = "sql"
    detail: str = ""

    @property
    def comments(self) -> str:
        return "\n".join((*self.leading_comments, *self.trailing_comments))


@dataclasses.dataclass
class QueryOccurrence:
    """A direct query or a query body extracted from a wrapper statement."""

    id: int
    unit: SQLUnit
    # Source occurrence; may be a carrier such as EXPLAIN.
    node: Any
    statement_type: str
    origin: str
    wrapper_type: str | None
    source_sql: str
    # Query qs must build: the comparison oracle and its location-free
    # libpg_query JSON form sent to the Go adapter.
    target_ast: Any
    target_tree: Any
    expected_error: bool
    expected_hint: str
    planner: bool
    families: tuple[str, ...]
    status: str = "unverified"
    reason: str = ""
    generated_sql: str = ""
    normalization: tuple[str, ...] = ()


def _line_number(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def _line_comments(text: str, begin: int, end: int) -> tuple[str, ...]:
    """Return SQL comments in a source range, preserving their wording."""

    value = text[begin:end]
    found: list[str] = []
    for match in re.finditer(r"--[^\n]*|/\*[\s\S]*?\*/", value):
        found.append(match.group(0))
    return tuple(found)


def _dollar_tag(text: str, index: int) -> str | None:
    if text[index] != "$":
        return None
    match = re.match(r"\$(?:[A-Za-z_][A-Za-z_0-9]*)?\$", text[index:])
    return match.group(0) if match else None


def _is_escape_string_prefix(text: str, index: int) -> bool:
    """Return whether the quote at *index* is preceded by a standalone E."""

    if index < 1 or text[index - 1] not in "eE":
        return False
    return index < 2 or not (text[index - 2].isalnum() or text[index - 2] == "_")


def extract_units(text: str, file: str) -> list[SQLUnit]:
    """Split a regression file without interpreting SQL or psql variables.

    The scanner tracks nested block comments, quote/dollar-quote delimiters
    and COPY FROM STDIN payloads.  It treats a backslash command at the start
    of a physical line as an excluded psql unit.  Every other semicolon at
    nesting depth zero terminates one SQL unit.
    """

    units: list[SQLUnit] = []
    n = len(text)
    i = 0
    sql_start: int | None = None
    pending_start = 0
    pending_comments: list[str] = []
    quote: str | None = None
    string_escape = False
    dollar: str | None = None
    block_depth = 0
    copy_payload = False
    copy_line_start = 0
    line_start = True

    def emit(end: int, *, include_semicolon: bool = False) -> None:
        nonlocal sql_start, pending_start, pending_comments
        if sql_start is None:
            return
        stop = end if include_semicolon else end
        raw = text[sql_start:stop].strip()
        if raw:
            leading = tuple(pending_comments)
            line_end = text.find("\n", stop)
            if line_end < 0:
                line_end = n
            trailing = _line_comments(text, stop, line_end)
            units.append(
                SQLUnit(
                    file=file,
                    start=sql_start,
                    end=stop,
                    line=_line_number(text, sql_start),
                    sql=raw,
                    leading_comments=leading,
                    trailing_comments=trailing,
                )
            )
        sql_start = None
        pending_comments = []
        pending_start = end

    while i < n:
        c = text[i]
        nxt = text[i + 1] if i + 1 < n else ""

        if copy_payload:
            if line_start and text.startswith("\\.", i):
                line_end = text.find("\n", i)
                if line_end < 0:
                    line_end = n
                copy_payload = False
                i = line_end
                line_start = True
                copy_line_start = i
                continue
            line_start = c == "\n"
            i += 1
            continue

        if sql_start is None:
            if c.isspace():
                line_start = c == "\n" or line_start
                i += 1
                continue
            if text.startswith("--", i):
                end = text.find("\n", i)
                if end < 0:
                    end = n
                pending_comments.append(text[i:end])
                i = end
                line_start = True
                continue
            if text.startswith("/*", i):
                end = i + 2
                depth = 1
                while end < n and depth:
                    if text.startswith("/*", end):
                        depth += 1
                        end += 2
                    elif text.startswith("*/", end):
                        depth -= 1
                        end += 2
                    else:
                        end += 1
                pending_comments.append(text[i:end])
                line_start = "\n" in text[i:end] and text[end - 1 : end] == "\n"
                i = end
                continue
            if c == "\\" and line_start:
                end = text.find("\n", i)
                if end < 0:
                    end = n
                units.append(
                    SQLUnit(
                        file=file,
                        start=i,
                        end=end,
                        line=_line_number(text, i),
                        sql=text[i:end],
                        leading_comments=tuple(pending_comments),
                        kind="psql",
                        detail="psql meta-command",
                    )
                )
                pending_comments = []
                i = end
                line_start = True
                continue
            sql_start = i
            quote = None
            dollar = None
            block_depth = 0

        if quote is not None:
            if c == quote:
                if nxt == quote:
                    i += 2
                    line_start = False
                    continue
                quote = None
            elif c == "\\" and quote == "'" and string_escape:
                i += 2
                line_start = False
                continue
            line_start = c == "\n"
            i += 1
            continue
        if dollar is not None:
            if text.startswith(dollar, i):
                i += len(dollar)
                dollar = None
            else:
                line_start = c == "\n"
                i += 1
            continue
        if block_depth:
            if text.startswith("/*", i):
                block_depth += 1
                i += 2
            elif text.startswith("*/", i):
                block_depth -= 1
                i += 2
            else:
                line_start = c == "\n"
                i += 1
            continue
        if text.startswith("--", i):
            end = text.find("\n", i)
            if end < 0:
                end = n
            i = end
            line_start = True
            continue
        if text.startswith("/*", i):
            block_depth = 1
            i += 2
            continue
        if c == "'" or c == '"':
            quote = c
            string_escape = c == "'" and _is_escape_string_prefix(text, i)
            i += 1
            line_start = False
            continue
        if c == "$":
            tag = _dollar_tag(text, i)
            if tag:
                dollar = tag
                i += len(tag)
                line_start = False
                continue
        if c == ";":
            end = i
            sql = text[sql_start:end].strip() if sql_start is not None else ""
            emit(end)
            if _COPY_STDIN_RE.search(sql):
                copy_payload = True
                copy_line_start = i + 1
            i += 1
            line_start = False
            continue
        if c == "\\" and line_start:
            # psql commands such as \g terminate the pending SQL command and
            # are executed by psql before the next physical line.  Keeping
            # them as separate excluded units avoids feeding the backslash to
            # PostgreSQL's parser.
            emit(i)
            end = text.find("\n", i)
            if end < 0:
                end = n
            units.append(
                SQLUnit(
                    file=file,
                    start=i,
                    end=end,
                    line=_line_number(text, i),
                    sql=text[i:end],
                    kind="psql",
                    detail="psql meta-command",
                )
            )
            i = end
            line_start = True
            continue
        line_start = c == "\n"
        i += 1

    if sql_start is not None:
        emit(n)
    return units


def _expected_hint(unit: SQLUnit) -> str:
    comments = unit.comments
    if not comments:
        return ""
    matches = [m.group(0).strip() for m in _EXPECTED_HINT_RE.finditer(comments)]
    return "; ".join(matches[:3])


def _has_psql_variable(sql: str) -> bool:
    """Detect psql ``:name`` substitution outside SQL literals/comments."""

    i = 0
    n = len(sql)
    quote: str | None = None
    string_escape = False
    dollar: str | None = None
    block_depth = 0
    while i < n:
        c = sql[i]
        nxt = sql[i + 1] if i + 1 < n else ""
        if quote is not None:
            if c == quote:
                if nxt == quote:
                    i += 2
                    continue
                quote = None
            elif c == "\\" and quote == "'" and string_escape:
                i += 2
                continue
            i += 1
            continue
        if dollar is not None:
            if sql.startswith(dollar, i):
                i += len(dollar)
                dollar = None
            else:
                i += 1
            continue
        if block_depth:
            if sql.startswith("/*", i):
                block_depth += 1
                i += 2
            elif sql.startswith("*/", i):
                block_depth -= 1
                i += 2
            else:
                i += 1
            continue
        if sql.startswith("--", i):
            end = sql.find("\n", i)
            i = n if end < 0 else end
            continue
        if sql.startswith("/*", i):
            block_depth = 1
            i += 2
            continue
        if c in "'\"":
            quote = c
            string_escape = c == "'" and _is_escape_string_prefix(sql, i)
            i += 1
            continue
        if c == "$":
            tag = _dollar_tag(sql, i)
            if tag:
                dollar = tag
                i += len(tag)
                continue
        if c == ":" and (i == 0 or sql[i - 1] != ":") and nxt != ":":
            if _VARIABLE_RE.match(sql, i):
                return True
        i += 1
    return False


def _node_name(node: Any) -> str:
    return type(node).__name__ if node is not None else ""


def _walk_nodes(node: Any) -> Iterator[Any]:
    if node is None:
        return
    if isinstance(node, (tuple, list)):
        for value in node:
            yield from _walk_nodes(value)
        return
    if isinstance(node, pgast.Node):
        yield node
        for slot in getattr(node, "__slots__", {}):
            yield from _walk_nodes(getattr(node, slot, None))


def _parse_statements(sql: str) -> list[tuple[Any, Any]]:
    """Parse SQL into (pglast AST, libpg_query JSON tree) statement pairs.

    The AST drives extraction and comparison; the JSON tree is what the Go
    adapter decodes.  Both come from the same pinned parser.
    """

    statements = parse_sql(sql)
    trees = json.loads(parse_sql_json(sql)).get("stmts", [])
    if len(statements) != len(trees):
        raise ValueError("parser representations disagree on statement count")
    return [(statement.stmt, tree["stmt"]) for statement, tree in zip(statements, trees)]


def _tree_tag(tree: Any) -> str:
    """Return the node tag of a libpg_query JSON node wrapper."""

    if isinstance(tree, dict) and len(tree) == 1:
        return next(iter(tree))
    return ""


def _strip_locations(tree: Any) -> Any:
    """Drop source positions, which no construction can reproduce."""

    if isinstance(tree, dict):
        return {key: _strip_locations(value) for key, value in tree.items() if key not in _LOCATION_FIELDS}
    if isinstance(tree, list):
        return [_strip_locations(value) for value in tree]
    return tree


def _query_node(stmt: Any, tree: Any) -> tuple[Any | None, Any | None, str, str | None]:
    name = _node_name(stmt)
    if name in QUERY_TYPES:
        return stmt, tree, "direct", None
    if name in WRAPPER_FIELDS:
        field, origin = WRAPPER_FIELDS[name]
        return getattr(stmt, field, None), tree[name].get(field), origin, name
    if name == "RuleStmt":
        return None, None, "rule", name
    return None, None, "", None


def _rule_actions(stmt: Any, tree: Any) -> Iterator[tuple[Any, Any, str]]:
    if _node_name(stmt) != "RuleStmt":
        return
    actions = list(getattr(stmt, "actions", ()) or ())
    trees = tree["RuleStmt"].get("actions", [])
    if len(actions) != len(trees):
        raise ValueError("parser representations disagree on rule actions")
    for action, action_tree in zip(actions, trees):
        if _node_name(action) in QUERY_TYPES:
            yield action, action_tree, "rule_action"


def _literal_harness_queries(stmt: Any, counts: Counter[str]) -> Iterator[tuple[Any, Any, str, str, str]]:
    """Extract fixed SQL passed to PostgreSQL's EXPLAIN test harnesses.

    These calls are ordinary SELECTs in the regression source, but the string
    argument is the SQL actually sent to the planner.  Counting only the
    outer function call would hide the tested query body.
    Dynamic ``format`` calls are deliberately excluded because their final SQL
    depends on runtime substitutions.
    """

    for node in _walk_nodes(stmt):
        if _node_name(node) != "FuncCall":
            continue
        names = [_str_value(value) for value in (node.funcname or ())]
        if not names or names[-1].lower() not in LITERAL_QUERY_HARNESSES:
            continue
        for arg in node.args or ():
            if _node_name(arg) != "A_Const" or _node_name(getattr(arg, "val", None)) != "String":
                continue
            source_sql = str(arg.val.sval)
            try:
                statements = _parse_statements(source_sql)
            except Exception as exc:
                counts["harness_parser_error"] += 1
                counts[f"harness_parser_error:{type(exc).__name__}"] += 1
                continue
            for parsed, parsed_tree in statements:
                body, body_tree, origin, wrapper = _query_node(parsed, parsed_tree)
                if body is None or _node_name(body) not in QUERY_TYPES:
                    counts["harness_out_of_scope"] += 1
                    continue
                if _node_name(body) == "ExplainStmt":
                    origin = "harness_explain"
                else:
                    origin = "harness"
                yield body, body_tree, origin, f"FuncCall:{names[-1]}", source_sql


def build_target(node: Any, tree: Any) -> tuple[Any, Any]:
    """Return the query qs must build as (AST, location-free JSON tree).

    qs does not model statements that only carry a query.  The carried query
    is the unit of construction and comparison: EXPLAIN, CREATE TABLE AS and
    DECLARE CURSOR are unwrapped, and a top-level SELECT INTO loses only its
    destination.  A carried statement qs cannot build (EXECUTE) is returned
    as is; the adapter classifies it.
    """

    name = _node_name(node)
    while name in _CARRIER_FIELDS:
        field = _CARRIER_FIELDS[name]
        node, tree = getattr(node, field, None), tree[name].get(field)
        name = _node_name(node)
    if _tree_tag(tree) != name:
        raise ValueError(f"parser representations disagree: AST {name!r}, JSON {_tree_tag(tree)!r}")
    if name == "SelectStmt" and node.intoClause is not None:
        node = copy.copy(node)
        node.intoClause = None
        tree = {name: {key: value for key, value in tree[name].items() if key != "intoClause"}}
    return node, _strip_locations(tree)


def _expected_file(root: Path, file: str) -> str | None:
    stem = Path(file).stem
    path = root / EXPECTED_ROOT / f"{stem}.out"
    return str(EXPECTED_ROOT / path.name) if path.is_file() else None


def _planner_file(file: str) -> bool:
    stem = Path(file).stem.lower()
    return any(needle in stem for needle in PLANNER_FILE_TOKENS)


def _families(node: Any, file: str, origin: str) -> tuple[str, ...]:
    names = {_node_name(value) for value in _walk_nodes(node)}
    families: set[str] = set()
    if "ExplainStmt" in names or origin == "explain":
        families.add("explain")
    if names & {"InsertStmt", "UpdateStmt", "DeleteStmt", "MergeStmt"}:
        families.add("dml")
    if "SelectStmt" in names:
        families.add("select")
    if "A_Expr" in names:
        families.add("operator")
    if "BoolExpr" in names or "BooleanTest" in names:
        families.add("boolean")
    if "FuncCall" in names:
        families.add("function")
    if "WindowDef" in names or "WindowClause" in names:
        families.add("window")
    if "JoinExpr" in names:
        families.add("join")
    if "CommonTableExpr" in names or "WithClause" in names:
        families.add("cte")
    if "SubLink" in names or "RangeSubselect" in names:
        families.add("subquery")
    if "CaseExpr" in names:
        families.add("case")
    if "ArrayExpr" in names or "ArrayRef" in names or "SubscriptingRef" in names:
        families.add("array")
    if "TypeCast" in names:
        families.add("cast")
    if "SortBy" in names or "limitCount" in names:
        families.add("pagination")
    lower = file.lower()
    for token, family in (
        ("json", "json"),
        ("range", "range"),
        ("tsquery", "fulltext"),
        ("tsearch", "fulltext"),
        ("window", "window"),
        ("aggregate", "aggregate"),
    ):
        if token in lower:
            families.add(family)
    return tuple(sorted(families)) or ("other",)


def census(root: Path) -> tuple[list[SQLUnit], list[QueryOccurrence], Counter[str]]:
    units: list[SQLUnit] = []
    occurrences: list[QueryOccurrence] = []
    counts: Counter[str] = Counter()
    for path in sorted((root / SQL_ROOT).glob("*.sql")):
        text = path.read_text(encoding="utf-8", errors="replace")
        rel = str(path.relative_to(root))
        file_units = extract_units(text, rel)
        for unit in file_units:
            current = unit
            if unit.kind != "sql":
                units.append(current)
                counts[current.kind] += 1
                continue
            if _has_psql_variable(unit.sql):
                current = dataclasses.replace(unit, kind="psql_variable", detail="psql variable substitution")
                units.append(current)
                counts[current.kind] += 1
                continue
            try:
                statements = _parse_statements(unit.sql)
            except Exception as exc:  # pglast's parser intentionally rejects some SQL files
                current = dataclasses.replace(unit, kind="parser_error", detail=str(exc))
                units.append(current)
                counts[current.kind] += 1
                continue
            if len(statements) != 1:
                current = dataclasses.replace(unit, kind="parser_error", detail="extractor produced multiple statements")
                units.append(current)
                counts[current.kind] += 1
                continue
            unit_index = len(units)
            units.append(current)
            counts["sql"] += 1
            stmt, stmt_tree = statements[0]
            body, body_tree, origin, wrapper = _query_node(stmt, stmt_tree)
            candidates: list[tuple[Any, Any, str, str | None, str]] = []
            if body is not None:
                candidates.append((body, body_tree, origin, wrapper, unit.sql))
            candidates.extend(
                (action, action_tree, action_origin, wrapper, unit.sql)
                for action, action_tree, action_origin in _rule_actions(stmt, stmt_tree)
            )
            candidates.extend(_literal_harness_queries(stmt, counts))
            if not candidates:
                statement_type = _node_name(stmt) or "unknown"
                current = dataclasses.replace(
                    unit,
                    kind="excluded_sql",
                    detail=f"out-of-scope statement {statement_type}",
                )
                units[unit_index] = current
                counts["excluded_sql"] += 1
                counts[f"excluded_{statement_type}"] += 1
            for body, body_tree, body_origin, body_wrapper, source_sql in candidates:
                type_name = _node_name(body)
                if type_name not in QUERY_TYPES:
                    continue
                if body_origin == "direct" and type_name == "ExplainStmt":
                    body_origin = "explain"
                expected = bool(_expected_hint(unit))
                target_ast, target_tree = build_target(body, body_tree)
                occurrence = QueryOccurrence(
                    id=len(occurrences),
                    unit=unit,
                    node=body,
                    statement_type=type_name,
                    origin=body_origin,
                    wrapper_type=body_wrapper,
                    source_sql=source_sql,
                    target_ast=target_ast,
                    target_tree=target_tree,
                    expected_error=expected,
                    expected_hint=_expected_hint(unit),
                    planner=_planner_file(unit.file) or type_name == "ExplainStmt",
                    families=_families(body, unit.file, body_origin),
                )
                occurrences.append(occurrence)
                counts["query_occurrence"] += 1
    return units, occurrences, counts


def _str_value(node: Any) -> str:
    if isinstance(node, str):
        return node
    if isinstance(node, pgast.String):
        return node.sval
    return str(node)


def _parameter_sentinel(occurrence_id: int, number: int) -> str:
    """Return the deterministic value bound to a source ``$n`` parameter.

    The occurrence id keeps values from separate frozen cases distinct while
    preserving the source parameter number in the value itself.  The Go
    adapter binds the same value; this side derives the expected order.
    """

    return f"qs-postgres-corpus-{occurrence_id}-param-{number}"


def _source_param_numbers(node: Any) -> tuple[int, ...]:
    """Collect source parameter numbers independently of the Go adapter."""

    numbers: list[int] = []

    def visit(value: Any) -> None:
        if isinstance(value, (tuple, list)):
            for item in value:
                visit(item)
        elif isinstance(value, pgast.Node):
            if _node_name(value) == "ParamRef":
                numbers.append(int(value.number))
                return
            # Each row-assignment target repeats the same source subtree;
            # the SQL contains that source, and its parameters, only once.
            if _node_name(value) == "MultiAssignRef" and value.colno != 1:
                return
            for slot in getattr(value, "__slots__", {}):
                visit(getattr(value, slot, None))

    visit(node)
    return tuple(numbers)


def _expected_arguments(node: Any, occurrence_id: int) -> tuple[str, ...]:
    """Return the expected rendered arguments from the source AST traversal."""

    return tuple(
        _parameter_sentinel(occurrence_id, number)
        for number in _source_param_numbers(node)
    )


def _default_ascending(node: Any) -> bool:
    """Report whether the source leaves a sort direction implicit.

    qs always renders ASC; ``canonical_ast`` treats the two as equal.
    """

    return any(
        _node_name(value) == "SortBy" and getattr(value.sortby_dir, "name", "") == "SORTBY_DEFAULT"
        for value in _walk_nodes(node)
    )


def canonical_ast(node: Any, *, parent: str = "", field: str = "") -> Any:
    """Convert pglast trees into a comparison form with narrow equivalences.

    Locations and parser bookkeeping are removed.  PostgreSQL's implicit
    ascending order and the default window frame spellings are semantic
    equivalences.  No literals, casts, type names, joins, set-operation
    grouping, NULLS direction, or boolean operator order is discarded.
    """

    if isinstance(node, enum.Enum):
        value = node.name
        if parent == "SortBy" and field == "sortby_dir" and value == "SORTBY_DEFAULT":
            return "SORTBY_ASC"
        return value
    if node is None or isinstance(node, (bool, int, float, str)):
        return node
    if isinstance(node, (tuple, list)):
        return [canonical_ast(value, parent=parent, field=field) for value in node]
    if isinstance(node, pgast.Node):
        result: dict[str, Any] = {"_type": type(node).__name__}
        for name in getattr(node, "__slots__", {}):
            if name in _LOCATION_FIELDS:
                continue
            value = getattr(node, name, None)
            if type(node).__name__ == "WindowDef" and name == "frameOptions" and value in {0x422, 0x423, 0x433}:
                # OVER () and its explicit RANGE UNBOUNDED PRECEDING /
                # CURRENT ROW spellings carry equivalent planner frames.
                result[name] = 0x422
                continue
            if type(node).__name__ == "TypeName" and name == "names":
                # Plain strings, not String nodes: frozen shape IDs hash this
                # form.  Qualification and spelling are kept exactly.
                result[name] = [_str_value(part) for part in (value or ())]
                continue
            result[name] = canonical_ast(value, parent=type(node).__name__, field=name)
        return result
    if hasattr(node, "name") and hasattr(node, "value"):
        return node.name
    return str(node)


def compare_trees(original: Any, rendered: Any) -> bool:
    """Report whether two trees are equal under ``canonical_ast`` alone."""

    return canonical_ast(original) == canonical_ast(rendered)


def query_shape(node: Any) -> Any:
    """Return a conservative query-shape tree for deduplicated diagnostics.

    Literal *kinds* remain part of the shape (integer, numeric, string, bit,
    boolean and NULL are not interchangeable), while literal payloads and
    parameter numbers are replaced.  Identifiers, casts, operators, joins,
    set grouping, NULLS direction and clause order remain intact.
    """

    value = canonical_ast(node)
    return _shape_value(value)


def _shape_value(value: Any) -> Any:
    """Rewrite an already-canonical tree without canonicalising it again."""

    if isinstance(value, dict):
        if value.get("_type") == "A_Const":
            literal = value.get("val")
            kind = literal.get("_type") if isinstance(literal, dict) else "unknown"
            return {"_type": "A_Const", "isnull": value.get("isnull"), "literal_kind": kind}
        if value.get("_type") == "ParamRef":
            return {"_type": "ParamRef"}
        return {key: _shape_value(item) for key, item in value.items()}
    if isinstance(value, list):
        return [_shape_value(item) for item in value]
    return value


def shape_id(node: Any) -> str:
    encoded = json.dumps(query_shape(node), sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return hashlib.sha256(encoded.encode("utf-8")).hexdigest()[:16]


def _source_hash(root: Path) -> str:
    digest = hashlib.sha256()
    for path in sorted((root / SQL_ROOT).glob("*.sql")):
        digest.update(str(path.relative_to(root)).encode())
        digest.update(b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def _discover_commit(root: Path) -> str:
    try:
        value = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "HEAD"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()
        if re.fullmatch(r"[0-9a-f]{40}", value, re.I):
            return value
    except (OSError, subprocess.CalledProcessError):
        pass
    match = _COMMIT_RE.search(root.name)
    return match.group(1) if match else "unknown"


def _canonical_oracles() -> None:
    """Reject accidental over-normalisation before measuring a corpus."""

    negative_pairs = (
        ("SELECT 1", "SELECT 2"),
        ("SELECT a FROM t WHERE a = 1", "SELECT a FROM t WHERE a = 2"),
        ("SELECT sum(x) OVER (ORDER BY a) FROM t", "SELECT sum(x) OVER (ORDER BY b) FROM t"),
        ("SELECT 1::pg_catalog.numeric", "SELECT 1::\"numeric\""),
        ("SELECT 1::custom.int4", "SELECT 1::custom.integer"),
        ("SELECT 1::\"INTEGER\"", "SELECT 1::integer"),
        ("SELECT (x).a FROM t", "SELECT (x).b FROM t"),
        ("SELECT (1)::interval(6)", "SELECT (1)::interval year to month"),
        ("SELECT XMLPARSE(DOCUMENT '<x/>')", "SELECT XMLPARSE(CONTENT '<x/>')"),
        (
            "SELECT JSON_QUERY('{\"a\": 1}', '$.a' WITHOUT WRAPPER)",
            "SELECT JSON_QUERY('{\"a\": 1}', '$.a' WITH WRAPPER)",
        ),
        (
            "SELECT JSON_VALUE('{}', '$.a' DEFAULT 'x' ON EMPTY)",
            "SELECT JSON_VALUE('{}', '$.a' ERROR ON EMPTY)",
        ),
        (
            "SELECT a FROM t ORDER BY a NULLS FIRST",
            "SELECT a FROM t ORDER BY a NULLS LAST",
        ),
    )
    for left, right in negative_pairs:
        if compare_trees(parse_sql(left)[0].stmt, parse_sql(right)[0].stmt):
            raise RuntimeError(f"canonical AST oracle collapsed distinct queries: {left!r} vs {right!r}")
    if shape_id(parse_sql("SELECT 1")[0].stmt) != shape_id(parse_sql("SELECT 2")[0].stmt):
        raise RuntimeError("query-shape oracle retained a literal payload")
    if shape_id(parse_sql("SELECT a FROM t")[0].stmt) == shape_id(parse_sql("SELECT b FROM t")[0].stmt):
        raise RuntimeError("query-shape oracle collapsed distinct identifiers")


def _render(repo: Path, occurrences: Sequence[QueryOccurrence]) -> dict[int, dict[str, Any]]:
    """Build and render every target tree through the Go adapter.

    One ``go run`` process reads ``{"id", "tree"}`` JSON lines and answers
    ``{"id", "outcome", "detail", "sql", "args"}`` lines.  The answer is only
    a candidate: ``_classify`` decides whether it is verified.
    """

    expected_ids = {occurrence.id for occurrence in occurrences}
    if len(expected_ids) != len(occurrences):
        raise RuntimeError("render input contains duplicate occurrence ids")
    payload = "".join(
        json.dumps(
            {"id": occurrence.id, "tree": occurrence.target_tree},
            ensure_ascii=False,
            separators=(",", ":"),
        )
        + "\n"
        for occurrence in occurrences
    )
    try:
        run = subprocess.run(
            list(RENDER_COMMAND),
            cwd=repo,
            input=payload.encode("utf-8"),
            capture_output=True,
            timeout=RENDER_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired as exc:
        raise RuntimeError(f"Go adapter exceeded the {RENDER_TIMEOUT_SECONDS}s timeout") from exc
    except OSError as exc:
        raise RuntimeError(f"cannot run the Go adapter: {exc}") from exc
    if run.returncode != 0:
        raise RuntimeError(
            f"Go adapter failed with exit {run.returncode}:\n"
            f"{run.stderr.decode('utf-8', errors='replace')[-4000:]}"
        )
    result: dict[int, dict[str, Any]] = {}
    for number, line in enumerate(run.stdout.decode("utf-8").splitlines(), 1):
        try:
            value = json.loads(line)
            identifier = int(value["id"])
            outcome = value["outcome"]
        except (ValueError, KeyError, TypeError) as exc:
            raise RuntimeError(f"Go adapter returned an invalid line {number}: {line[:200]!r}") from exc
        if identifier in result:
            raise RuntimeError(f"Go adapter returned duplicate occurrence id {identifier}")
        if outcome not in _RENDER_OUTCOMES:
            raise RuntimeError(f"Go adapter returned unknown outcome {outcome!r} for id {identifier}")
        result[identifier] = value
    missing = sorted(expected_ids - set(result))
    extra = sorted(set(result) - expected_ids)
    if missing or extra:
        details = []
        if missing:
            details.append(f"missing ids {missing[:8]}")
        if extra:
            details.append(f"unexpected ids {extra[:8]}")
        raise RuntimeError("Go adapter id set mismatch: " + "; ".join(details))
    return result


def _target_ast(sql: str) -> Any:
    statements = parse_sql(sql)
    if len(statements) != 1:
        raise ValueError("rendered SQL contains more than one statement")
    return statements[0].stmt


def _classify(occurrence: QueryOccurrence, rendering: dict[str, Any]) -> None:
    """Grade one adapter answer against the source tree.

    Only this function assigns ``verified``, and only after the rendered SQL
    reparsed to the target tree with the source parameter order.
    """

    outcome = rendering["outcome"]
    if outcome != "built":
        detail = str(rendering.get("detail", ""))
        if not detail:
            raise RuntimeError(f"Go adapter gave no detail for {outcome} id {occurrence.id}")
        occurrence.status = outcome
        occurrence.reason = detail
        return
    rendered = str(rendering.get("sql", ""))
    occurrence.generated_sql = rendered
    expected_args = list(_expected_arguments(occurrence.target_ast, occurrence.id))
    actual_args = list(rendering.get("args") or ())
    if actual_args != expected_args:
        occurrence.status = "arg_mismatch"
        occurrence.reason = (
            "bound arguments differ from source ParamRef order: "
            f"expected {expected_args!r}, got {actual_args!r}"
        )
        return
    try:
        rendered_ast = _target_ast(rendered)
    except Exception as exc:
        occurrence.status = "reparse_error"
        occurrence.reason = f"rendered SQL did not parse: {exc}"
        return
    if not compare_trees(occurrence.target_ast, rendered_ast):
        occurrence.status = "ast_mismatch"
        occurrence.reason = "rendered SQL AST differs from source AST"
        return
    occurrence.status = "verified"
    if _default_ascending(occurrence.target_ast):
        occurrence.normalization = ("default_ascending",)


def _record_json(occurrence: QueryOccurrence, root: Path) -> dict[str, Any]:
    unit = occurrence.unit
    value: dict[str, Any] = {
        "id": occurrence.id,
        "source": unit.file,
        "line": unit.line,
        "origin": occurrence.origin,
        "wrapper": occurrence.wrapper_type,
        "statement": occurrence.statement_type,
        "families": list(occurrence.families),
        "planner": occurrence.planner,
        "expected_error_hint": occurrence.expected_error,
        "status": occurrence.status,
        "shape": shape_id(occurrence.node),
    }
    expected_path = _expected_file(root, unit.file)
    if expected_path:
        value["expected_output"] = expected_path
    if occurrence.expected_hint:
        value["expected_hint"] = occurrence.expected_hint
    if occurrence.reason:
        value["reason"] = occurrence.reason
    if occurrence.normalization:
        value["normalization"] = list(occurrence.normalization)
    if occurrence.generated_sql:
        value["rendered_sql"] = occurrence.generated_sql
    return value


def make_report(
    root: Path,
    units: Sequence[SQLUnit],
    occurrences: list[QueryOccurrence],
    counts: Counter[str],
    commit: str,
    parser_version: str,
    rendered: bool,
    *,
    census_occurrences: int,
    limit: int | None,
) -> dict[str, Any]:
    statuses = Counter(item.status for item in occurrences)
    verified = [item for item in occurrences if item.status == "verified"]
    planner = [item for item in occurrences if item.planner]
    planner_verified = [item for item in planner if item.status == "verified"]
    shape_groups: dict[str, list[QueryOccurrence]] = defaultdict(list)
    for item in occurrences:
        shape_groups[shape_id(item.node)].append(item)
    distinct_verified = [group for group in shape_groups.values() if all(item.status == "verified" for item in group)]
    planner_shapes = [group for group in shape_groups.values() if any(item.planner for item in group)]
    planner_shapes_verified = [group for group in planner_shapes if all(item.status == "verified" for item in group)]
    family_counts: dict[str, dict[str, int]] = {}
    families = sorted({family for item in occurrences for family in item.families})
    for family in families:
        selected = [item for item in occurrences if family in item.families]
        family_counts[family] = {
            "total": len(selected),
            "verified": sum(item.status == "verified" for item in selected),
            "unverified": sum(item.status != "verified" for item in selected),
        }
    failures = Counter((item.status, item.reason) for item in occurrences if item.status != "verified")
    files = sorted({item.file for item in units})
    planner_files = sorted({unit.file for unit in units if _planner_file(unit.file)})
    report = {
        "schema": "qs.postgres-coverage.v2",
        "generated_at": __import__("datetime").datetime.now(__import__("datetime").timezone.utc).isoformat(),
        "repository": REPOSITORY,
        "revision": commit,
        "source": {
            "root": str(root),
            "sql_path": str(SQL_ROOT),
            "sql_files": len(files),
            "sql_sha256": _source_hash(root),
            "expected_path": str(EXPECTED_ROOT),
        },
        "parser": {"package": "pglast", "version": parser_version, "postgres": list(pglast.get_postgresql_version())},
        "scope": {
            "included": "parsable SELECT/VALUES/TABLE, INSERT, UPDATE, DELETE and MERGE query occurrences, including the queries carried by EXPLAIN, PREPARE, views, CREATE TABLE AS, cursors, SELECT INTO and rule actions",
            "harnesses": sorted(LITERAL_QUERY_HARNESSES),
            "excluded": "DDL, session/control, COPY payloads, psql commands and parser-invalid input; each is counted separately",
            "comparison": "rendered qs SQL is reparsed and compared with the source AST of the carried query; locations, explicit/default ASC, and the documented narrow equivalences are removed",
            "expected_error_classification": "comment-derived hint only; expected output files are linked per source file but do not prove a particular occurrence failed",
            "planner_file_tokens": list(PLANNER_FILE_TOKENS),
            "planner_files": planner_files,
            "partial": limit is not None,
            "limit": limit,
        },
        "counts": {
            "units": len(units),
            "sql_units": counts["sql"],
            "parser_errors": counts["parser_error"],
            "psql_commands": counts["psql"],
            "psql_variables": counts["psql_variable"],
            "excluded_sql": counts["excluded_sql"],
            "harness_parser_errors": counts["harness_parser_error"],
            "harness_out_of_scope": counts["harness_out_of_scope"],
            "census_query_occurrences": census_occurrences,
            "query_occurrences": len(occurrences),
            "expected_error_hints": sum(item.expected_error for item in occurrences),
            "verified": len(verified),
            "unverified": len(occurrences) - len(verified),
            "statuses": dict(sorted(statuses.items())),
            "verified_rate": (len(verified) / len(occurrences) if occurrences else 0.0),
            "planner_total": len(planner),
            "planner_verified": len(planner_verified),
            "planner_rate": (len(planner_verified) / len(planner) if planner else 0.0),
            "distinct_query_shapes": len(shape_groups),
            "distinct_verified_shapes": len(distinct_verified),
            "distinct_verified_rate": (len(distinct_verified) / len(shape_groups) if shape_groups else 0.0),
            "planner_distinct_shapes": len(planner_shapes),
            "planner_distinct_verified_shapes": len(planner_shapes_verified),
            "planner_distinct_rate": (len(planner_shapes_verified) / len(planner_shapes) if planner_shapes else 0.0),
        },
        "families": family_counts,
        "failure_reasons": [
            {
                "status": status,
                "reason": reason,
                "count": count,
                "examples": [item.id for item in occurrences if (item.status, item.reason) == (status, reason)][:8],
            }
            for (status, reason), count in failures.most_common()
        ],
        "render": {"ran": rendered, "command": list(RENDER_COMMAND)},
        "entries": [_record_json(item, root) for item in occurrences],
        "excluded_units": [
            {"source": unit.file, "line": unit.line, "kind": unit.kind, "detail": unit.detail}
            for unit in units
            if unit.kind != "sql"
        ],
    }
    return report


def _export_metrics(occurrences: Sequence[QueryOccurrence]) -> dict[str, int]:
    shape_groups: dict[str, list[QueryOccurrence]] = defaultdict(list)
    for occurrence in occurrences:
        shape_groups[shape_id(occurrence.node)].append(occurrence)
    distinct_verified = sum(
        all(item.status == "verified" for item in group)
        for group in shape_groups.values()
    )
    planner_groups = [group for group in shape_groups.values() if any(item.planner for item in group)]
    planner_distinct_verified = sum(
        all(item.status == "verified" for item in group)
        for group in planner_groups
    )
    return {
        "total": len(occurrences),
        **_status_counts(occurrences),
        "planner_total": sum(item.planner for item in occurrences),
        "planner_verified": sum(item.planner and item.status == "verified" for item in occurrences),
        "distinct_shapes": len(shape_groups),
        "distinct_verified_shapes": distinct_verified,
        "planner_distinct_shapes": len(planner_groups),
        "planner_distinct_verified_shapes": planner_distinct_verified,
    }


def _status_counts(items: Sequence[QueryOccurrence]) -> dict[str, int]:
    return {status: sum(item.status == status for item in items) for status in _STATUSES}


def _fixed_coverage_failures(
    metrics: dict[str, int],
    census_occurrences: int,
    *,
    commit: str | None = None,
    expected_sql_sha256: str | None = None,
    parser_version: str | None = None,
    parser_postgres_version: tuple[int, int] | None = None,
    require_pins: bool = True,
) -> list[str]:
    """Return failures against the immutable pinned corpus acceptance contract."""

    failures: list[str] = []
    expected_total = _PINNED_CORPUS_FLOORS["total"]
    if census_occurrences != expected_total:
        failures.append(
            f"census total {census_occurrences} differs from pinned {expected_total}"
        )
    if metrics.get("total") != expected_total:
        failures.append(
            f"derived total {metrics.get('total')} differs from pinned {expected_total}"
        )
    for field in (
        "planner_total",
        "distinct_shapes",
        "planner_distinct_shapes",
    ):
        expected = _PINNED_CORPUS_FLOORS[field]
        actual = metrics.get(field)
        if actual != expected:
            failures.append(f"{field} denominator {actual} differs from pinned {expected}")
    for field in (
        "verified",
        "planner_verified",
        "distinct_verified_shapes",
        "planner_distinct_verified_shapes",
    ):
        expected = _PINNED_CORPUS_FLOORS[field]
        actual = metrics.get(field, 0)
        if actual < expected:
            failures.append(f"{field} {actual} is below pinned floor {expected}")
    classified = sum(metrics.get(status, 0) for status in _STATUSES)
    if classified != metrics.get("total"):
        failures.append(
            f"{metrics.get('total', 0) - classified} occurrences have no final status"
        )
    if require_pins:
        if commit != _PINNED_POSTGRES_COMMIT:
            failures.append(
                f"source commit {commit!r} differs from pinned {_PINNED_POSTGRES_COMMIT}"
            )
        if expected_sql_sha256 != _PINNED_SQL_SHA256:
            failures.append("SQL source hash differs from the pinned regression corpus")
        if parser_version != _PINNED_PGLAST_VERSION:
            failures.append(
                f"parser version {parser_version!r} differs from pinned {_PINNED_PGLAST_VERSION}"
            )
        if parser_postgres_version != _PINNED_POSTGRES_VERSION:
            failures.append(
                "parser PostgreSQL version "
                f"{parser_postgres_version!r} differs from pinned {_PINNED_POSTGRES_VERSION!r}"
            )
    return failures


_BASELINE_RECORD_FIELDS = (
    "id",
    "source",
    "line",
    "origin",
    "wrapper",
    "statement",
    "families",
    "planner",
    "expected_error_hint",
    "expected_output",
    "shape",
    "original_sql",
    "tree",
)


def _fixture_name(index: int) -> str:
    return f"{_FIXTURE_DIRECTORY}/corpus-{index:02d}.jsonl"


def _read_baseline_fixtures(path: Path) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    """Read and authenticate the checked-in JSONL fixture baseline."""

    manifest_path = path / "manifest.json"
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise SystemExit(f"cannot read baseline fixture manifest {manifest_path}: {exc}") from exc
    if not isinstance(manifest, dict) or manifest.get("schema") != _CORPUS_SCHEMA:
        raise SystemExit(f"baseline fixture has unsupported schema: {manifest_path}")
    files = manifest.get("files")
    counts = manifest.get("counts")
    if not isinstance(files, list) or not files or not isinstance(counts, dict):
        raise SystemExit("baseline fixture manifest has no file inventory or counts")

    records: list[dict[str, Any]] = []
    base = path.resolve()
    for index, entry in enumerate(files):
        if not isinstance(entry, dict) or entry.get("path") != _fixture_name(index):
            raise SystemExit(f"baseline fixture file {index} has invalid manifest entry")
        jsonl_path = (path / entry["path"]).resolve()
        try:
            jsonl_path.relative_to(base)
        except ValueError as exc:
            raise SystemExit(f"baseline fixture file {index} escapes its root") from exc
        try:
            data = jsonl_path.read_bytes()
        except OSError as exc:
            raise SystemExit(f"cannot read baseline fixture {jsonl_path}: {exc}") from exc
        digest = hashlib.sha256(data).hexdigest()
        if digest != entry.get("sha256"):
            raise SystemExit(
                f"baseline fixture file {index} hash mismatch: expected {entry.get('sha256')}, got {digest}"
            )
        file_records: list[dict[str, Any]] = []
        for line_number, line in enumerate(data.splitlines(), 1):
            try:
                item = json.loads(line)
            except ValueError as exc:
                raise SystemExit(
                    f"baseline fixture file {index} line {line_number} is invalid JSON"
                ) from exc
            if not isinstance(item, dict):
                raise SystemExit(f"baseline fixture file {index} line {line_number} is not an object")
            file_records.append(item)
        first = len(records)
        if entry.get("first_id") != first or entry.get("count") != len(file_records):
            raise SystemExit(f"baseline fixture file {index} inventory disagrees with JSONL records")
        if [item.get("id") for item in file_records] != list(range(first, first + len(file_records))):
            raise SystemExit(f"baseline fixture file {index} IDs are not contiguous")
        local = Counter(item.get("status") for item in file_records)
        if set(local) - set(_STATUSES) or any(entry.get(status) != local.get(status, 0) for status in _STATUSES):
            raise SystemExit(f"baseline fixture file {index} status counts disagree with JSONL")
        records.extend(file_records)

    if counts.get("total") != len(records):
        raise SystemExit("baseline fixture manifest total disagrees with JSONL records")
    shape_groups: dict[Any, list[dict[str, Any]]] = defaultdict(list)
    for item in records:
        shape_groups[item.get("shape")].append(item)
    derived_counts = {
        **{status: sum(item.get("status") == status for item in records) for status in _STATUSES},
        "planner_total": sum(bool(item.get("planner")) for item in records),
        "planner_verified": sum(bool(item.get("planner")) and item.get("status") == "verified" for item in records),
        "distinct_shapes": len(shape_groups),
        "distinct_verified_shapes": sum(
            all(item.get("status") == "verified" for item in group)
            for group in shape_groups.values()
        ),
    }
    planner_groups = [group for group in shape_groups.values() if any(item.get("planner") for item in group)]
    derived_counts["planner_distinct_shapes"] = len(planner_groups)
    derived_counts["planner_distinct_verified_shapes"] = sum(
        all(item.get("status") == "verified" for item in group)
        for group in planner_groups
    )
    for field, value in derived_counts.items():
        if counts.get(field) != value:
            raise SystemExit(
                f"baseline fixture manifest {field} disagrees with JSONL: "
                f"expected {value}, got {counts.get(field)}"
            )
    return manifest, records


def _compare_fixture_baseline(
    occurrences: Sequence[QueryOccurrence],
    baseline_path: Path,
    *,
    source_root: Path,
    commit: str,
    sql_sha256: str,
    parser_version: str,
    parser_postgres_version: tuple[int, int],
) -> None:
    """Reject export changes that regress or reshuffle the checked-in corpus."""

    manifest, baseline_records = _read_baseline_fixtures(baseline_path)
    expected_parser = {
        "package": "pglast",
        "version": parser_version,
        "postgresql": f"{parser_postgres_version[0]}.{parser_postgres_version[1]}",
    }
    expected_manifest = {
        "schema": _CORPUS_SCHEMA,
        "revision": commit,
        "sql_sha256": sql_sha256,
        "parser": expected_parser,
    }
    for key, expected in expected_manifest.items():
        if manifest.get(key) != expected:
            raise SystemExit(
                f"baseline fixture {key} disagrees with pinned source: "
                f"expected {expected!r}, got {manifest.get(key)!r}"
            )

    current = [_fixture_record(item, source_root) for item in occurrences]
    if len(current) != len(baseline_records):
        raise SystemExit(
            "baseline fixture occurrence count changed: "
            f"expected {len(baseline_records)}, got {len(current)}"
        )
    for position, (before, after) in enumerate(zip(baseline_records, current, strict=True)):
        if before.get("id") != after.get("id"):
            raise SystemExit(
                f"baseline fixture occurrence order changed at position {position}: "
                f"expected id {before.get('id')}, got {after.get('id')}"
            )
        for field in _BASELINE_RECORD_FIELDS:
            if before.get(field) != after.get(field):
                raise SystemExit(
                    f"baseline fixture id {after.get('id')} {field} changed: "
                    f"expected {before.get(field)!r}, got {after.get(field)!r}"
                )
        before_status = before.get("status")
        after_status = after.get("status")
        if before_status == "verified":
            if after_status != "verified":
                raise SystemExit(
                    f"baseline fixture id {after.get('id')} lost verified coverage: {after_status!r}"
                )
            for field in ("want_sql", "want_args", "normalization"):
                if before.get(field) != after.get(field):
                    raise SystemExit(
                        f"baseline fixture id {after.get('id')} {field} changed: "
                        f"expected {before.get(field)!r}, got {after.get(field)!r}"
                    )
        elif before_status == "out_of_scope":
            # A feature that enters scope is measured from then on: its queries
            # may verify, expose a gap or be refused, none of which is a loss.
            continue
        elif after_status not in {before_status, "verified"}:
            raise SystemExit(
                f"baseline fixture id {after.get('id')} status regressed: "
                f"expected {before_status!r} or verified, got {after_status!r}"
            )

    before_counts = manifest["counts"]
    after_counts = _export_metrics(occurrences)
    for field in ("total", "planner_total", "distinct_shapes", "planner_distinct_shapes"):
        if before_counts.get(field) != after_counts.get(field):
            raise SystemExit(
                f"baseline fixture {field} changed: expected {before_counts.get(field)}, "
                f"got {after_counts.get(field)}"
            )
    for field in (
        "verified",
        "planner_verified",
        "distinct_verified_shapes",
        "planner_distinct_verified_shapes",
    ):
        if after_counts.get(field, 0) < before_counts.get(field, 0):
            raise SystemExit(
                f"baseline fixture {field} decreased: expected at least {before_counts.get(field)}, "
                f"got {after_counts.get(field)}"
            )


def _validate_export_state(
    occurrences: Sequence[QueryOccurrence],
    census_occurrences: int,
    renderings: dict[int, dict[str, Any]],
    *,
    expected_sql_sha256: str | None,
    commit: str | None,
    baseline_fixtures: Path | None = None,
    source_root: Path | None = None,
    parser_postgres_version: tuple[int, int] | None = None,
) -> None:
    """Reject anything that could produce a misleading frozen fixture."""

    if expected_sql_sha256 is None:
        raise SystemExit("--export-fixtures requires --expected-sql-sha256")
    if commit is None or not re.fullmatch(r"[0-9a-f]{40}", commit, re.I):
        raise SystemExit("--export-fixtures requires an explicit 40-character --commit")
    if pglast.__version__ != _PINNED_PGLAST_VERSION:
        raise SystemExit(
            f"--export-fixtures requires pglast {_PINNED_PGLAST_VERSION}, got {pglast.__version__}"
        )
    if tuple(pglast.get_postgresql_version()) != _PINNED_POSTGRES_VERSION:
        raise SystemExit(
            "--export-fixtures requires the PostgreSQL "
            f"{_PINNED_POSTGRES_VERSION[0]}.{_PINNED_POSTGRES_VERSION[1]} parser"
        )
    if len(occurrences) != census_occurrences:
        raise SystemExit("--export-fixtures requires the complete unbounded corpus")
    identifiers = [item.id for item in occurrences]
    if identifiers != list(range(census_occurrences)):
        raise SystemExit("--export-fixtures requires contiguous ordered occurrence ids")
    if set(renderings) != set(identifiers):
        missing = sorted(set(identifiers) - set(renderings))
        extra = sorted(set(renderings) - set(identifiers))
        raise SystemExit(
            "--export-fixtures render id set mismatch: "
            f"missing={missing[:8]!r} extra={extra[:8]!r}"
        )
    invalid = [item for item in occurrences if item.status not in _STATUSES]
    if invalid:
        raise SystemExit(
            "--export-fixtures requires every occurrence to have a final status; "
            f"first id {invalid[0].id} has status {invalid[0].status!r}: {invalid[0].reason}"
        )
    for item in occurrences:
        outcome = renderings[item.id].get("outcome")
        if item.status == "verified":
            # A verified record freezes exactly the SQL that was reparsed.
            if outcome != "built" or not item.generated_sql or item.generated_sql != renderings[item.id].get("sql"):
                raise SystemExit(f"verified occurrence {item.id} does not match its rendering")
        elif outcome != item.status or not item.reason or item.generated_sql:
            raise SystemExit(f"{item.status} occurrence {item.id} does not match its rendering")
    fixed_failures = _fixed_coverage_failures(
        _export_metrics(occurrences),
        census_occurrences,
        commit=commit,
        expected_sql_sha256=expected_sql_sha256,
        parser_version=pglast.__version__,
        parser_postgres_version=parser_postgres_version or tuple(pglast.get_postgresql_version()),
    )
    if fixed_failures:
        raise SystemExit("--export-fixtures fixed pinned coverage failed: " + "; ".join(fixed_failures))
    if baseline_fixtures is not None:
        if source_root is None:
            raise SystemExit("--export-fixtures baseline comparison requires the PostgreSQL source root")
        postgres_version = parser_postgres_version or tuple(pglast.get_postgresql_version())
        _compare_fixture_baseline(
            occurrences,
            baseline_fixtures,
            source_root=source_root,
            commit=commit,
            sql_sha256=expected_sql_sha256,
            parser_version=pglast.__version__,
            parser_postgres_version=postgres_version,
        )


def _fixture_record(occurrence: QueryOccurrence, root: Path) -> dict[str, Any]:
    """Return the complete immutable record stored in JSONL.

    The Go test receives the tree to build and the frozen expectation, so it
    needs neither pglast nor the PostgreSQL source.
    """

    verified = occurrence.status == "verified"
    return {
        "id": occurrence.id,
        "source": occurrence.unit.file,
        "line": occurrence.unit.line,
        "origin": occurrence.origin,
        "wrapper": occurrence.wrapper_type or "",
        "statement": occurrence.statement_type,
        "families": list(occurrence.families),
        "planner": occurrence.planner,
        "expected_error_hint": occurrence.expected_error,
        "expected_output": _expected_file(root, occurrence.unit.file) or "",
        "status": occurrence.status,
        "reason": occurrence.reason,
        "shape": shape_id(occurrence.node),
        "original_sql": occurrence.source_sql,
        "tree": occurrence.target_tree,
        "normalization": list(occurrence.normalization) if verified else [],
        "want_sql": occurrence.generated_sql if verified else "",
        "want_args": list(_expected_arguments(occurrence.target_ast, occurrence.id)) if verified else [],
    }


def _jsonl_bytes(items: Sequence[QueryOccurrence], root: Path) -> bytes:
    """Encode fixture records with one deterministic UTF-8 JSON object per line."""

    return b"".join(
        json.dumps(
            _fixture_record(item, root),
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
        ).encode("utf-8")
        + b"\n"
        for item in items
    )


def _export_fixtures(
    output: Path,
    occurrences: Sequence[QueryOccurrence],
    *,
    source_root: Path,
    commit: str,
    sql_sha256: str,
    parser_version: str,
    parser_postgres_version: tuple[int, int],
) -> None:
    """Write the checked-in fixtures and manifest after full verification."""

    if len(occurrences) == 0:
        raise SystemExit("--export-fixtures requires at least one occurrence")
    if [item.id for item in occurrences] != list(range(len(occurrences))):
        raise SystemExit("--export-fixtures requires contiguous ordered occurrence ids")
    directory = output / _FIXTURE_DIRECTORY
    directory.mkdir(parents=True, exist_ok=True)
    manifest_files: list[dict[str, Any]] = []
    written: set[Path] = set()
    for index, first in enumerate(range(0, len(occurrences), _FIXTURE_RECORDS)):
        items = occurrences[first : first + _FIXTURE_RECORDS]
        data = _jsonl_bytes(items, source_root)
        path = output / _fixture_name(index)
        path.write_bytes(data)
        written.add(path)
        manifest_files.append(
            {
                "path": _fixture_name(index),
                "sha256": hashlib.sha256(data).hexdigest(),
                "first_id": first,
                "count": len(items),
                **_status_counts(items),
            }
        )
    # A shorter corpus must not leave a stale trailing file behind.
    for stale in directory.glob("corpus-*.jsonl"):
        if stale not in written:
            stale.unlink()
    manifest = {
        "schema": _CORPUS_SCHEMA,
        "revision": commit,
        "sql_sha256": sql_sha256,
        "parser": {
            "package": "pglast",
            "version": parser_version,
            "postgresql": f"{parser_postgres_version[0]}.{parser_postgres_version[1]}",
        },
        "counts": _export_metrics(occurrences),
        "files": manifest_files,
    }
    (output / "manifest.json").write_text(
        json.dumps(manifest, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )


def run(args: argparse.Namespace) -> int:
    _canonical_oracles()
    export = getattr(args, "export_fixtures", None)
    if export is not None:
        if not args.render:
            raise SystemExit("--export-fixtures requires the Go adapter")
        if args.limit is not None:
            raise SystemExit("--export-fixtures requires the complete corpus; remove --limit")
        if not getattr(args, "expected_sql_sha256", None):
            raise SystemExit("--export-fixtures requires --expected-sql-sha256")
        if not getattr(args, "commit", None):
            raise SystemExit("--export-fixtures requires an explicit --commit")
    root = args.postgres_root.resolve()
    repo = args.repo.resolve()
    sql_path = root / SQL_ROOT
    if not sql_path.is_dir():
        raise SystemExit(f"PostgreSQL source does not contain {SQL_ROOT}: {root}")
    expected_hash = getattr(args, "expected_sql_sha256", None)
    if expected_hash is not None:
        if not re.fullmatch(r"[0-9a-f]{64}", expected_hash, re.I):
            raise SystemExit("--expected-sql-sha256 must be a 64-character hexadecimal digest")
        actual_hash = _source_hash(root)
        if actual_hash.lower() != expected_hash.lower():
            raise SystemExit(
                f"PostgreSQL regression SQL hash mismatch: expected {expected_hash}, got {actual_hash}"
            )
    units, occurrences, counts = census(root)
    census_occurrences = len(occurrences)
    if args.limit:
        occurrences = occurrences[: args.limit]

    renderings: dict[int, dict[str, Any]] = {}
    if args.render and occurrences:
        renderings = _render(repo, occurrences)
        for occurrence in occurrences:
            _classify(occurrence, renderings[occurrence.id])
    else:
        for occurrence in occurrences:
            occurrence.status = "parsed"

    commit = args.commit or _discover_commit(root)
    report = make_report(
        root,
        units,
        occurrences,
        counts,
        commit,
        pglast.__version__,
        bool(args.render),
        census_occurrences=census_occurrences,
        limit=args.limit,
    )
    minimum_support = _threshold_fraction(getattr(args, "minimum_support", None))
    minimum_planner_support = _threshold_fraction(getattr(args, "minimum_planner_support", None))
    minimum_shape_support = _threshold_fraction(getattr(args, "minimum_shape_support", None))
    minimum_planner_shape_support = _threshold_fraction(
        getattr(args, "minimum_planner_shape_support", None)
    )
    require_pinned_coverage = bool(getattr(args, "require_pinned_coverage", False))
    threshold_reasons: list[str] = []
    if (
        minimum_support is not None
        or minimum_planner_support is not None
        or minimum_shape_support is not None
        or minimum_planner_shape_support is not None
    ):
        if args.limit is not None:
            threshold_reasons.append("--limit produces a partial corpus")
        if not args.render:
            threshold_reasons.append("a threshold requires the Go adapter")
        if minimum_support is not None and report["counts"]["verified_rate"] < minimum_support:
            threshold_reasons.append(
                f"verified rate {report['counts']['verified_rate']:.4f} is below {minimum_support:.4f}"
            )
        if minimum_planner_support is not None and report["counts"]["planner_rate"] < minimum_planner_support:
            threshold_reasons.append(
                f"planner rate {report['counts']['planner_rate']:.4f} is below {minimum_planner_support:.4f}"
            )
        if minimum_shape_support is not None and report["counts"]["distinct_verified_rate"] < minimum_shape_support:
            threshold_reasons.append(
                f"distinct-shape rate {report['counts']['distinct_verified_rate']:.4f} is below {minimum_shape_support:.4f}"
            )
        if (
            minimum_planner_shape_support is not None
            and report["counts"]["planner_distinct_rate"] < minimum_planner_shape_support
        ):
            threshold_reasons.append(
                "planner distinct-shape rate "
                f"{report['counts']['planner_distinct_rate']:.4f} is below "
                f"{minimum_planner_shape_support:.4f}"
            )
    if require_pinned_coverage:
        if args.limit is not None:
            threshold_reasons.append("--require-pinned-coverage requires the complete unbounded corpus")
        if not args.render:
            threshold_reasons.append("--require-pinned-coverage requires the Go adapter")
        fixed_failures = _fixed_coverage_failures(
            _export_metrics(occurrences),
            census_occurrences,
            commit=commit,
            expected_sql_sha256=getattr(args, "expected_sql_sha256", None),
            parser_version=pglast.__version__,
            parser_postgres_version=tuple(pglast.get_postgresql_version()),
        )
        threshold_reasons.extend(
            f"--require-pinned-coverage: {failure}" for failure in fixed_failures
        )
    baseline_fixtures = getattr(args, "baseline_fixtures", None)
    if baseline_fixtures is None:
        baseline_fixtures = repo / "internal" / "postgrescorpus"
    else:
        baseline_fixtures = Path(baseline_fixtures).resolve()
    if require_pinned_coverage and export is None and args.limit is None and args.render:
        try:
            _compare_fixture_baseline(
                occurrences,
                baseline_fixtures,
                source_root=root,
                commit=commit,
                sql_sha256=getattr(args, "expected_sql_sha256", None) or _source_hash(root),
                parser_version=pglast.__version__,
                parser_postgres_version=tuple(pglast.get_postgresql_version()),
            )
        except SystemExit as exc:
            threshold_reasons.append(f"--require-pinned-coverage baseline: {exc}")
    report["thresholds"] = {
        "minimum_support": minimum_support,
        "minimum_planner_support": minimum_planner_support,
        "minimum_shape_support": minimum_shape_support,
        "minimum_planner_shape_support": minimum_planner_shape_support,
        "require_pinned_coverage": require_pinned_coverage,
        "passed": not threshold_reasons,
        "failures": threshold_reasons,
    }
    if export is not None:
        _validate_export_state(
            occurrences,
            census_occurrences,
            renderings,
            expected_sql_sha256=args.expected_sql_sha256,
            commit=args.commit,
            baseline_fixtures=baseline_fixtures,
            source_root=root,
            parser_postgres_version=tuple(pglast.get_postgresql_version()),
        )
        _export_fixtures(
            Path(export).resolve(),
            occurrences,
            commit=args.commit,
            source_root=root,
            sql_sha256=_source_hash(root),
            parser_version=pglast.__version__,
            parser_postgres_version=tuple(pglast.get_postgresql_version()),
        )
    report_path = args.report.resolve()
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(json.dumps(report["counts"], indent=2, sort_keys=True))
    print(f"report: {report_path}")
    if threshold_reasons:
        print("threshold: FAIL; " + "; ".join(threshold_reasons))
        return 1
    return 0


def _threshold_fraction(value: float | None) -> float | None:
    if value is None:
        return None
    fraction = value / 100.0 if value > 1 else value
    if not 0 <= fraction <= 1:
        raise SystemExit("coverage thresholds must be between 0 and 1, or 0 and 100 percent")
    return fraction


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("postgres_root", type=Path, help="pinned PostgreSQL source archive or checkout")
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parent.parent, help="qs repository whose Go adapter renders the queries")
    parser.add_argument("--report", type=Path, default=Path("postgres-coverage.json"), help="coverage report path")
    parser.add_argument("--commit", help="40-character PostgreSQL commit SHA when the source is not a git checkout")
    parser.add_argument("--expected-sql-sha256", help="fail if the regression SQL tree hash differs from this digest")
    parser.add_argument(
        "--export-fixtures",
        type=Path,
        help="write the JSONL fixtures and manifest into this directory after complete verification",
    )
    parser.add_argument(
        "--baseline-fixtures",
        type=Path,
        help="fixture directory used as the pre-export corpus baseline (defaults to repo/internal/postgrescorpus)",
    )
    parser.add_argument(
        "--require-pinned-coverage",
        action="store_true",
        help="require the complete pinned PostgreSQL corpus and its fixed verified floors",
    )
    parser.add_argument("--limit", type=int, help="process only the first N query occurrences (development smoke check)")
    parser.add_argument(
        "--minimum-support",
        type=float,
        help="fail after writing the report unless full-corpus verified occurrence coverage reaches this fraction or percent",
    )
    parser.add_argument(
        "--minimum-planner-support",
        type=float,
        help="fail after writing the report unless full-corpus planner coverage reaches this fraction or percent",
    )
    parser.add_argument(
        "--minimum-shape-support",
        type=float,
        help="fail after writing the report unless distinct normalized-shape coverage reaches this fraction or percent",
    )
    parser.add_argument(
        "--minimum-planner-shape-support",
        type=float,
        help="fail after writing the report unless distinct normalized planner-shape coverage reaches this fraction or percent",
    )
    parser.add_argument("--no-render", dest="render", action="store_false", help="only extract and parse; do not run the Go adapter")
    parser.set_defaults(render=True)
    args = parser.parse_args(argv)
    if args.limit is not None and args.limit < 1:
        parser.error("--limit must be positive")
    return run(args)


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main())
