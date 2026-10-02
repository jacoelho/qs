#!/usr/bin/env python3
"""Measure PostgreSQL regression query construction with qs.

This is a development-time tool.  It deliberately keeps PostgreSQL and
``pglast`` out of the qs runtime and uses a short-lived Go probe for the
queries that can be expressed with public qs constructors.  A query is
counted as supported only after the probe builds it, the rendered SQL parses
again with the pinned PostgreSQL parser, and the reparsed tree matches the
source tree after the small normalisations documented in ``canonical_ast``.

The extractor is independent of pglast's statement splitter.  PostgreSQL's
regression files contain psql commands, COPY stdin data, nested comments,
dollar quoted bodies and intentionally malformed statements; those must be
reported rather than silently discarded.

Typical invocation (from the qs repository):

    python scripts/postgres_corpus.py \
      /path/to/postgres-<commit> \
      --repo . --report docs/postgres-coverage.json

The Go probe is transient unless ``--probe-dir`` is supplied.  ``--export-go``
writes eight fixed JSONL shards and test-only typed builders after complete
verification. The command does not contact the network. Install the pinned
parser from ``scripts/requirements-postgres.txt`` in a development virtualenv.
"""

from __future__ import annotations

import argparse
import dataclasses
import enum
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any, Iterable, Iterator, Sequence

try:
    import pglast
    from pglast import ast as pgast
    from pglast import enums as pgenums
    from pglast import parse_sql
except ImportError as exc:  # pragma: no cover - exercised by the CLI
    raise SystemExit(
        "pglast is required for PostgreSQL corpus measurement; "
        "install scripts/requirements-postgres.txt"
    ) from exc


REPOSITORY = "https://github.com/postgres/postgres"
PROBE_TIMEOUT_SECONDS = 180
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
_CORPUS_SCHEMA = "qs-postgres-corpus-v2"
_CORPUS_SHARDS = 8
_CORPUS_GROUP_SIZE = 256


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
    node: Any
    statement_type: str
    origin: str
    wrapper_type: str | None
    source_sql: str
    source_ast: Any
    expected_error: bool
    expected_hint: str
    planner: bool
    families: tuple[str, ...]
    status: str = "unverified"
    reason: str = ""
    generated_sql: str = ""
    normalization: tuple[str, ...] = ()
    builder: str = ""


class Unsupported(Exception):
    """The corpus adapter cannot translate this AST through the public qs API."""

    def __init__(self, reason: str):
        self.reason = reason
        super().__init__(reason)


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


def _query_node(stmt: Any) -> tuple[Any | None, str, str | None]:
    name = _node_name(stmt)
    if name in QUERY_TYPES:
        return stmt, "direct", None
    if name in WRAPPER_FIELDS:
        field, origin = WRAPPER_FIELDS[name]
        return getattr(stmt, field, None), origin, name
    if name == "RuleStmt":
        return None, "rule", name
    return None, "", None


def _rule_actions(stmt: Any) -> Iterator[tuple[Any, str]]:
    if _node_name(stmt) != "RuleStmt":
        return
    for action in getattr(stmt, "actions", ()) or ():
        if _node_name(action) in QUERY_TYPES:
            yield action, "rule_action"


def _literal_harness_queries(stmt: Any, counts: Counter[str]) -> Iterator[tuple[Any, str, str, str]]:
    """Extract fixed SQL passed to PostgreSQL's EXPLAIN test harnesses.

    These calls are ordinary SELECTs in the regression source, but the string
    argument is the SQL actually sent to the planner.  Counting only the
    outer function call would hide EXPLAIN options and the tested query body.
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
                statements = parse_sql(source_sql)
            except Exception as exc:
                counts["harness_parser_error"] += 1
                counts[f"harness_parser_error:{type(exc).__name__}"] += 1
                continue
            for parsed in statements:
                body, origin, wrapper = _query_node(parsed.stmt)
                if body is None or _node_name(body) not in QUERY_TYPES:
                    counts["harness_out_of_scope"] += 1
                    continue
                if _node_name(body) == "ExplainStmt":
                    origin = "harness_explain"
                else:
                    origin = "harness"
                yield body, origin, f"FuncCall:{names[-1]}", source_sql


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
                statements = parse_sql(unit.sql)
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
            stmt = statements[0].stmt
            body, origin, wrapper = _query_node(stmt)
            candidates: list[tuple[Any, str, str | None, str]] = []
            if body is not None:
                candidates.append((body, origin, wrapper, unit.sql))
            candidates.extend((action, action_origin, wrapper, unit.sql) for action, action_origin in _rule_actions(stmt))
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
            for body, body_origin, body_wrapper, source_sql in candidates:
                type_name = _node_name(body)
                if type_name not in QUERY_TYPES:
                    continue
                if body_origin == "direct" and type_name == "ExplainStmt":
                    body_origin = "explain"
                expected = bool(_expected_hint(unit))
                occurrence = QueryOccurrence(
                    id=len(occurrences),
                    unit=unit,
                    node=body,
                    statement_type=type_name,
                    origin=body_origin,
                    wrapper_type=body_wrapper,
                    source_sql=source_sql,
                    source_ast=body,
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


def _go_quote(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def _parameter_sentinel(occurrence_id: int | None, number: int) -> str:
    """Return the deterministic value bound to a source ``$n`` parameter.

    The occurrence id keeps values from separate frozen cases distinct while
    preserving the source parameter number in the value itself.  A missing id
    is useful for the small hand-written adapter probes and keeps their output
    deterministic too.
    """

    if occurrence_id is None:
        return f"qs-postgres-param-{number}"
    return f"qs-postgres-corpus-{occurrence_id}-param-{number}"


def _source_param_numbers(node: Any) -> tuple[int, ...]:
    """Collect source parameter numbers independently of the Go emitter."""

    return tuple(
        int(value.number)
        for value in _walk_nodes(node)
        if _node_name(value) == "ParamRef"
    )


def _expected_arguments(node: Any, occurrence_id: int | None) -> tuple[str, ...]:
    """Return the expected probe arguments from the source AST traversal."""

    return tuple(
        _parameter_sentinel(occurrence_id, number)
        for number in _source_param_numbers(node)
    )


def _join(parts: Iterable[str]) -> str:
    return ", ".join(parts)


def _enum_name(value: Any) -> str:
    return getattr(value, "name", str(value))


class GoEmitter:
    """Translate the structural PostgreSQL AST subset into qs calls."""

    def __init__(self, occurrence_id: int | None = None) -> None:
        self.occurrence_id = occurrence_id
        self.param_numbers: dict[int, int] = {}
        self.next_param = 1
        self.normalizations: set[str] = set()

    def unsupported(self, reason: str) -> Unsupported:
        return Unsupported(reason)

    def expr(self, node: Any) -> str:
        if node is None:
            raise self.unsupported("missing expression")
        name = _node_name(node)
        if name == "ColumnRef":
            fields = list(getattr(node, "fields", ()) or ())
            if not fields:
                raise self.unsupported("ColumnRef has no fields")
            values = [_str_value(f) if _node_name(f) != "A_Star" else "*" for f in fields]
            if values[-1] == "*":
                if len(values) == 1:
                    return "qs.Star()"
                return f"qs.Star({_go_quote('.'.join(values[:-1]))})"
            return f"qs.Ident({_join(_go_quote(value) for value in values)})"
        if name == "A_Const":
            if getattr(node, "isnull", False):
                return "qs.NullLiteral()"
            return self.expr(getattr(node, "val", None))
        if name == "Integer":
            return f"qs.LiteralInt({int(node.ival)})"
        if name == "Float":
            value = str(node.fval)
            # Keep PostgreSQL's token spelling: converting through float64
            # changes large decimal/exponent literals and can alter the AST.
            if value.lower() in {"nan", "infinity", "-infinity"}:
                raise self.unsupported("non-finite float literal")
            return f"qs.LiteralNumeric({_go_quote(value)})"
        if name == "String":
            return f"qs.LiteralString({_go_quote(node.sval)})"
        if name == "Boolean":
            return f"qs.LiteralBool({str(bool(node.boolval)).lower()})"
        if name == "BitString":
            value = str(node.bsval)
            if not value or value[0].lower() not in {"b", "x"}:
                raise self.unsupported("unknown bit string literal prefix")
            constructor = "LiteralBit" if value[0].lower() == "b" else "LiteralHex"
            return f"qs.{constructor}({_go_quote(value[1:])})"
        if name == "ParamRef":
            number = int(node.number)
            prior = self.param_numbers.get(number)
            if prior is not None:
                raise self.unsupported("reused parameter number cannot be represented without raw SQL")
            if number != self.next_param:
                raise self.unsupported("non-sequential parameter reference")
            self.param_numbers[number] = self.next_param
            self.next_param += 1
            sentinel = _parameter_sentinel(self.occurrence_id, number)
            return f"qs.Param[any]({_go_quote(sentinel)})"
        if name == "SetToDefault":
            return "qs.Default()"
        if name == "TypeCast":
            typ = self.data_type(node.typeName)
            return f"({self.expr(node.arg)}).Cast({typ})"
        if name == "A_Expr":
            return self.a_expr(node)
        if name == "BoolExpr":
            args = [f"qs.AsCondition({self.expr(arg)})" for arg in node.args]
            op = _enum_name(node.boolop)
            if op == "AND_EXPR":
                return f"qs.And({_join(args)}).Expr()"
            if op == "OR_EXPR":
                return f"qs.Or({_join(args)}).Expr()"
            if op == "NOT_EXPR" and len(args) == 1:
                return f"qs.Not({args[0]}).Expr()"
            raise self.unsupported(f"unsupported BoolExpr shape {op}")
        if name == "NullTest":
            value = self.expr(node.arg)
            kind = _enum_name(node.nulltesttype)
            if kind == "IS_NULL":
                return f"({value}).IsNull().Expr()"
            if kind == "IS_NOT_NULL":
                return f"({value}).IsNotNull().Expr()"
            raise self.unsupported(f"unsupported NullTest {kind}")
        if name == "BooleanTest":
            value = self.expr(node.arg)
            method = {
                "IS_TRUE": "IsTrue",
                "IS_NOT_TRUE": "IsNotTrue",
                "IS_FALSE": "IsFalse",
                "IS_NOT_FALSE": "IsNotFalse",
                "IS_UNKNOWN": "IsUnknown",
                "IS_NOT_UNKNOWN": "IsNotUnknown",
            }.get(_enum_name(node.booltesttype))
            if method is None:
                raise self.unsupported(f"unsupported BooleanTest {_enum_name(node.booltesttype)}")
            return f"({value}).{method}().Expr()"
        if name == "FuncCall":
            return self.function(node)
        if name == "SubLink":
            query = self.rowset(node.subselect)
            kind = _enum_name(node.subLinkType)
            if kind == "EXISTS_SUBLINK":
                return f"qs.Exists({query}).Expr()"
            if kind == "EXPR_SUBLINK":
                return f"qs.Scalar({query})"
            if kind in {"ANY_SUBLINK", "ALL_SUBLINK"}:
                ops = list(node.operName or ())
                quant = "qs.AnyQuery" if kind == "ANY_SUBLINK" else "qs.AllQuery"
                if not ops:
                    if kind == "ANY_SUBLINK":
                        if _node_name(node.testexpr) == "RowExpr":
                            # Row IN sublinks carry their projection width in
                            # qs.RowExpr.  Calling Expr() first would erase
                            # that invariant and make every tuple scalar.
                            args = list(node.testexpr.args or ())
                            if not args:
                                raise self.unsupported("empty row IN operand")
                            constructor = (
                                "qs.Tuple"
                                if _enum_name(node.testexpr.row_format) == "COERCE_IMPLICIT_CAST"
                                else "qs.Row"
                            )
                            return f"{constructor}({_join(self.expr(arg) for arg in args)}).InQuery({query}).Expr()"
                        left = self.expr(node.testexpr)
                        return f"({left}).InQuery({query}).Expr()"
                    raise self.unsupported("ALL subquery has no comparison operator")
                left = self.expr(node.testexpr)
                if len(ops) == 1:
                    op = _str_value(ops[0])
                    return f"qs.Operator({left}, {_go_quote(op)}, {quant}({query}))"
                if len(ops) == 2:
                    schema, op = (_str_value(value) for value in ops)
                    return f"qs.QualifiedOperator({left}, {_go_quote(schema)}, {_go_quote(op)}, {quant}({query}))"
                raise self.unsupported("quantified subquery has an unusual operator")
            if kind == "ARRAY_SUBLINK":
                return f"qs.ArrayFrom({query})"
            raise self.unsupported(f"unsupported sublink {kind}")
        if name == "RowExpr":
            if not node.args:
                raise self.unsupported("empty ROW expression is not representable")
            constructor = "qs.Tuple" if _enum_name(getattr(node, "row_format", None)) == "COERCE_IMPLICIT_CAST" else "qs.Row"
            return f"{constructor}({_join(self.expr(arg) for arg in node.args)}).Expr()"
        if name == "A_ArrayExpr":
            return f"qs.Array({_join(self.expr(arg) for arg in (node.elements or ()))})"
        if name == "CaseExpr":
            return self.case(node)
        if name == "CoalesceExpr":
            return f"qs.Coalesce({_join(self.expr(arg) for arg in node.args)})"
        if name == "MinMaxExpr":
            fn = "qs.Greatest" if _enum_name(node.op) == "IS_GREATEST" else "qs.Least"
            return f"{fn}({_join(self.expr(arg) for arg in node.args)})"
        if name == "GroupingFunc":
            return f"qs.Grouping({_join(self.expr(arg) for arg in (node.args or ()))})"
        if name == "CollateClause":
            value = self.expr(node.arg)
            coll = ".".join(_str_value(v) for v in node.collname)
            return f"({value}).Collate({_go_quote(coll)})"
        if name == "SQLValueFunction":
            methods = {
                "SVFOP_CURRENT_DATE": ("CurrentDate", False),
                "SVFOP_CURRENT_TIME": ("CurrentTime", False),
                "SVFOP_CURRENT_TIME_N": ("CurrentTime", True),
                "SVFOP_CURRENT_TIMESTAMP": ("CurrentTimestamp", False),
                "SVFOP_CURRENT_TIMESTAMP_N": ("CurrentTimestamp", True),
                "SVFOP_LOCALTIME": ("LocalTime", False),
                "SVFOP_LOCALTIME_N": ("LocalTime", True),
                "SVFOP_LOCALTIMESTAMP": ("LocalTimestamp", False),
                "SVFOP_LOCALTIMESTAMP_N": ("LocalTimestamp", True),
                "SVFOP_CURRENT_USER": ("CurrentUser", False),
                "SVFOP_SESSION_USER": ("SessionUser", False),
                "SVFOP_CURRENT_CATALOG": ("CurrentCatalog", False),
                "SVFOP_CURRENT_SCHEMA": ("CurrentSchema", False),
            }
            method_info = methods.get(_enum_name(node.op))
            if method_info is None:
                raise self.unsupported(f"SQL value function {_enum_name(node.op)}")
            method, precision = method_info
            if precision:
                return f"qs.{method}({int(node.typmod)})"
            return f"qs.{method}()"
        if name == "MergeSupportFunc":
            return "qs.MergeAction()"
        if name == "A_Star":
            return "qs.Star()"
        if name == "ArrayRef":
            value = self.expr(node.refexpr)
            if node.reflowerindexpr and node.refupperindexpr:
                return f"({value}).Slice({self.expr(node.reflowerindexpr[0])}, {self.expr(node.refupperindexpr[0])})"
            if node.reflowerindexpr:
                return f"({value}).SliceFrom({self.expr(node.reflowerindexpr[0])})"
            if node.refupperindexpr:
                return f"({value}).SliceTo({self.expr(node.refupperindexpr[0])})"
            return f"({value}).SliceAll()"
        if name == "A_Indirection":
            value = self.expr(node.arg)
            # PostgreSQL keeps a parenthesised indirection as a nested
            # A_Indirection node.  Applying another subscript/field directly
            # to the child would flatten `(a[1])[1]` into `a[1][1]`, which is
            # a different raw parse tree and can resolve to a different type.
            if _node_name(node.arg) == "A_Indirection":
                value = f"({value}).Parenthesized()"
            for item in node.indirection or ():
                item_name = _node_name(item)
                if item_name == "A_Indices":
                    lower = self.expr(item.lidx) if item.lidx is not None else None
                    upper = self.expr(item.uidx) if item.uidx is not None else None
                    if item.is_slice:
                        if lower is not None and upper is not None:
                            value = f"({value}).Slice({lower}, {upper})"
                        elif lower is not None:
                            value = f"({value}).SliceFrom({lower})"
                        elif upper is not None:
                            value = f"({value}).SliceTo({upper})"
                        else:
                            value = f"({value}).SliceAll()"
                    elif upper is not None:
                        value = f"({value}).Index({upper})"
                    else:
                        raise self.unsupported("array subscript has no index")
                elif item_name == "String":
                    value = f"({value}).Field({_go_quote(str(item.sval))})"
                elif item_name == "A_Star":
                    value = f"({value}).Fields()"
                else:
                    raise self.unsupported(f"unknown indirection node {item_name}")
            return value
        if name == "GroupingSet":
            return self.group_expr(node)
        if name == "JsonFuncExpr":
            return self.json_function(node)
        if name in {
            "JsonObjectConstructor",
            "JsonArrayConstructor",
            "JsonObjectAgg",
            "JsonArrayAgg",
            "JsonArrayQueryConstructor",
            "JsonParseExpr",
            "JsonScalarExpr",
            "JsonSerializeExpr",
            "JsonIsPredicate",
        }:
            return self.json_constructor(node)
        if name == "JsonValueExpr":
            raise self.unsupported("JSON value wrapper outside a SQL/JSON constructor")
        if name == "XmlExpr":
            return self.xml_expr(node)
        if name == "XmlSerialize":
            return self.xml_serialize(node)
        if name == "FieldSelect":
            field = getattr(node, "field", None)
            if field is None:
                raise self.unsupported("composite field selection has no field name")
            return f"({self.expr(node.arg)}).Field({_go_quote(_str_value(field))})"
        if name == "RelabelType":
            return self.expr(node.arg)
        if name == "CoerceToDomain":
            return self.expr(node.arg)
        raise self.unsupported(f"unsupported expression node {name}")

    def _json_encoding(self, encoding: str, *, input_value: bool) -> str:
        methods = {
            "JS_ENC_UTF8": "EncodingUTF8",
            "JS_ENC_UTF16": "Encoding",
            "JS_ENC_UTF32": "Encoding",
        }
        method = methods.get(encoding)
        if method is None:
            if encoding in {"", "JS_ENC_DEFAULT"}:
                return ""
            raise self.unsupported(f"JSON encoding {encoding}")
        if method == "EncodingUTF8":
            return ".EncodingUTF8()"
        enum_name = {
            "JS_ENC_UTF16": "JSONEncodingUTF16",
            "JS_ENC_UTF32": "JSONEncodingUTF32",
        }[encoding]
        return f".Encoding(qs.{enum_name})"

    def _json_format(self, node: Any, *, input_value: bool = True) -> str:
        format_node = getattr(node, "format", None)
        if format_node is None:
            return ""
        format_name = _enum_name(getattr(format_node, "format_type", None))
        encoding = _enum_name(getattr(format_node, "encoding", None))
        if format_name in {"", "JS_FORMAT_DEFAULT"}:
            if encoding not in {"", "JS_ENC_DEFAULT"}:
                raise self.unsupported("JSON encoding without FORMAT JSON")
            return ""
        if format_name != "JS_FORMAT_JSON":
            raise self.unsupported(f"JSON format {format_name}")
        result = ".FormatJSON()"
        encoding_suffix = self._json_encoding(encoding, input_value=input_value)
        return result + encoding_suffix

    def _json_utf8_format(self, node: Any) -> str:
        # Path functions and JSON_TABLE expose only UTF8, unlike the JSON
        # constructors. Reject other encodings instead of changing the query.
        fmt = getattr(node, "format", None)
        if fmt is None:
            return ""
        encoding = _enum_name(getattr(fmt, "encoding", None))
        if encoding not in {"", "JS_ENC_DEFAULT", "JS_ENC_UTF8"}:
            raise self.unsupported(f"SQL/JSON path encoding {encoding}")
        return self._json_format(node)

    def json_input(self, node: Any) -> str:
        if _node_name(node) != "JsonValueExpr":
            raise self.unsupported("SQL/JSON input is not a JsonValueExpr")
        value = self.expr(node.raw_expr)
        format_suffix = self._json_format(node)
        if not format_suffix:
            return value
        return f"qs.JSONInputExpr({value}){format_suffix}"

    def json_output(self, builder: str, output: Any) -> str:
        if output is None:
            return builder
        type_name = getattr(output, "typeName", None)
        returning = getattr(output, "returning", None)
        if type_name is None or returning is None:
            raise self.unsupported("SQL/JSON output has no RETURNING type")
        builder += f".Returning({self.data_type(type_name)})"
        builder += self._json_format(returning)
        return builder

    def json_null_policy(self, builder: str, absent_on_null: bool, *, array: bool) -> str:
        # PostgreSQL records only the effective policy.  Omit the constructor
        # default and spell the non-default branch explicitly.
        if array:
            if not absent_on_null:
                builder += ".NullOnNull()"
        elif absent_on_null:
            builder += ".AbsentOnNull()"
        return builder

    def json_aggregate_tail(self, builder: str, constructor: Any) -> str:
        if getattr(constructor, "agg_filter", None) is not None:
            builder += f".Filter(qs.AsCondition({self.expr(constructor.agg_filter)}))"
        over = getattr(constructor, "over", None)
        if over is not None:
            if over.name:
                builder += f".OverNamed({_go_quote(_str_value(over.name))})"
            else:
                builder += f".Over({self.window(over)})"
        return builder

    def json_constructor(self, node: Any) -> str:
        name = _node_name(node)
        if name == "JsonObjectConstructor":
            members: list[str] = []
            for member in node.exprs or ():
                members.append(f"qs.JSONPair({self.expr(member.key)}, {self.json_input(member.value)})")
            builder = f"qs.JSONObject({_join(members)})"
            builder = self.json_null_policy(builder, bool(node.absent_on_null), array=False)
            if node.unique:
                builder += ".WithUniqueKeys()"
            builder = self.json_output(builder, node.output)
            return f"{builder}.Expr()"
        if name == "JsonArrayConstructor":
            values = _join(self.json_input(value) for value in (node.exprs or ()))
            builder = f"qs.JSONArray({values})"
            builder = self.json_null_policy(builder, bool(node.absent_on_null), array=True)
            builder = self.json_output(builder, node.output)
            return f"{builder}.Expr()"
        if name == "JsonArrayQueryConstructor":
            builder = f"qs.JSONArrayQuery({self.rowset(node.query)})"
            format_node = getattr(node, "format", None)
            format_name = _enum_name(getattr(format_node, "format_type", None))
            encoding = _enum_name(getattr(format_node, "encoding", None))
            if format_name not in {"", "JS_FORMAT_DEFAULT"}:
                if format_name != "JS_FORMAT_JSON":
                    raise self.unsupported(f"JSON_ARRAY query input format {format_name}")
                builder += ".InputFormatJSON()"
                if encoding not in {"", "JS_ENC_DEFAULT"}:
                    suffix = self._json_encoding(encoding, input_value=True)
                    suffix = suffix.replace(".EncodingUTF8()", ".InputEncoding(qs.JSONEncodingUTF8)")
                    suffix = suffix.replace(".Encoding(qs.JSONEncodingUTF16)", ".InputEncoding(qs.JSONEncodingUTF16)")
                    suffix = suffix.replace(".Encoding(qs.JSONEncodingUTF32)", ".InputEncoding(qs.JSONEncodingUTF32)")
                    builder += suffix
            elif encoding not in {"", "JS_ENC_DEFAULT"}:
                raise self.unsupported("JSON_ARRAY query encoding without FORMAT JSON")
            builder = self.json_output(builder, node.output)
            return f"{builder}.Expr()"
        if name == "JsonObjectAgg":
            constructor = node.constructor
            if constructor is None:
                raise self.unsupported("JSON_OBJECTAGG has no constructor metadata")
            if constructor.agg_order:
                raise self.unsupported("JSON_OBJECTAGG ORDER BY")
            builder = f"qs.JSONObjectAggregate({self.expr(node.arg.key)}, {self.json_input(node.arg.value)})"
            builder = self.json_null_policy(builder, bool(node.absent_on_null), array=False)
            if node.unique:
                builder += ".WithUniqueKeys()"
            builder = self.json_output(builder, constructor.output)
            builder = self.json_aggregate_tail(builder, constructor)
            return f"{builder}.Expr()"
        if name == "JsonArrayAgg":
            constructor = node.constructor
            if constructor is None:
                raise self.unsupported("JSON_ARRAYAGG has no constructor metadata")
            builder = f"qs.JSONArrayAggregate({self.json_input(node.arg)})"
            if constructor.agg_order:
                builder += f".OrderBy({_join(self.order(value) for value in constructor.agg_order)})"
            builder = self.json_null_policy(builder, bool(node.absent_on_null), array=True)
            builder = self.json_output(builder, constructor.output)
            builder = self.json_aggregate_tail(builder, constructor)
            return f"{builder}.Expr()"
        if name == "JsonParseExpr":
            if node.output is not None:
                raise self.unsupported("JSON parse RETURNING output")
            builder = f"qs.JSONParse({self.json_input(node.expr)})"
            if node.unique_keys:
                builder += ".WithUniqueKeys()"
            return f"{builder}.Expr()"
        if name == "JsonScalarExpr":
            if node.output is not None:
                raise self.unsupported("JSON_SCALAR RETURNING output")
            return f"qs.JSONScalar({self.expr(node.expr)})"
        if name == "JsonSerializeExpr":
            builder = f"qs.JSONSerialize({self.json_input(node.expr)})"
            builder = self.json_output(builder, node.output)
            return f"{builder}.Expr()"
        if name == "JsonIsPredicate":
            format_name = _enum_name(getattr(node.format, "format_type", None))
            encoding = _enum_name(getattr(node.format, "encoding", None))
            if format_name not in {"", "JS_FORMAT_DEFAULT"} or encoding not in {"", "JS_ENC_DEFAULT"}:
                raise self.unsupported("IS JSON FORMAT clause")
            builder = f"qs.IsJSON({self.expr(node.expr)})"
            item_method = {
                "JS_TYPE_ANY": "Any",
                "JS_TYPE_OBJECT": "Object",
                "JS_TYPE_ARRAY": "Array",
                "JS_TYPE_SCALAR": "Scalar",
            }.get(_enum_name(node.item_type))
            if item_method is None:
                raise self.unsupported(f"IS JSON item type {_enum_name(node.item_type)}")
            if item_method != "Any":
                builder += f".{item_method}()"
            if node.unique_keys:
                builder += ".WithUniqueKeys()"
            return f"{builder}.Expr()"
        raise self.unsupported(f"unsupported SQL/JSON node {name}")

    def xml_mode(self, node: Any) -> str:
        mode = _enum_name(getattr(node, "xmloption", None))
        if mode == "XMLOPTION_DOCUMENT":
            return "qs.XMLDocument"
        if mode == "XMLOPTION_CONTENT":
            return "qs.XMLContent"
        raise self.unsupported(f"XML mode {mode}")

    def xml_items(self, items: Sequence[Any]) -> str:
        values: list[str] = []
        for item in items:
            if item.name is None:
                values.append(f"qs.XMLValue({self.expr(item.val)})")
            else:
                values.append(f"qs.XMLAttr({_go_quote(_str_value(item.name))}, {self.expr(item.val)})")
        return _join(values)

    def xml_expr(self, node: Any) -> str:
        op = _enum_name(node.op)
        if op == "IS_XMLCONCAT":
            return f"qs.XMLConcat({_join(self.expr(value) for value in (node.args or ()))})"
        if op == "IS_XMLELEMENT":
            builder = f"qs.XMLElement({_go_quote(_str_value(node.name))})"
            if node.named_args:
                builder += f".Attributes({self.xml_items(node.named_args)})"
            if node.args:
                builder += f".Content({_join(self.expr(value) for value in node.args)})"
            return f"{builder}.Expr()"
        if op == "IS_XMLFOREST":
            if not node.named_args:
                raise self.unsupported("XMLFOREST has no values")
            return f"qs.XMLForest({self.xml_items(node.named_args)}).Expr()"
        if op == "IS_XMLPARSE":
            args = list(node.args or ())
            if len(args) not in {1, 2}:
                raise self.unsupported("XMLPARSE argument count")
            builder = f"qs.XMLParse({self.xml_mode(node)}, {self.expr(args[0])})"
            if len(args) == 2:
                value = args[1]
                if _node_name(value) != "A_Const" or _node_name(value.val) != "Boolean":
                    raise self.unsupported("XMLPARSE whitespace flag")
                if value.val.boolval:
                    builder += ".PreserveWhitespace()"
            return f"{builder}.Expr()"
        if op == "IS_XMLPI":
            args = list(node.args or ())
            if len(args) > 1:
                raise self.unsupported("XMLPI argument count")
            builder = f"qs.XMLPI({_go_quote(_str_value(node.name))}"
            if args:
                builder += f", {self.expr(args[0])}"
            return f"{builder}).Expr()"
        if op == "IS_XMLROOT":
            args = list(node.args or ())
            if len(args) != 3:
                raise self.unsupported("XMLROOT argument count")
            builder = f"qs.XMLRoot({self.expr(args[0])}"
            version = args[1]
            if _node_name(version) == "A_Const" and version.isnull:
                builder += ").VersionNoValue()"
            else:
                builder += f").Version({self.expr(version)})"
            standalone = args[2]
            if _node_name(standalone) != "A_Const" or _node_name(standalone.val) != "Integer":
                raise self.unsupported("XMLROOT standalone mode")
            # pglast follows PostgreSQL's XmlStandalone enum: 0=YES,
            # 1=NO, 2=NO VALUE, and 3 means the clause was omitted.
            modes = {0: "XMLStandaloneYes", 1: "XMLStandaloneNo", 2: "XMLStandaloneNoValue"}
            mode = modes.get(int(standalone.val.ival))
            if int(standalone.val.ival) not in {0, 1, 2, 3}:
                raise self.unsupported("XMLROOT standalone mode")
            if mode:
                builder += f".Standalone(qs.{mode})"
            return f"{builder}.Expr()"
        if op == "IS_DOCUMENT":
            args = list(node.args or ())
            if len(args) != 1:
                raise self.unsupported("IS DOCUMENT argument count")
            return f"qs.XMLIsDocument({self.expr(args[0])}).Expr()"
        raise self.unsupported(f"unsupported XML expression {op}")

    def xml_serialize(self, node: Any) -> str:
        builder = f"qs.XMLSerialize({self.xml_mode(node)}, {self.expr(node.expr)}, {self.data_type(node.typeName)})"
        if node.indent:
            builder += ".Indent()"
        return f"{builder}.Expr()"

    def a_expr(self, node: Any) -> str:
        left = self.expr(node.lexpr) if node.lexpr is not None else None
        values = node.rexpr
        op_values = list(node.name or ())
        op = _str_value(op_values[0]) if op_values else ""
        kind = _enum_name(node.kind)
        if isinstance(values, tuple):
            rights = [self.expr(value) for value in values]
        else:
            rights = [self.expr(values)] if values is not None else []
        if kind == "AEXPR_IN":
            method = "InExpr" if op == "=" else "NotInExpr"
            if op not in {"=", "<>"}:
                raise self.unsupported(f"IN operator {op!r}")
            return f"({left}).{method}({_join(rights)}).Expr()"
        if kind in {"AEXPR_BETWEEN", "AEXPR_NOT_BETWEEN", "AEXPR_BETWEEN_SYM", "AEXPR_NOT_BETWEEN_SYM"}:
            if len(rights) != 2:
                raise self.unsupported("BETWEEN requires two bounds")
            method = {
                "AEXPR_BETWEEN": "BetweenExpr",
                "AEXPR_NOT_BETWEEN": "NotBetweenExpr",
                "AEXPR_BETWEEN_SYM": "BetweenSymmetric",
                "AEXPR_NOT_BETWEEN_SYM": "NotBetweenSymmetric",
            }[kind]
            return f"({left}).{method}({rights[0]}, {rights[1]}).Expr()"
        if kind == "AEXPR_NULLIF":
            return f"qs.NullIf({left}, {rights[0]})"
        if kind == "AEXPR_DISTINCT":
            return f"({left}).IsDistinctFromExpr({rights[0]}).Expr()"
        if kind == "AEXPR_NOT_DISTINCT":
            return f"({left}).IsNotDistinctFromExpr({rights[0]}).Expr()"
        if kind in {"AEXPR_OP_ANY", "AEXPR_OP_ALL"}:
            if len(rights) != 1:
                raise self.unsupported("quantified operator has no single right operand")
            quantifier = "qs.AnyArray" if kind == "AEXPR_OP_ANY" else "qs.AllArray"
            return f"qs.Operator({left}, {_go_quote(op)}, {quantifier}({rights[0]}))"
        if kind in {"AEXPR_LIKE", "AEXPR_ILIKE", "AEXPR_SIMILAR"}:
            methods = {
                "AEXPR_LIKE": {"~~": "LikeExpr", "!~~": "NotLikeExpr"},
                "AEXPR_ILIKE": {"~~*": "ILikeExpr", "!~~*": "NotILikeExpr"},
                "AEXPR_SIMILAR": {"~": "SimilarTo", "!~": "NotSimilarTo"},
            }
            method = methods[kind].get(op)
            if method is None or len(rights) != 1:
                raise self.unsupported(f"pattern operator {op!r} requires escape/function handling")
            pattern = rights[0]
            escape: str | None = None
            helper = node.rexpr
            if _node_name(helper) == "FuncCall":
                helper_names = tuple(_str_value(value).lower() for value in (helper.funcname or ()))
                expected_helper = {
                    "AEXPR_LIKE": ("pg_catalog", "like_escape"),
                    "AEXPR_ILIKE": ("pg_catalog", "like_escape"),
                    "AEXPR_SIMILAR": ("pg_catalog", "similar_to_escape"),
                }[kind]
                if helper_names == expected_helper:
                    helper_args = list(helper.args or ())
                    if len(helper_args) not in {1, 2}:
                        raise self.unsupported("pattern escape helper has unsupported arity")
                    pattern = self.expr(helper_args[0])
                    if len(helper_args) == 2:
                        escape = self.expr(helper_args[1])
            condition = f"({left}).{method}({pattern})"
            if escape is not None:
                condition += f".EscapeExpr({escape})"
            return condition + ".Expr()"
        if left is None:
            if len(rights) != 1:
                raise self.unsupported(f"prefix operator {op!r} has {len(rights)} operands")
            if not re.fullmatch(r"[+*/%<>=~!@#^&|?-]+", op):
                raise self.unsupported(f"non-symbolic prefix operator {op!r}")
            return f"qs.PrefixOperator({_go_quote(op)}, {rights[0]})"
        if len(rights) != 1:
            raise self.unsupported(f"operator {op!r} has {len(rights)} right operands")
        if op == "AT TIME ZONE":
            return f"({left}).AtTimeZone({rights[0]})"
        if not re.fullmatch(r"[+*/%<>=~!@#^&|?-]+", op):
            raise self.unsupported(f"non-symbolic operator {op!r}")
        return f"qs.Operator({left}, {_go_quote(op)}, {rights[0]})"

    def function(self, node: Any) -> str:
        names = [_str_value(value) for value in (node.funcname or ())]
        if not names:
            raise self.unsupported("function has no name")
        if _enum_name(node.funcformat) == "COERCE_SQL_SYNTAX":
            if names[-1].lower() == "extract":
                args = list(node.args or ())
                if (
                    len(args) != 2
                    or _node_name(args[0]) != "A_Const"
                    or _node_name(getattr(args[0], "val", None)) != "String"
                ):
                    raise self.unsupported("EXTRACT needs a date-part and expression")
                parts = {
                    "year": "PartYear",
                    "month": "PartMonth",
                    "day": "PartDay",
                    "hour": "PartHour",
                    "minute": "PartMinute",
                    "second": "PartSecond",
                    "epoch": "PartEpoch",
                    "dow": "PartDOW",
                    "isodow": "PartISODOW",
                    "doy": "PartDOY",
                    "week": "PartWeek",
                    "quarter": "PartQuarter",
                    "isoyear": "PartISOYear",
                    "timezone": "PartTimezone",
                    "timezone_hour": "PartTimezoneHour",
                    "timezone_minute": "PartTimezoneMinute",
                    "microseconds": "PartMicroseconds",
                    "milliseconds": "PartMilliseconds",
                    "decade": "PartDecade",
                    "century": "PartCentury",
                    "millennium": "PartMillennium",
                }
                part = parts.get(str(args[0].val.sval).lower())
                if part is None:
                    raise self.unsupported(f"EXTRACT date-part {args[0].val.sval}")
                return f"qs.Extract(qs.{part}, {self.expr(args[1])})"
            if names[-1].lower() == "timezone" and len(node.args or ()) == 2:
                return f"({self.expr(node.args[1])}).AtTimeZone({self.expr(node.args[0])})"
            if names[-1].lower() == "timezone" and len(node.args or ()) == 1:
                return f"({self.expr(node.args[0])}).AtLocal()"
            if names[-1].lower() == "substring":
                args = list(node.args or ())
                if len(args) not in {2, 3}:
                    raise self.unsupported("SUBSTRING argument count")
                length = f", {self.expr(args[2])}" if len(args) == 3 else ""
                # PostgreSQL stores FROM, FOR, and SIMILAR forms in the same
                # FuncCall shape. SubstringFrom reparses to that canonical AST
                # for all of them, including the FOR-only start=1 cast.
                return f"qs.SubstringFrom({self.expr(args[0])}, {self.expr(args[1])}{length})"
            if names[-1].lower() == "position":
                args = list(node.args or ())
                if len(args) != 2:
                    raise self.unsupported("POSITION argument count")
                # The parser stores POSITION(value, substring), while the
                # public constructor follows SQL's substring IN value order.
                return f"qs.Position({self.expr(args[1])}, {self.expr(args[0])})"
            if names[-1].lower() == "normalize":
                args = list(node.args or ())
                if len(args) not in {1, 2}:
                    raise self.unsupported("NORMALIZE argument count")
                if len(args) == 1:
                    return f"qs.Normalize({self.expr(args[0])})"
                form = args[1]
                if _node_name(form) != "A_Const" or _node_name(form.val) != "String":
                    raise self.unsupported("NORMALIZE form")
                forms = {"NFC": "NFC", "NFD": "NFD", "NFKC": "NFKC", "NFKD": "NFKD"}
                value = forms.get(str(form.val.sval).upper())
                if value is None:
                    raise self.unsupported("NORMALIZE form")
                return f"qs.Normalize({self.expr(args[0])}, qs.{value})"
            if names[-1].lower() == "overlay":
                args = list(node.args or ())
                if len(args) not in {3, 4}:
                    raise self.unsupported("OVERLAY argument count")
                length = f", {self.expr(args[3])}" if len(args) == 4 else ""
                return f"qs.Overlay({self.expr(args[0])}, {self.expr(args[1])}, {self.expr(args[2])}{length})"
            if names[-1].lower() == "overlaps":
                args = list(node.args or ())
                if len(args) != 4:
                    raise self.unsupported("OVERLAPS argument count")
                return f"qs.Overlaps({_join(self.expr(value) for value in args)}).Expr()"
            if names[-1].lower() in {"btrim", "ltrim", "rtrim"}:
                args = list(node.args or ())
                if len(args) not in {1, 2}:
                    raise self.unsupported("TRIM argument count")
                direction = {
                    "btrim": "TrimBothDirection",
                    "ltrim": "TrimLeadingDirection",
                    "rtrim": "TrimTrailingDirection",
                }[names[-1].lower()]
                characters = f", {self.expr(args[1])}" if len(args) == 2 else ""
                return f"qs.TrimSyntax({self.expr(args[0])}, qs.{direction}{characters})"
            if names[-1].lower() == "pg_collation_for":
                args = list(node.args or ())
                if len(args) != 1:
                    raise self.unsupported("COLLATION FOR argument count")
                return f"qs.CollationFor({self.expr(args[0])})"
            if names[-1].lower() == "is_normalized":
                args = list(node.args or ())
                if len(args) not in {1, 2}:
                    raise self.unsupported("IS NORMALIZED argument count")
                if len(args) == 1:
                    return f"qs.IsNormalized({self.expr(args[0])}).Expr()"
                form = args[1]
                if _node_name(form) != "A_Const" or _node_name(form.val) != "String":
                    raise self.unsupported("IS NORMALIZED form")
                forms = {"NFC": "NFC", "NFD": "NFD", "NFKC": "NFKC", "NFKD": "NFKD"}
                value = forms.get(str(form.val.sval).upper())
                if value is None:
                    raise self.unsupported("IS NORMALIZED form")
                return f"qs.IsNormalized({self.expr(args[0])}, qs.{value}).Expr()"
            if names[-1].lower() == "xmlexists":
                args = list(node.args or ())
                if len(args) != 2:
                    raise self.unsupported("XMLEXISTS argument count")
                return f"qs.XMLExists({self.expr(args[0])}, {self.expr(args[1])}).Expr()"
            raise self.unsupported(f"SQL-syntax function {'.'.join(names)}")
        special = names[-1].lower()
        args: list[str] = []
        for arg in node.args or ():
            if _node_name(arg) == "NamedArgExpr":
                args.append(f"qs.NamedArg({_go_quote(str(arg.name))}, {self.expr(arg.arg)})")
            else:
                args.append(self.expr(arg))
        if node.func_variadic:
            if not args:
                raise self.unsupported("VARIADIC function requires one argument")
            args[-1] = f"qs.Variadic({args[-1]})"
        if node.agg_star:
            args = ["qs.Star()"]
        if special in {"coalesce", "nullif", "greatest", "least", "concat"}:
            fn = {
                "coalesce": "Coalesce",
                "nullif": "NullIf",
                "greatest": "Greatest",
                "least": "Least",
                "concat": "Concat",
            }[special]
            if special == "nullif" and len(args) != 2:
                raise self.unsupported("NULLIF arity")
            expression = f"qs.{fn}({_join(args)})"
        elif special == "substring":
            # A regular function call named substring is distinct from the
            # SQL SUBSTRING grammar node handled above.  Preserve it as a
            # symbolic call so its ordinary FuncCall AST reparses unchanged.
            expression = f"qs.Call({_go_quote('.'.join(names))}{', ' if args else ''}{_join(args)})"
        else:
            expression = f"qs.Call({_go_quote('.'.join(names))}{', ' if args else ''}{_join(args)})"
        if node.agg_distinct:
            expression = f"({expression}).Distinct()"
        if node.agg_order and not node.agg_within_group:
            expression = f"({expression}).OrderBy({_join(self.order(value) for value in node.agg_order)})"
        if node.agg_within_group:
            expression = f"({expression}).WithinGroup({_join(self.order(value) for value in node.agg_order)})"
        if node.agg_filter is not None:
            expression = f"({expression}).Filter(qs.AsCondition({self.expr(node.agg_filter)}))"
        if node.over is not None:
            # PostgreSQL distinguishes OVER name from OVER (name): the
            # former references a named window, while the latter is a window
            # specification whose refname is inside parentheses.
            if node.over.name:
                expression = f"({expression}).OverNamed({_go_quote(_str_value(node.over.name))})"
            else:
                expression = f"({expression}).Over({self.window(node.over)})"
        return expression

    def case(self, node: Any) -> str:
        builder = f"qs.CaseOf({self.expr(node.arg)})" if node.arg is not None else "qs.Case()"
        for branch in node.args or ():
            if node.arg is not None:
                builder += f".WhenValue({self.expr(branch.expr)}, {self.expr(branch.result)})"
            else:
                builder += f".When(qs.AsCondition({self.expr(branch.expr)}), {self.expr(branch.result)})"
        if node.defresult is not None:
            builder += f".Else({self.expr(node.defresult)})"
        return f"({builder}).End()"

    def data_type(self, node: Any) -> str:
        names = [_str_value(value) for value in (node.names or ())]
        if not names:
            raise self.unsupported("empty type name")
        if node.setof or node.pct_type:
            raise self.unsupported("setof/%TYPE cast")
        lower = names[-1].lower()
        aliases = {
            "bool": "Bool",
            "boolean": "Bool",
            "int2": "Int2",
            "smallint": "Int2",
            "int4": "Int4",
            "integer": "Int4",
            "int": "Int4",
            "int8": "Int8",
            "bigint": "Int8",
            "float4": "Float4",
            "real": "Float4",
            "float8": "Float8",
            "double": "Float8",
            "text": "Text",
            "bytea": "Bytea",
            "uuid": "UUID",
            "json": "JSON",
            "jsonb": "JSONB",
            "jsonpath": "JSONPath",
            "date": "Date",
            "time": "Time",
            "timetz": "TimeTZ",
            "timestamp": "Timestamp",
            "timestamptz": "TimestampTZ",
            "interval": "Interval",
            "numeric": "Numeric",
            "decimal": "Numeric",
            "tsvector": "TSVector",
            "tsquery": "TSQuery",
            "inet": "Inet",
            "cidr": "CIDR",
            "xml": "XML",
        }
        qualified = len(names) > 1
        builtin = qualified and names[0].lower() == "pg_catalog" and (
            lower in aliases or lower in {"varchar", "character varying"}
        )
        if builtin and lower in {"varchar", "character varying"}:
            typmods = list(node.typmods or ())
            if not typmods:
                # An unconstrained pg_catalog.varchar is distinct from a
                # validated Varchar(length). Preserve the parsed type name
                # instead of inventing a length or emitting an invalid zero.
                result = f"qs.NamedType({_join(_go_quote(value) for value in names)})"
            elif len(typmods) != 1 or _node_name(typmods[0]) != "A_Const" or _node_name(typmods[0].val) != "Integer":
                raise self.unsupported("varchar cast without an integer typmod")
            else:
                result = f"qs.Varchar({int(typmods[0].val.ival)})"
        elif builtin and lower in {"numeric", "decimal"} and node.typmods:
            mods = list(node.typmods)
            if len(mods) != 2 or any(_node_name(mod) != "A_Const" or _node_name(mod.val) != "Integer" for mod in mods):
                raise self.unsupported("numeric cast with non-integer typmod")
            result = f"qs.Decimal({int(mods[0].val.ival)}, {int(mods[1].val.ival)})"
        elif builtin and not node.typmods:
            result = f"qs.{aliases[lower]}"
        elif len(names) == 1 and not node.typmods:
            # A one-part type may be a quoted identifier or a PostgreSQL
            # alias which the parser leaves unqualified.  Preserve its exact
            # spelling rather than resolving it through search_path.
            result = f"qs.NamedType({_go_quote(names[0])})"
        elif len(names) <= 3:
            result = f"qs.NamedType({_join(_go_quote(value) for value in names)})"
        else:
            raise self.unsupported("type name has too many qualification parts")
        if node.typmods and result.startswith("qs.NamedType("):
            modifiers: list[str] = []
            for modifier in node.typmods:
                if _node_name(modifier) != "A_Const" or _node_name(modifier.val) != "Integer":
                    raise self.unsupported("type typmod is not an integer constant")
                modifiers.append(str(int(modifier.val.ival)))
            result += f".Modifiers({_join(modifiers)})"
        for _ in node.arrayBounds or ():
            result = f"qs.ArrayType({result})"
        return result

    def order(self, node: Any) -> str:
        value = self.expr(node.node)
        direction = _enum_name(node.sortby_dir)
        if direction == "SORTBY_DESC":
            result = f"({value}).Desc()"
        elif direction == "SORTBY_USING":
            ops = [_str_value(op) for op in (node.useOp or ())]
            if len(ops) != 1:
                raise self.unsupported("ORDER BY USING has no single operator")
            result = f"({value}).Using({_go_quote(ops[0])})"
        else:
            if direction == "SORTBY_DEFAULT":
                self.normalizations.add("default_ascending")
            result = f"({value}).Asc()"
        nulls = _enum_name(node.sortby_nulls)
        if nulls == "SORTBY_NULLS_FIRST":
            result += ".NullsFirst()"
        elif nulls == "SORTBY_NULLS_LAST":
            result += ".NullsLast()"
        return result

    def group_expr(self, node: Any) -> str:
        if _node_name(node) != "GroupingSet":
            return self.expr(node)
        kind = _enum_name(node.kind)
        values = [self.expr(value) for value in (node.content or ())]
        if kind == "GROUPING_SET_EMPTY":
            return "qs.GroupingSet()"
        if kind == "GROUPING_SET_SIMPLE":
            return f"qs.GroupingSet({_join(values)})"
        if kind == "GROUPING_SET_ROLLUP":
            return f"qs.Rollup({_join(values)})"
        if kind == "GROUPING_SET_CUBE":
            return f"qs.Cube({_join(values)})"
        if kind == "GROUPING_SET_SETS":
            return f"qs.GroupingSets({_join(self.group_expr(value) for value in (node.content or ()))})"
        raise self.unsupported(f"unknown grouping set kind {kind}")

    def relation(self, node: Any) -> str:
        name = _node_name(node)
        if name == "RangeVar":
            parts = [value for value in (node.catalogname, node.schemaname, node.relname) if value]
            relation = f"qs.Table({_go_quote('.'.join(parts))})"
            if not node.inh:
                relation += ".Only()"
            if node.alias is not None:
                relation += f".As({_go_quote(node.alias.aliasname)}"
                cols = list(node.alias.colnames or ())
                if cols:
                    relation += ", " + _join(_go_quote(_str_value(c)) for c in cols)
                relation += ")"
            return relation
        if name == "RangeSubselect":
            if getattr(node.subquery, "intoClause", None) is not None:
                raise self.unsupported("SELECT INTO cannot be used as a subquery")
            if node.alias is None:
                relation = f"qs.Derived({self.rowset(node.subquery)})"
            else:
                relation = f"qs.Subquery({self.rowset(node.subquery)}, {_go_quote(node.alias.aliasname)}"
                cols = list(node.alias.colnames or ())
                if cols:
                    relation += ", " + _join(_go_quote(_str_value(c)) for c in cols)
                relation += ")"
            if node.lateral:
                relation = f"qs.Lateral({relation})"
            return relation
        if name == "RangeFunction":
            functions = list(node.functions or ())
            if not functions:
                raise self.unsupported("empty table function list")

            def definitions(columns: Sequence[Any]) -> str:
                values: list[str] = []
                for column in columns:
                    if column.colname is None or column.typeName is None:
                        raise self.unsupported("table function column definition is incomplete")
                    values.append(f"qs.Def({_go_quote(column.colname)}, {self.data_type(column.typeName)})")
                return _join(values)

            records: list[str] = []
            expressions: list[str] = []
            for function, columns in functions:
                function_expr = self.expr(function)
                expressions.append(function_expr)
                record = f"qs.Function({function_expr})"
                if columns:
                    record += f".DefineColumns({definitions(columns)})"
                records.append(record)
            # A RangeFunction-level coldeflist belongs to the relation, not to
            # the function's ROWS FROM item.  PostgreSQL's regression corpus
            # uses this with one function; qs's typed TableFunc relation is the
            # corresponding structural form and emits ``AS alias (defs)``.
            coldeflist = list(node.coldeflist or ())
            if coldeflist:
                if len(records) != 1 or node.is_rowsfrom or functions[0][1]:
                    raise self.unsupported("table function relation column definitions have unsupported shape")
                relation = f"qs.TableFunc({expressions[0]}).DefineColumns({definitions(coldeflist)})"
            elif len(records) == 1 and not node.is_rowsfrom:
                relation = f"qs.TableFunc({expressions[0]})"
                if functions[0][1]:
                    relation += f".DefineColumns({definitions(functions[0][1])})"
            else:
                if not node.is_rowsfrom:
                    raise self.unsupported("multiple table functions without ROWS FROM")
                relation = f"qs.RowsFrom({_join(records)})"
            if node.ordinality:
                relation += ".WithOrdinality()"
            if node.alias is not None:
                relation += f".As({_go_quote(node.alias.aliasname)}"
                # A relation coldeflist already carries typed names.  Do not
                # add a second alias column list; qs rejects that combination
                # to keep its relation invariants explicit.
                cols = [] if coldeflist else list(node.alias.colnames or ())
                if cols:
                    relation += ", " + _join(_go_quote(_str_value(c)) for c in cols)
                relation += ")"
            if node.lateral:
                relation = f"qs.Lateral({relation})"
            return relation
        if name == "RangeTableSample":
            relation = self.relation(node.relation)
            methods = [_str_value(value) for value in (node.method or ())]
            if len(methods) != 1:
                raise self.unsupported("TABLESAMPLE has no single method name")
            arguments = [self.expr(value) for value in (node.args or ())]
            relation += f".TableSample({_go_quote(methods[0])}"
            if arguments:
                relation += ", " + _join(arguments)
            relation += ")"
            if node.repeatable is not None:
                relation += f".Repeatable({self.expr(node.repeatable)})"
            return relation
        if name == "RangeTableFunc":
            columns: list[str] = []
            for column in node.columns or ():
                if column.colname is None:
                    raise self.unsupported("XMLTABLE column has no name")
                if column.for_ordinality:
                    if column.typeName is not None or column.colexpr is not None or column.coldefexpr is not None:
                        raise self.unsupported("XMLTABLE ordinality column options")
                    columns.append(f"qs.XMLOrdinality({_go_quote(column.colname)})")
                    continue
                if column.typeName is None:
                    raise self.unsupported("XMLTABLE column has no type")
                value = f"qs.XMLColumn({_go_quote(column.colname)}, {self.data_type(column.typeName)})"
                if column.colexpr is not None:
                    value += f".Path({self.expr(column.colexpr)})"
                if column.coldefexpr is not None:
                    value += f".Default({self.expr(column.coldefexpr)})"
                if column.is_not_null:
                    value += ".NotNull()"
                columns.append(value)
            if not columns:
                raise self.unsupported("XMLTABLE has no columns")
            relation = f"qs.XMLTable({self.expr(node.rowexpr)}, {self.expr(node.docexpr)}, {_join(columns)})"
            if node.namespaces:
                namespaces: list[str] = []
                for namespace in node.namespaces:
                    if namespace.name is None:
                        namespaces.append(f"qs.XMLDefaultNamespace({self.expr(namespace.val)})")
                    else:
                        namespaces.append(
                            f"qs.XMLNamespace({self.expr(namespace.val)}, {_go_quote(_str_value(namespace.name))})"
                        )
                relation += f".Namespaces({_join(namespaces)})"
            if node.alias is not None:
                alias_columns = list(node.alias.colnames or ())
                relation += f".As({_go_quote(node.alias.aliasname)}"
                if alias_columns:
                    relation += ", " + _join(_go_quote(_str_value(value)) for value in alias_columns)
                relation += ")"
            else:
                relation += ".Ref()"
            if node.lateral:
                relation = f"qs.Lateral({relation})"
            return relation
        if name == "JsonTable":
            relation = self.json_table(node)
            if node.alias is not None:
                columns = list(node.alias.colnames or ())
                if columns:
                    # JSONTableBuilder.As accepts only the table alias;
                    # relation aliases carry an optional column list.
                    relation += ".Ref().As(" + _go_quote(node.alias.aliasname)
                    relation += ", " + _join(_go_quote(_str_value(value)) for value in columns) + ")"
                else:
                    relation += f".As({_go_quote(node.alias.aliasname)})"
            else:
                relation += ".Ref()"
            if node.lateral:
                relation = f"qs.Lateral({relation})"
            return relation
        if name == "JoinExpr":
            kind = _enum_name(node.jointype)
            if kind == "JOIN_INNER" and not node.isNatural and not node.usingClause and node.quals is None:
                relation = f"qs.CrossJoin({self.relation(node.larg)}, {self.relation(node.rarg)})"
                if node.alias is not None:
                    columns = list(node.alias.colnames or ())
                    relation += f".As({_go_quote(node.alias.aliasname)}"
                    if columns:
                        relation += ", " + _join(_go_quote(_str_value(value)) for value in columns)
                    relation += ")"
                return relation
            ctor = {
                "JOIN_INNER": "InnerJoin",
                "JOIN_LEFT": "LeftJoin",
                "JOIN_RIGHT": "RightJoin",
                "JOIN_FULL": "FullJoin",
                "JOIN_SEMI": "InnerJoin",
                "JOIN_ANTI": "InnerJoin",
            }.get(kind)
            if ctor is None or kind in {"JOIN_SEMI", "JOIN_ANTI"}:
                raise self.unsupported(f"join type {kind}")
            relation = f"qs.{ctor}({self.relation(node.larg)}, {self.relation(node.rarg)})"
            if node.isNatural:
                natural = {
                    "JOIN_INNER": "NaturalJoin",
                    "JOIN_LEFT": "NaturalLeftJoin",
                    "JOIN_RIGHT": "NaturalRightJoin",
                    "JOIN_FULL": "NaturalFullJoin",
                }[kind]
                relation = f"qs.{natural}({self.relation(node.larg)}, {self.relation(node.rarg)})"
            if node.usingClause:
                columns = _join(_go_quote(_str_value(value)) for value in node.usingClause)
                if node.join_using_alias is not None:
                    relation += f".UsingAs({_go_quote(node.join_using_alias.aliasname)}, {columns})"
                else:
                    relation += f".Using({columns})"
            elif node.quals is not None:
                relation += f".On(qs.AsCondition({self.expr(node.quals)}))"
            if node.alias is not None:
                columns = list(node.alias.colnames or ())
                relation += f".As({_go_quote(node.alias.aliasname)}"
                if columns:
                    relation += ", " + _join(_go_quote(_str_value(value)) for value in columns)
                relation += ")"
            return relation
        raise self.unsupported(f"unsupported relation node {name}")

    def json_value(self, node: Any) -> str:
        if _node_name(node) != "JsonValueExpr":
            return self.expr(node)
        fmt = getattr(node, "format", None)
        if fmt is not None and _enum_name(getattr(fmt, "format_type", None)) not in {"JS_FORMAT_DEFAULT", ""}:
            raise self.unsupported("JSON_TABLE context FORMAT JSON is not representable")
        return self.expr(node.raw_expr)

    def json_behavior(self, node: Any) -> str:
        if node is None:
            raise self.unsupported("missing JSON behavior")
        kind = _enum_name(node.btype)
        constructors = {
            "JSON_BEHAVIOR_ERROR": "JSONError()",
            "JSON_BEHAVIOR_NULL": "JSONNull()",
            "JSON_BEHAVIOR_EMPTY_ARRAY": "JSONEmptyArray()",
            "JSON_BEHAVIOR_EMPTY_OBJECT": "JSONEmptyObject()",
            "JSON_BEHAVIOR_TRUE": "JSONTrue()",
            "JSON_BEHAVIOR_FALSE": "JSONFalse()",
            "JSON_BEHAVIOR_UNKNOWN": "JSONUnknown()",
        }
        if kind == "JSON_BEHAVIOR_DEFAULT":
            if node.expr is None:
                raise self.unsupported("JSON DEFAULT behavior has no expression")
            return f"qs.JSONDefault({self.expr(node.expr)})"
        constructor = constructors.get(kind)
        if constructor is None:
            raise self.unsupported(f"JSON behavior {kind}")
        return f"qs.{constructor}"

    def json_function(self, node: Any) -> str:
        if node.column_name is not None:
            raise self.unsupported("SQL/JSON column context is not representable")
        operation = _enum_name(node.op)
        constructors = {
            "JSON_VALUE_OP": "JSONValue",
            "JSON_QUERY_OP": "JSONQuery",
            "JSON_EXISTS_OP": "JSONExists",
        }
        constructor = constructors.get(operation)
        if constructor is None:
            raise self.unsupported(f"SQL/JSON operation {operation}")
        builder = f"qs.{constructor}({self.json_value(node.context_item)}, {self.expr(node.pathspec)})"
        for argument in node.passing or ():
            builder += f".Passing({_go_quote(argument.name)}, {self.json_value(argument.val)})"
        if node.output is not None:
            if node.output.typeName is None:
                raise self.unsupported("SQL/JSON RETURNING has no type")
            builder += f".Returning({self.data_type(node.output.typeName)})"
            returning = getattr(node.output, "returning", None)
            builder += self._json_utf8_format(returning)
        wrappers = {
            "JSW_NONE": "WithoutWrapper",
            "JSW_UNCONDITIONAL": "WithWrapper",
            "JSW_CONDITIONAL": "WithConditionalWrapper",
        }
        wrapper = wrappers.get(_enum_name(node.wrapper)) if operation == "JSON_QUERY_OP" else None
        if wrapper:
            builder += f".{wrapper}()"
        quotes = {
            "JS_QUOTES_KEEP": "KeepQuotes",
            "JS_QUOTES_OMIT": "OmitQuotes",
        }.get(_enum_name(node.quotes)) if operation == "JSON_QUERY_OP" else None
        if quotes:
            builder += f".{quotes}()"
        if node.on_empty is not None:
            builder += f".OnEmpty({self.json_behavior(node.on_empty)})"
        if node.on_error is not None:
            builder += f".OnError({self.json_behavior(node.on_error)})"
        return f"({builder}).Expr()"

    def json_path(self, node: Any) -> tuple[str, str | None]:
        if node is None or _node_name(getattr(node, "string", None)) != "A_Const":
            raise self.unsupported("dynamic JSON_TABLE path")
        value = getattr(node.string, "val", None)
        if _node_name(value) != "String":
            raise self.unsupported("JSON_TABLE path is not a string literal")
        return str(value.sval), getattr(node, "name", None)

    def json_column(self, node: Any) -> str:
        kind = _enum_name(node.coltype)
        if kind == "JTC_NESTED":
            path, path_name = self.json_path(node.pathspec)
            columns = _join(self.json_column(value) for value in (node.columns or ()))
            if not columns:
                raise self.unsupported("JSON_TABLE nested column group is empty")
            nested = f"qs.JSONNested({_go_quote(path)}, {columns})"
            if path_name:
                nested += f".PathName({_go_quote(path_name)})"
            column = nested
        else:
            if node.name is None:
                raise self.unsupported("JSON_TABLE column has no name")
            typ = self.data_type(node.typeName) if kind != "JTC_FOR_ORDINALITY" else ""
            constructor = {
                "JTC_FOR_ORDINALITY": f"qs.JSONOrdinality({_go_quote(node.name)})",
                "JTC_EXISTS": f"qs.JSONExistsColumn({_go_quote(node.name)}, {typ})",
                "JTC_FORMATTED": f"qs.JSONColumn({_go_quote(node.name)}, {typ})",
                "JTC_REGULAR": f"qs.JSONColumn({_go_quote(node.name)}, {typ})",
            }.get(kind)
            if constructor is None:
                raise self.unsupported(f"JSON_TABLE column kind {kind}")
            column = constructor
            if node.pathspec is not None:
                path, path_name = self.json_path(node.pathspec)
                column += f".Path({_go_quote(path)})"
                if path_name:
                    column += f".PathName({_go_quote(path_name)})"
            column += self._json_utf8_format(node)
            if kind == "JTC_EXISTS":
                # EXISTS columns do not accept value wrappers or quote
                # policies. PostgreSQL still stores JSW_NONE in the raw AST;
                # it means the default here, not WITHOUT WRAPPER.
                if _enum_name(node.wrapper) not in {"JSW_NONE", "JSW_UNSPEC"}:
                    raise self.unsupported("JSON_TABLE EXISTS wrapper")
                if _enum_name(node.quotes) not in {"JS_QUOTES_UNSPEC"}:
                    raise self.unsupported("JSON_TABLE EXISTS quotes")
            else:
                wrappers = {
                    "JSW_NONE": "WithoutWrapper",
                    "JSW_UNCONDITIONAL": "WithWrapper",
                    "JSW_CONDITIONAL": "WithConditionalWrapper",
                }
                # PostgreSQL permits wrapper/quote clauses on regular
                # JSON_TABLE columns as well as FORMAT JSON columns. The AST
                # keeps those flags independently of coltype.
                wrapper = wrappers.get(_enum_name(node.wrapper))
                if wrapper:
                    column += f".{wrapper}()"
                quotes = {
                    "JS_QUOTES_KEEP": "KeepQuotes",
                    "JS_QUOTES_OMIT": "OmitQuotes",
                }.get(_enum_name(node.quotes))
                if quotes:
                    column += f".{quotes}()"
        if node.on_empty is not None:
            column += f".OnEmpty({self.json_behavior(node.on_empty)})"
        if node.on_error is not None:
            column += f".OnError({self.json_behavior(node.on_error)})"
        return column

    def json_table(self, node: Any) -> str:
        path, path_name = self.json_path(node.pathspec)
        builder = f"qs.JSONTable({self.json_value(node.context_item)}, {_go_quote(path)}"
        if node.passing:
            # Passing belongs on the immutable table descriptor after its
            # columns; keep the argument order explicit in generated code.
            pass
        builder += ")"
        if path_name:
            builder += f".PathName({_go_quote(path_name)})"
        if node.passing:
            for argument in node.passing:
                builder += f".Passing({_go_quote(argument.name)}, {self.json_value(argument.val)})"
        columns = [self.json_column(value) for value in (node.columns or ())]
        if columns:
            builder += f".Columns({_join(columns)})"
        else:
            builder += ".Columns()"
        if node.on_error is not None:
            builder += f".OnError({self.json_behavior(node.on_error)})"
        return builder

    def rowset(self, node: Any) -> str:
        if node is None:
            raise self.unsupported("missing rowset")
        name = _node_name(node)
        if name == "SelectStmt":
            return self.select(node)
        if name == "InsertStmt":
            return self.insert(node)
        if name == "UpdateStmt":
            return self.update(node)
        if name == "DeleteStmt":
            return self.delete(node)
        if name == "MergeStmt":
            return self.merge(node)
        if name == "ExecuteStmt":
            return self.execute(node)
        if name == "CreateTableAsStmt":
            return self.create_table_as(node)
        if name == "DeclareCursorStmt":
            return self.declare_cursor(node)
        raise self.unsupported(f"unsupported rowset node {name}")

    def execute(self, node: Any) -> str:
        if not node.name:
            raise self.unsupported("EXECUTE has no prepared statement name")
        arguments = _join(self.expr(value) for value in (node.params or ()))
        if arguments:
            return f"qs.Execute({_go_quote(str(node.name))}, {arguments})"
        return f"qs.Execute({_go_quote(str(node.name))})"

    def destination_name(self, relation: Any, clause: str) -> str:
        if _node_name(relation) != "RangeVar" or not relation.relname:
            raise self.unsupported(f"{clause} destination is incomplete")
        parts = [value for value in (relation.catalogname, relation.schemaname, relation.relname) if value]
        if relation.alias is not None:
            raise self.unsupported(f"{clause} destination alias")
        return ".".join(str(value) for value in parts)

    def utility_destination(self, builder: str, into: Any, clause: str, *, materialized: bool) -> str:
        relation = getattr(into, "rel", None)
        name = self.destination_name(relation, clause)
        columns = list(getattr(into, "colNames", None) or ())
        options = getattr(into, "options", None)
        if options:
            raise self.unsupported(f"{clause} storage options")
        persistence = _str_value(getattr(relation, "relpersistence", "p"))
        if materialized:
            if persistence != "p":
                raise self.unsupported(f"{clause} persistence")
        elif persistence == "t":
            builder += ".Temporary()"
        elif persistence == "u":
            builder += ".Unlogged()"
        elif persistence != "p":
            raise self.unsupported(f"{clause} persistence")
        if getattr(into, "if_not_exists", False):
            # IntoClause does not carry this flag; CTAS stores it on its
            # statement node and applies it in create_table_as below.
            raise self.unsupported(f"{clause} IF NOT EXISTS metadata")
        if columns:
            builder += f".Columns({_join(_go_quote(_str_value(value)) for value in columns)})"
        access = getattr(into, "accessMethod", None)
        if access:
            builder += f".Using({_go_quote(str(access))})"
        tablespace = getattr(into, "tableSpaceName", None)
        if tablespace:
            builder += f".Tablespace({_go_quote(str(tablespace))})"
        commit = _enum_name(getattr(into, "onCommit", None))
        if commit != "ONCOMMIT_NOOP":
            if materialized:
                raise self.unsupported(f"{clause} ON COMMIT")
            action = {
                "ONCOMMIT_PRESERVE_ROWS": "OnCommitPreserveRows",
                "ONCOMMIT_DELETE_ROWS": "OnCommitDeleteRows",
                "ONCOMMIT_DROP": "OnCommitDrop",
            }.get(commit)
            if action is None:
                raise self.unsupported(f"{clause} ON COMMIT {commit}")
            builder += f".OnCommit(qs.{action})"
        if getattr(into, "skipData", False):
            builder += ".WithNoData()"
        return builder

    def create_table_as(self, node: Any) -> str:
        into = getattr(node, "into", None)
        if into is None:
            raise self.unsupported("CREATE TABLE AS has no destination")
        materialized = _enum_name(getattr(node, "objtype", None)) == "OBJECT_MATVIEW"
        if _enum_name(getattr(node, "objtype", None)) not in {"OBJECT_TABLE", "OBJECT_MATVIEW"}:
            raise self.unsupported(f"CREATE TABLE AS object type {_enum_name(node.objtype)}")
        name = self.destination_name(getattr(into, "rel", None), "CREATE MATERIALIZED VIEW" if materialized else "CREATE TABLE AS")
        query = getattr(node, "query", None)
        if _node_name(query) == "ExecuteStmt":
            if materialized:
                raise self.unsupported("materialized view EXECUTE source")
            builder = f"qs.CreateTableAsExecute({_go_quote(name)}, {self.execute(query)})"
        else:
            if getattr(query, "intoClause", None) is not None:
                raise self.unsupported("SELECT INTO cannot be a CREATE TABLE AS source")
            source = self.rowset(query)
            constructor = "MaterializedViewAs" if materialized else "CreateTableAs"
            builder = f"qs.{constructor}({_go_quote(name)}, {source})"
        if getattr(node, "if_not_exists", False):
            builder += ".IfNotExists()"
        return self.utility_destination(builder, into, "CREATE MATERIALIZED VIEW" if materialized else "CREATE TABLE AS", materialized=materialized)

    def declare_cursor(self, node: Any) -> str:
        options = int(getattr(node, "options", 0) or 0)
        # CURSOR_OPT_FAST_PLAN is parser bookkeeping present on every cursor;
        # ASENSITIVE and planner-selection bits have no public qs spelling.
        known = 0x0001 | 0x0002 | 0x0004 | 0x0008 | 0x0020 | 0x0100
        if options & ~known:
            raise self.unsupported(f"DECLARE CURSOR options {options:#x}")
        if options & 0x0010:
            raise self.unsupported("DECLARE CURSOR ASENSITIVE")
        if getattr(node.query, "intoClause", None) is not None:
            raise self.unsupported("SELECT INTO cannot be a cursor source")
        builder = f"qs.DeclareCursor({_go_quote(str(node.portalname))}, {self.rowset(node.query)})"
        if options & 0x0001:
            builder += ".Binary()"
        if options & 0x0008:
            builder += ".Insensitive()"
        if options & 0x0002:
            builder += ".Scroll(qs.CursorScroll)"
        elif options & 0x0004:
            builder += ".Scroll(qs.CursorNoScroll)"
        if options & 0x0020:
            builder += ".WithHold()"
        return builder

    def _with(self, builder: str, clause: Any) -> str:
        if clause is None:
            return builder
        ctes: list[str] = []
        for cte in clause.ctes or ():
            body = self.rowset(cte.ctequery)
            value = f"qs.CTE({_go_quote(cte.ctename)}, {body})"
            if cte.aliascolnames:
                value += f".Columns({_join(_go_quote(_str_value(c)) for c in cte.aliascolnames)})"
            materialized = _enum_name(cte.ctematerialized)
            if materialized == "CTEMaterializeAlways":
                value += ".Materialized()"
            elif materialized == "CTEMaterializeNever":
                value += ".NotMaterialized()"
            if cte.search_clause is not None:
                search = cte.search_clause
                constructor = "SearchBreadthFirst" if search.search_breadth_first else "SearchDepthFirst"
                spec = f"qs.{constructor}({_join(_go_quote(_str_value(c)) for c in search.search_col_list)})"
                value += f".Search({spec}.Set({_go_quote(search.search_seq_column)}))"
            if cte.cycle_clause is not None:
                cycle = cte.cycle_clause
                spec = f"qs.Cycle({_join(_go_quote(_str_value(c)) for c in cycle.cycle_col_list)})"
                spec += f".Set({_go_quote(cycle.cycle_mark_column)}).Using({_go_quote(cycle.cycle_path_column)})"
                if cycle.cycle_mark_value is not None or cycle.cycle_mark_default is not None:
                    spec += f".Values({self.expr(cycle.cycle_mark_value)}, {self.expr(cycle.cycle_mark_default)})"
                value += f".Cycle({spec})"
            ctes.append(value)
        method = "WithRecursive" if clause.recursive else "With"
        return f"({builder}).{method}({_join(ctes)})"

    def _tail(
        self,
        builder: str,
        node: Any,
        *,
        allow_fetch: bool = True,
        allow_locks: bool = True,
    ) -> str:
        if node.sortClause:
            builder += f".OrderBy({_join(self.order(value) for value in node.sortClause)})"
        if node.limitOffset is not None:
            builder += f".OffsetExpr({self.expr(node.limitOffset)})"
        if node.limitCount is not None:
            if _node_name(node.limitCount) == "A_Const" and _node_name(node.limitCount.val) == "String" and str(node.limitCount.val.sval).upper() == "ALL":
                builder += ".LimitAll()"
            elif _enum_name(node.limitOption) == "LIMIT_OPTION_WITH_TIES":
                if not allow_fetch:
                    raise self.unsupported("FETCH WITH TIES on this rowset")
                builder += f".FetchExpr({self.expr(node.limitCount)}, qs.WithTies)"
            else:
                builder += f".LimitExpr({self.expr(node.limitCount)})"
        if node.lockingClause:
            if not allow_locks:
                raise self.unsupported("locking clause on a non-SELECT rowset")
            for lock in node.lockingClause:
                strength = {
                    "LCS_FORUPDATE": "ForUpdate",
                    "LCS_FORNOKEYUPDATE": "ForNoKeyUpdate",
                    "LCS_FORSHARE": "ForShare",
                    "LCS_FORKEYSHARE": "ForKeyShare",
                }.get(_enum_name(lock.strength))
                if strength is None:
                    raise self.unsupported(f"locking strength {_enum_name(lock.strength)}")
                value = f"qs.{strength}()"
                if lock.lockedRels:
                    value += ".Of(" + _join(_go_quote(rel.relname) for rel in lock.lockedRels) + ")"
                policy = _enum_name(lock.waitPolicy)
                if policy == "LockWaitError":
                    value += ".NoWait()"
                elif policy == "LockWaitSkip":
                    value += ".SkipLocked()"
                builder += f".Lock({value})"
        return builder

    def select(self, node: Any) -> str:
        if node.op != pgenums.SetOperation.SETOP_NONE:
            ctor = {
                "SETOP_UNION": "UnionAll" if node.all else "Union",
                "SETOP_INTERSECT": "IntersectAll" if node.all else "Intersect",
                "SETOP_EXCEPT": "ExceptAll" if node.all else "Except",
            }.get(_enum_name(node.op))
            if ctor is None:
                raise self.unsupported(f"set operation {_enum_name(node.op)}")
            builder = f"qs.{ctor}({self.rowset(node.larg)}, {self.rowset(node.rarg)})"
            builder = self._with(builder, node.withClause)
            return self.select_into(self._tail(builder, node, allow_locks=False), node.intoClause)
        if node.valuesLists is not None:
            rows = list(node.valuesLists)
            if not rows:
                raise self.unsupported("empty VALUES")
            builder = f"qs.ValuesExpr({_join(self.expr(value) for value in rows[0])})"
            for row in rows[1:]:
                builder += f".RowExpr({_join(self.expr(value) for value in row)})"
            builder = self._with(builder, node.withClause)
            return self.select_into(self._tail(builder, node, allow_locks=False), node.intoClause)
        if not node.targetList:
            builder = "qs.SelectNoColumns()"
        else:
            expressions: list[str] = []
            for target in node.targetList:
                if target.indirection:
                    raise self.unsupported("projection target indirection")
                value = self.expr(target.val)
                if target.name:
                    value += f".As({_go_quote(target.name)})"
                expressions.append(value)
            builder = f"qs.Select({_join(expressions)})"
        distinct = list(node.distinctClause or ())
        if distinct:
            if distinct == [None]:
                builder += ".Distinct()"
            else:
                builder += f".DistinctOn({_join(self.expr(value) for value in distinct)})"
        if node.fromClause:
            builder += f".FromExpr({_join(self.relation(value) for value in node.fromClause)})"
        if _node_name(node.whereClause) == "CurrentOfExpr":
            cursor = getattr(node.whereClause, "cursor_name", None)
            if not cursor:
                raise self.unsupported("CURRENT OF has no cursor name")
            builder += f".WhereCurrentOf({_go_quote(str(cursor))})"
        elif node.whereClause is not None:
            builder += f".Where(qs.AsCondition({self.expr(node.whereClause)}))"
        if node.groupClause:
            builder += f".GroupByExpr({_join(self.group_expr(value) for value in node.groupClause)})"
        if node.groupDistinct:
            builder += ".GroupByDistinct()"
        if node.havingClause is not None:
            builder += f".Having(qs.AsCondition({self.expr(node.havingClause)}))"
        if node.windowClause:
            for window in node.windowClause:
                window_name = window.name or window.refname
                if window_name is None:
                    raise self.unsupported("unnamed WINDOW definition")
                builder += f".Window({_go_quote(_str_value(window_name))}, {self.window(window)})"
        builder = self._with(builder, node.withClause)
        return self.select_into(self._tail(builder, node), node.intoClause)

    def select_into(self, builder: str, into: Any) -> str:
        if into is None:
            return builder
        relation = getattr(into, "rel", None)
        name = self.destination_name(relation, "SELECT INTO")
        if getattr(into, "colNames", None) or getattr(into, "options", None):
            raise self.unsupported("SELECT INTO destination options")
        if getattr(into, "accessMethod", None) or getattr(into, "tableSpaceName", None):
            raise self.unsupported("SELECT INTO destination storage options")
        commit = _enum_name(getattr(into, "onCommit", None))
        if commit != "ONCOMMIT_NOOP":
            raise self.unsupported("SELECT INTO ON COMMIT")
        if getattr(into, "skipData", False):
            raise self.unsupported("SELECT INTO WITH NO DATA")
        builder += f".Into({_go_quote(name)})"
        persistence = _str_value(getattr(relation, "relpersistence", "p"))
        if persistence == "t":
            builder += ".Temporary()"
        elif persistence == "u":
            builder += ".Unlogged()"
        elif persistence != "p":
            raise self.unsupported(f"SELECT INTO persistence {persistence}")
        return builder

    def window(self, node: Any) -> str:
        builder = "qs.Window()"
        # ``name`` names a WINDOW definition when this node comes from a
        # SELECT's windowClause.  Only refname is an inherited window inside
        # a parenthesised specification.
        base = node.refname
        if base:
            builder += f".Base({_go_quote(_str_value(base))})"
        if node.partitionClause:
            builder += f".PartitionBy({_join(self.expr(value) for value in node.partitionClause)})"
        if node.orderClause:
            builder += f".OrderBy({_join(self.order(value) for value in node.orderClause)})"
        options = int(node.frameOptions or 0)
        if options:
            # FrameOptions is a stable PostgreSQL bit mask.  Keep the decoder
            # local so the generated expression remains structural and can
            # use qs's validation for invalid boundary combinations.
            frame_rows = 0x004
            frame_range = 0x002
            frame_groups = 0x008
            frame_between = 0x010
            start_unbounded_preceding = 0x020
            end_unbounded_preceding = 0x040
            start_unbounded_following = 0x080
            end_unbounded_following = 0x100
            start_current = 0x200
            end_current = 0x400
            start_offset_preceding = 0x800
            end_offset_preceding = 0x1000
            start_offset_following = 0x2000
            end_offset_following = 0x4000
            exclude_current = 0x8000
            exclude_group = 0x10000
            exclude_ties = 0x20000
            exclude_no_others = 0x40000

            if options & frame_rows:
                method = "RowsBetween" if options & frame_between else "Rows"
            elif options & frame_groups:
                method = "GroupsBetween" if options & frame_between else "Groups"
            elif options & frame_range:
                method = "RangeBetween" if options & frame_between else "Range"
            else:
                raise self.unsupported(f"unknown window frame kind {options:#x}")

            def bound(start: bool) -> str:
                if start:
                    if options & start_unbounded_preceding:
                        return "qs.UnboundedPreceding()"
                    if options & start_unbounded_following:
                        return "qs.UnboundedFollowing()"
                    if options & start_current:
                        return "qs.CurrentRow()"
                    if options & start_offset_preceding:
                        if node.startOffset is None:
                            raise self.unsupported("window start offset is missing")
                        return f"qs.PrecedingExpr({self.expr(node.startOffset)})"
                    if options & start_offset_following:
                        if node.startOffset is None:
                            raise self.unsupported("window start offset is missing")
                        return f"qs.FollowingExpr({self.expr(node.startOffset)})"
                    # PostgreSQL's default frame starts at UNBOUNDED
                    # PRECEDING, including OVER () and RANGE ... CURRENT ROW.
                    return "qs.UnboundedPreceding()"
                if options & end_unbounded_preceding:
                    return "qs.UnboundedPreceding()"
                if options & end_unbounded_following:
                    return "qs.UnboundedFollowing()"
                if options & end_current:
                    return "qs.CurrentRow()"
                if options & end_offset_preceding:
                    if node.endOffset is None:
                        raise self.unsupported("window end offset is missing")
                    return f"qs.PrecedingExpr({self.expr(node.endOffset)})"
                if options & end_offset_following:
                    if node.endOffset is None:
                        raise self.unsupported("window end offset is missing")
                    return f"qs.FollowingExpr({self.expr(node.endOffset)})"
                return "qs.CurrentRow()"

            frame_args = f"{bound(True)}, {bound(False)}" if options & frame_between else bound(True)
            builder += f".{method}({frame_args})"
            exclusions = (
                (exclude_current, "ExcludeCurrentRow"),
                (exclude_group, "ExcludeGroup"),
                (exclude_ties, "ExcludeTies"),
                (exclude_no_others, "ExcludeNoOthers"),
            )
            selected = [name for bit, name in exclusions if options & bit]
            if len(selected) > 1:
                raise self.unsupported("multiple window frame exclusions")
            if selected:
                builder += f".Exclude(qs.{selected[0]})"
        return builder

    def returning(self, node: Any) -> tuple[list[str], str | None]:
        if node is None:
            return [], None
        aliases: dict[str, str] = {}
        option_order: list[str] = []
        for option in node.options or ():
            kind = _enum_name(option.option)
            alias = getattr(option, "value", None)
            if kind == "RETURNING_OPTION_OLD":
                key = "Old"
            elif kind == "RETURNING_OPTION_NEW":
                key = "New"
            else:
                raise self.unsupported(f"RETURNING row option {kind}")
            if key in aliases:
                raise self.unsupported(f"duplicate RETURNING {key} row option")
            if not alias:
                raise self.unsupported(f"RETURNING {key} row option has no alias")
            aliases[key] = str(alias)
            option_order.append(key)
        # ReturningAliases has fixed OLD, NEW slots.  Keep PostgreSQL's option
        # order exact rather than treating the two clauses as an unordered set.
        if option_order != sorted(option_order, key=("Old", "New").index):
            raise self.unsupported("RETURNING row options are in unsupported order")
        alias_builder: str | None = None
        if aliases:
            fields = _join(f"{key}: {_go_quote(value)}" for key, value in aliases.items())
            alias_builder = f"qs.ReturningAliases{{{fields}}}"
        expressions: list[str] = []
        for target in node.exprs or ():
            if target.indirection:
                raise self.unsupported("RETURNING target indirection")
            value = self.expr(target.val)
            if target.name:
                value += f".As({_go_quote(target.name)})"
            expressions.append(value)
        return expressions, alias_builder

    def assignments(self, targets: Sequence[Any]) -> list[str]:
        values: list[str] = []
        index = 0
        while index < len(targets):
            target = targets[index]
            if _node_name(target.val) == "MultiAssignRef":
                reference = target.val
                count = reference.ncolumns
                group = targets[index:index + count]
                if reference.colno != 1 or len(group) != count or any(
                    _node_name(item.val) != "MultiAssignRef"
                    or item.val.colno != column + 1
                    or item.val.ncolumns != count
                    or item.val.source != reference.source
                    or not item.name
                    for column, item in enumerate(group)
                ):
                    raise self.unsupported("complex row assignment target")
                columns = "[]string{" + _join(_go_quote(item.name) for item in group) + "}"
                target_exprs = "[]qs.Expr{" + _join(self.expr_from_target(item) for item in group) + "}"
                source = reference.source
                if _node_name(source) == "SubLink" and _enum_name(source.subLinkType) == "EXPR_SUBLINK":
                    values.append(f"qs.AssignRowFrom({target_exprs}, {self.rowset(source.subselect)})")
                elif _node_name(source) == "RowExpr":
                    row_format = _enum_name(getattr(source, "row_format", None))
                    if row_format == "COERCE_EXPLICIT_CALL" and not any(item.indirection for item in group):
                        values.append(f"qs.SetRow({columns}, {_join(self.expr(arg) for arg in source.args)})")
                    else:
                        constructor = "qs.Tuple" if row_format == "COERCE_IMPLICIT_CAST" else "qs.Row"
                        values.append(
                            f"qs.AssignRow({target_exprs}, {constructor}({_join(self.expr(arg) for arg in source.args)}))"
                        )
                else:
                    raise self.unsupported(f"row assignment source {_node_name(source)}")
                index += count
                continue
            value = self.expr(target.val)
            if target.indirection:
                target_expr = self.expr_from_target(target)
                values.append(f"qs.Assign({target_expr}, {value})")
            elif target.name:
                if _node_name(target.val) == "SetToDefault":
                    values.append(f"qs.SetDefault({_go_quote(target.name)})")
                else:
                    values.append(f"qs.SetExpr({_go_quote(target.name)}, {value})")
            else:
                raise self.unsupported("assignment target has no name")
            index += 1
        return values

    def merge_insert(self, targets: Sequence[Any], values: Sequence[Any] | None) -> str:
        """Emit MERGE INSERT values whose targets and RHS are separate lists.

        PostgreSQL stores MERGE's INSERT target names/indirections in
        ``targetList`` and the corresponding expressions in ``values``.  A
        normal INSERT stores both in each ResTarget, so passing this list to
        ``assignments`` loses every RHS and produces a misleading
        ``missing expression`` failure.
        """

        target_list = list(targets or ())
        value_list = list(values or ())
        if target_list and len(target_list) != len(value_list):
            raise self.unsupported("MERGE INSERT target/value width differs")
        if not target_list:
            if not value_list:
                return ".ThenInsertDefault()"
            rendered = _join(self.expr(value) for value in value_list)
            return f".ThenInsertValues([]string{{}}, {rendered})"

        rendered_values = [self.expr(value) for value in value_list]
        if all(not target.indirection for target in target_list):
            columns = _join(_go_quote(str(target.name)) for target in target_list)
            return f".ThenInsertValues([]string{{{columns}}}, {_join(rendered_values)})"

        assignments: list[str] = []
        for target, value in zip(target_list, rendered_values):
            target_expr = self.expr_from_target(target)
            assignments.append(f"qs.Assign({target_expr}, {value})")
        return f".ThenInsert({_join(assignments)})"

    def expr_from_target(self, target: Any) -> str:
        if not target.name:
            raise self.unsupported("assignment target has no name")
        value = f"qs.Ident({_go_quote(target.name)})"
        for item in target.indirection or ():
            if _node_name(item) == "String":
                value = f"({value}).Field({_go_quote(str(item.sval))})"
                continue
            if _node_name(item) != "A_Indices":
                raise self.unsupported("composite assignment target")
            lower = self.expr(item.lidx) if item.lidx is not None else None
            upper = self.expr(item.uidx) if item.uidx is not None else None
            if item.is_slice:
                if lower is not None and upper is not None:
                    value += f".Slice({lower}, {upper})"
                elif lower is not None:
                    value += f".SliceFrom({lower})"
                elif upper is not None:
                    value += f".SliceTo({upper})"
                else:
                    value += ".SliceAll()"
            elif upper is not None:
                value += f".Index({upper})"
            else:
                raise self.unsupported("array assignment has no index")
        return value

    def insert(self, node: Any) -> str:
        relation = self.relation(node.relation)
        builder = f"qs.InsertIntoTable({relation})"
        target_indirections = bool(node.cols and any(col.indirection for col in node.cols))
        if node.cols:
            if target_indirections:
                # Targets preserves composite fields and array subscripts while
                # allowing every INSERT source form (VALUES and SELECT).  The
                # Columns API intentionally accepts only ordinary identifiers.
                builder += f".Targets({_join(self.expr_from_target(col) for col in node.cols)})"
            else:
                builder += f".Columns({_join(_go_quote(col.name) for col in node.cols)})"
        select = node.selectStmt
        if select is None:
            builder += ".DefaultValues()"
        elif _node_name(select) != "SelectStmt":
            raise self.unsupported("INSERT source is not SelectStmt")
        elif select.valuesLists is not None:
            rows = list(select.valuesLists)
            if not rows:
                raise self.unsupported("INSERT has empty VALUES")
            builder += f".ValuesExpr({_join(self.expr(value) for value in rows[0])})"
            for row in rows[1:]:
                builder += f".ValuesExpr({_join(self.expr(value) for value in row)})"
        elif select.targetList is not None or select.op != pgenums.SetOperation.SETOP_NONE:
            if getattr(select, "intoClause", None) is not None:
                raise self.unsupported("SELECT INTO cannot be an INSERT source")
            builder += f".From({self.rowset(select)})"
        else:
            raise self.unsupported("INSERT source shape")
        if node.onConflictClause is not None:
            builder += f".OnConflict({self.conflict(node.onConflictClause)})"
        returning, aliases = self.returning(node.returningClause)
        if aliases:
            builder += f".ReturningRows({aliases})"
        if returning:
            builder += f".Returning({_join(returning)})"
        builder = self._with(builder, node.withClause)
        override = _enum_name(node.override)
        if override == "OVERRIDING_SYSTEM_VALUE":
            builder += ".OverridingSystemValue()"
        elif override == "OVERRIDING_USER_VALUE":
            builder += ".OverridingUserValue()"
        return builder

    def conflict(self, node: Any) -> str:
        infer = node.infer
        if infer is None:
            if _enum_name(node.action) != "ONCONFLICT_NOTHING":
                raise self.unsupported("ON CONFLICT DO UPDATE has no target")
            value = "qs.AnyConflict()"
        elif infer.conname:
            value = f"qs.ConflictConstraint({_go_quote(infer.conname)})"
        else:
            elems = list(infer.indexElems or ())
            if not elems:
                raise self.unsupported("empty conflict inference")
            elements: list[str] = []
            for elem in elems:
                if elem.name:
                    element = f"qs.IndexColumn({_go_quote(elem.name)})"
                elif elem.expr is not None:
                    element = f"qs.IndexExpr({self.expr(elem.expr)})"
                else:
                    raise self.unsupported("conflict index element has no expression")
                if elem.collation:
                    element += f".Collate({_go_quote('.'.join(_str_value(v) for v in elem.collation))})"
                if elem.opclass:
                    element += f".OpClass({_go_quote('.'.join(_str_value(v) for v in elem.opclass))})"
                elements.append(element)
            # ConflictColumns is only the shorthand for an entirely plain
            # name list.  Mixed expressions, collations, opclasses, or even a
            # single decorated name require ConflictIndex so no inference
            # element is silently discarded.
            plain_columns = all(
                elem.name and elem.expr is None and not elem.collation and not elem.opclass
                for elem in elems
            )
            if plain_columns:
                value = f"qs.ConflictColumns({_join(_go_quote(elem.name) for elem in elems if elem.name)})"
            else:
                value = f"qs.ConflictIndex({_join(elements)})"
            if infer.whereClause is not None:
                value += f".TargetWhere(qs.AsCondition({self.expr(infer.whereClause)}))"
        if _enum_name(node.action) == "ONCONFLICT_NOTHING":
            return f"({value}).DoNothing()"
        updates = self.assignments(node.targetList or ())
        if not updates:
            raise self.unsupported("ON CONFLICT DO UPDATE has no assignments")
        value += f".DoUpdate({_join(updates)})"
        if node.whereClause is not None:
            value += f".Where(qs.AsCondition({self.expr(node.whereClause)}))"
        return value

    def update(self, node: Any) -> str:
        builder = f"qs.UpdateTable({self.relation(node.relation)})"
        assigns = self.assignments(node.targetList or ())
        if not assigns:
            raise self.unsupported("UPDATE has no assignments")
        builder += f".Set({_join(assigns)})"
        if node.fromClause:
            builder += f".FromExpr({_join(self.relation(value) for value in node.fromClause)})"
        if _node_name(node.whereClause) == "CurrentOfExpr":
            cursor = getattr(node.whereClause, "cursor_name", None)
            if not cursor:
                raise self.unsupported("CURRENT OF has no cursor name")
            builder += f".WhereCurrentOf({_go_quote(str(cursor))})"
        elif node.whereClause is not None:
            builder += f".Where(qs.AsCondition({self.expr(node.whereClause)}))"
        returning, aliases = self.returning(node.returningClause)
        if aliases:
            builder += f".ReturningRows({aliases})"
        if returning:
            builder += f".Returning({_join(returning)})"
        return self._with(builder, node.withClause)

    def delete(self, node: Any) -> str:
        builder = f"qs.DeleteFromTable({self.relation(node.relation)})"
        if node.usingClause:
            builder += f".UsingExpr({_join(self.relation(value) for value in node.usingClause)})"
        if _node_name(node.whereClause) == "CurrentOfExpr":
            cursor = getattr(node.whereClause, "cursor_name", None)
            if not cursor:
                raise self.unsupported("CURRENT OF has no cursor name")
            builder += f".WhereCurrentOf({_go_quote(str(cursor))})"
        elif node.whereClause is not None:
            builder += f".Where(qs.AsCondition({self.expr(node.whereClause)}))"
        returning, aliases = self.returning(node.returningClause)
        if aliases:
            builder += f".ReturningRows({aliases})"
        if returning:
            builder += f".Returning({_join(returning)})"
        return self._with(builder, node.withClause)

    def merge(self, node: Any) -> str:
        builder = f"qs.MergeIntoTable({self.relation(node.relation)}).Using({self.relation(node.sourceRelation)})"
        if node.joinCondition is None:
            raise self.unsupported("MERGE has no join condition")
        builder += f".On(qs.AsCondition({self.expr(node.joinCondition)}))"
        branches: list[str] = []
        for branch in node.mergeWhenClauses or ():
            kind = {
                "MERGE_WHEN_MATCHED": "Matched",
                "MERGE_WHEN_NOT_MATCHED_BY_TARGET": "NotMatchedByTarget",
                "MERGE_WHEN_NOT_MATCHED_BY_SOURCE": "NotMatchedBySource",
            }.get(_enum_name(branch.matchKind))
            if kind is None:
                raise self.unsupported(f"MERGE match kind {_enum_name(branch.matchKind)}")
            value = f"qs.{kind}()"
            if branch.condition is not None:
                value += f".And(qs.AsCondition({self.expr(branch.condition)}))"
            action = _enum_name(branch.commandType)
            if action == "CMD_UPDATE":
                assignments = self.assignments(branch.targetList or ())
                value += f".ThenUpdate({_join(assignments)})"
            elif action == "CMD_DELETE":
                value += ".ThenDelete()"
            elif action == "CMD_NOTHING":
                value += ".ThenDoNothing()"
            elif action == "CMD_INSERT":
                override = _enum_name(branch.override)
                if override == "OVERRIDING_SYSTEM_VALUE":
                    value += ".OverridingSystemValue()"
                elif override == "OVERRIDING_USER_VALUE":
                    value += ".OverridingUserValue()"
                value += self.merge_insert(branch.targetList, branch.values)
            else:
                raise self.unsupported(f"MERGE action {action}")
            branches.append(value)
        if not branches:
            raise self.unsupported("MERGE has no WHEN branches")
        builder += f".When({_join(branches)})"
        returning, aliases = self.returning(node.returningClause)
        if aliases:
            builder += f".ReturningRows({aliases})"
        if returning:
            builder += f".Returning({_join(returning)})"
        if node.withClause is not None and node.withClause.recursive:
            raise self.unsupported("recursive CTE on MERGE")
        return self._with(builder, node.withClause)

    def statement(self, node: Any) -> str:
        if _node_name(node) == "ExplainStmt":
            query = self.rowset(node.query)
            builder = f"qs.Explain({query})"
            for option in node.options or ():
                name = str(option.defname).lower()
                if name == "format":
                    fmt = str(getattr(option.arg, "sval", "")).lower()
                    formats = {"text": "ExplainText", "json": "ExplainJSON", "xml": "ExplainXML", "yaml": "ExplainYAML"}
                    if fmt not in formats:
                        raise self.unsupported(f"EXPLAIN format {fmt}")
                    builder += f".Format(qs.{formats[fmt]})"
                    continue
                methods = {
                    "analyze": "Analyze",
                    "verbose": "Verbose",
                    "costs": "Costs",
                    "settings": "Settings",
                    "buffers": "Buffers",
                    "wal": "WAL",
                    "timing": "Timing",
                    "summary": "Summary",
                    "generic_plan": "GenericPlan",
                    "memory": "Memory",
                }
                if name == "serialize":
                    # Preserve bare SERIALIZE separately from explicit TEXT;
                    # both mean text to PostgreSQL, but the parser AST keeps
                    # the spelling and qs exposes both forms.
                    if option.arg is None:
                        builder += ".Serialize()"
                        continue
                    mode = str(getattr(option.arg, "sval", "text")).lower()
                    modes = {"none": "SerializeNone", "text": "SerializeText", "binary": "SerializeBinary"}
                    if mode not in modes:
                        raise self.unsupported(f"EXPLAIN serialization mode {mode}")
                    builder += f".Serialize(qs.{modes[mode]})"
                    continue
                method = methods.get(name)
                if method is None:
                    raise self.unsupported(f"EXPLAIN option {name}")
                value = True
                if option.arg is not None:
                    if _node_name(option.arg) != "String" or str(option.arg.sval).lower() not in {"true", "false", "on", "off"}:
                        raise self.unsupported(f"EXPLAIN option {name} has non-boolean value")
                    value = str(option.arg.sval).lower() in {"true", "on"}
                builder += f".{method}({str(value).lower()})"
            return builder
        return self.rowset(node)


def _normalise_type_names(value: Any) -> Any:
    aliases = {
        "int2": "smallint",
        "int4": "integer",
        "int8": "bigint",
        "float4": "real",
        "float8": "double precision",
        "bool": "boolean",
        "timestamptz": "timestamp with time zone",
        "timetz": "time with time zone",
        "decimal": "numeric",
    }
    if isinstance(value, str):
        return aliases.get(value.lower(), value)
    return value


def canonical_ast(node: Any, *, parent: str = "", field: str = "") -> Any:
    """Convert pglast trees into a comparison form with narrow equivalences.

    Locations and parser bookkeeping are removed.  PostgreSQL's implicit
    ascending order, built-in type aliases, numeric spelling, and the
    spelling of boolean EXPLAIN options are semantic equivalences.  No
    literals, casts, joins, set-operation grouping, NULLS direction, or
    boolean operator order is discarded.
    """

    if isinstance(node, enum.Enum):
        value = node.name
        if parent == "SortBy" and field == "sortby_dir" and value == "SORTBY_DEFAULT":
            return "SORTBY_ASC"
        return value
    if node is None or isinstance(node, (bool, int, float, str)):
        return node
    if isinstance(node, (tuple, list)):
        values = [canonical_ast(value, parent=parent, field=field) for value in node]
        if parent == "SortBy" and field == "sortby_dir":
            return values
        return values
    if isinstance(node, pgast.Node):
        result: dict[str, Any] = {"_type": type(node).__name__}
        for name in getattr(node, "__slots__", {}):
            if name in {"location", "stmt_location", "stmt_len", "rexpr_list_start", "rexpr_list_end", "list_start", "list_end", "name_location"}:
                continue
            value = getattr(node, name, None)
            if type(node).__name__ == "WindowDef" and name == "frameOptions" and value in {0x422, 0x423, 0x433}:
                # OVER () and its explicit RANGE UNBOUNDED PRECEDING /
                # CURRENT ROW spellings carry equivalent planner frames.
                result[name] = 0x422
                continue
            if type(node).__name__ == "TypeName" and name == "names":
                raw_parts = [_str_value(part) for part in (value or ())]
                # Keep an explicit qualification: pg_catalog.numeric and a
                # quoted/search_path-resolved type can denote different
                # objects.  Alias normalisation applies only to an
                # unqualified, lower-case grammar name.
                parts = raw_parts
                result[name] = parts
                continue
            if (
                type(node).__name__ == "DefElem"
                and name == "arg"
                and parent == "ExplainStmt"
                and field == "options"
            ):
                option_name = str(getattr(node, "defname", "")).lower()
                if option_name in _EXPLAIN_BOOLEAN_OPTIONS:
                    # PostgreSQL accepts bare flags, ON/OFF, and TRUE/FALSE;
                    # qs's typed option methods render TRUE/FALSE.  Keep the
                    # option itself, order, and semantic value while removing
                    # only this spelling difference.
                    if value is None:
                        result[name] = {"_type": "Boolean", "boolval": True}
                        continue
                    parsed = _explain_boolean_value(value)
                    if isinstance(parsed, bool):
                        result[name] = {"_type": "Boolean", "boolval": parsed}
                        continue
            result[name] = canonical_ast(value, parent=type(node).__name__, field=name)
        return result
    if hasattr(node, "name") and hasattr(node, "value"):
        return node.name
    return str(node)


_EXPLAIN_BOOLEAN_OPTIONS = frozenset(
    {
        "analyze",
        "verbose",
        "costs",
        "settings",
        "buffers",
        "wal",
        "timing",
        "summary",
        "generic_plan",
        "memory",
    }
)


def _explain_boolean_spelling_difference(original: Any, rendered: Any) -> bool:
    """Report whether equal semantic EXPLAIN flags used different tokens."""

    def values(node: Any) -> list[tuple[str, str | None, bool | str | None]]:
        found: list[tuple[str, str | None, bool | str | None]] = []
        # DefElem is used by many PostgreSQL clauses.  Only options attached
        # directly to an ExplainStmt receive boolean spelling equivalence.
        for explain in _walk_nodes(node):
            if _node_name(explain) != "ExplainStmt":
                continue
            for value in getattr(explain, "options", ()) or ():
                if _node_name(value) != "DefElem":
                    continue
                name = str(getattr(value, "defname", "")).lower()
                if name not in _EXPLAIN_BOOLEAN_OPTIONS:
                    continue
                arg = getattr(value, "arg", None)
                found.append((name, None if arg is None else _str_value(arg), _explain_boolean_value(arg)))
        return found

    left, right = values(original), values(rendered)
    if not left or left == right:
        return False

    return [(name, semantic) for name, _, semantic in left] == [
        (name, semantic) for name, _, semantic in right
    ]


def _explain_boolean_value(value: Any) -> bool | str | None:
    """Return a semantic EXPLAIN boolean or the untouched token."""

    if value is None:
        return True
    name = _node_name(value)
    if name == "Boolean":
        return bool(value.boolval)
    if name == "Integer":
        integer = int(value.ival)
        if integer in {0, 1}:
            return bool(integer)
        return str(integer)
    if name == "String":
        token = str(value.sval).lower()
        if token in {"true", "on", "1"}:
            return True
        if token in {"false", "off", "0"}:
            return False
        return str(value.sval)
    return _node_name(value)


def compare_trees(original: Any, rendered: Any) -> tuple[bool, tuple[str, ...]]:
    left = canonical_ast(original)
    right = canonical_ast(rendered)
    if left == right:
        if _explain_boolean_spelling_difference(original, rendered):
            return True, ("explain_boolean_spelling",)
        return True, ()
    normalizations = (
        "default_ascending",
        "type_alias",
        "numeric_literal_spelling",
        "and_associativity",
    )
    # The canonical representation already applies the first three.  AND
    # associativity is intentionally handled only when the two trees differ
    # solely by binary nesting, with all leaves and order preserved.
    if _and_shape(left) == _and_shape(right):
        return True, ("and_associativity",)
    return False, ()


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


def _and_shape(value: Any) -> Any:
    if isinstance(value, dict):
        if value.get("_type") == "BoolExpr" and value.get("boolop") == "AND_EXPR":
            args: list[Any] = []
            for arg in value.get("args", []):
                shape = _and_shape(arg)
                if isinstance(shape, tuple) and shape and shape[0] == "AND":
                    args.extend(shape[1])
                else:
                    args.append(shape)
            return ("AND", tuple(args))
        return tuple((key, _and_shape(item)) for key, item in sorted(value.items()))
    if isinstance(value, list):
        return tuple(_and_shape(item) for item in value)
    return value


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
        ("EXPLAIN (COSTS OFF) SELECT 1", "EXPLAIN (COSTS ON) SELECT 1"),
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
        if compare_trees(parse_sql(left)[0].stmt, parse_sql(right)[0].stmt)[0]:
            raise RuntimeError(f"canonical AST oracle collapsed distinct queries: {left!r} vs {right!r}")
    if shape_id(parse_sql("SELECT 1")[0].stmt) != shape_id(parse_sql("SELECT 2")[0].stmt):
        raise RuntimeError("query-shape oracle retained a literal payload")
    if shape_id(parse_sql("SELECT a FROM t")[0].stmt) == shape_id(parse_sql("SELECT b FROM t")[0].stmt):
        raise RuntimeError("query-shape oracle collapsed distinct identifiers")


def _go_probe(repo: Path, occurrences: Sequence[QueryOccurrence], probe_dir: Path) -> dict[int, dict[str, Any]]:
    """Compile and execute chunked builders, returning probe results."""

    probe_dir.mkdir(parents=True, exist_ok=True)
    result: dict[int, dict[str, Any]] = {}
    expected_ids = {occurrence.id for occurrence in occurrences}
    if len(expected_ids) != len(occurrences):
        raise RuntimeError("Go probe input contains duplicate occurrence ids")
    chunk_size = 300
    for chunk_start in range(0, len(occurrences), chunk_size):
        chunk = occurrences[chunk_start : chunk_start + chunk_size]
        path = probe_dir / f"probe_{chunk_start:06d}.go"
        entries: list[str] = []
        for occurrence in chunk:
            entries.append(
                "{id: %d, build: func() qs.Statement { return %s }}"
                % (occurrence.id, occurrence.builder)
            )
        source = """package main

import (
    "encoding/json"
    "fmt"
    "os"
    qs "github.com/jacoelho/qs"
)

type probeCase struct {
    id int
    build func() qs.Statement
}
type probeResult struct {
    ID int `json:"id"`
    SQL string `json:"sql"`
    Args []string `json:"args"`
    Error string `json:"error,omitempty"`
}

func argumentValues(args []any) ([]string, error) {
    values := make([]string, len(args))
    for index, arg := range args {
        value, ok := arg.(string)
        if !ok {
            return nil, fmt.Errorf("argument %%d has type %%T, want string", index, arg)
        }
        values[index] = value
    }
    return values, nil
}

var cases = []probeCase{
%s
}

func main() {
    output := make([]probeResult, 0, len(cases))
    for _, item := range cases {
        sql, args, err := item.build().ToSQL()
        result := probeResult{ID: item.id, SQL: sql}
        if err != nil {
            result.Error = err.Error()
        } else if values, argErr := argumentValues(args); argErr != nil {
            result.Error = argErr.Error()
        } else {
            result.Args = values
        }
        output = append(output, result)
    }
    if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
        panic(err)
    }
}
""" % ((",\n".join(entries) + ",") if entries else "")
        path.write_text(source, encoding="utf-8")
        probe_env = os.environ.copy()
        # The desktop sandbox may not permit the user's global Go build
        # cache.  Keep probe artifacts entirely in the caller-owned temp
        # directory so the measurement remains reproducible and local.
        cache = probe_dir / "gocache"
        cache.mkdir(parents=True, exist_ok=True)
        probe_env["GOCACHE"] = str(cache)
        try:
            run = subprocess.run(
                ["go", "run", str(path)],
                cwd=repo,
                capture_output=True,
                text=True,
                env=probe_env,
                timeout=PROBE_TIMEOUT_SECONDS,
            )
        except subprocess.TimeoutExpired as exc:
            raise RuntimeError(
                f"Go probe {path.name} exceeded the {PROBE_TIMEOUT_SECONDS}s timeout"
            ) from exc
        if run.returncode != 0:
            raise RuntimeError(
                f"Go probe {path.name} failed with exit {run.returncode}:\n"
                f"{run.stderr[-4000:]}"
            )
        try:
            values = json.loads(run.stdout)
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"Go probe {path.name} returned invalid JSON: {run.stdout[-1000:]}") from exc
        for value in values:
            identifier = int(value["id"])
            if identifier in result:
                raise RuntimeError(f"Go probe returned duplicate occurrence id {identifier}")
            result[identifier] = value
    missing = sorted(expected_ids - set(result))
    extra = sorted(set(result) - expected_ids)
    if missing or extra:
        details = []
        if missing:
            details.append(f"missing ids {missing[:8]}")
        if extra:
            details.append(f"unexpected ids {extra[:8]}")
        raise RuntimeError("Go probe id set mismatch: " + "; ".join(details))
    return result


def _target_ast(sql: str) -> Any:
    statements = parse_sql(sql)
    if len(statements) != 1:
        raise ValueError("rendered SQL contains more than one statement")
    return statements[0].stmt


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
        "shape": shape_id(occurrence.source_ast),
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
    repo: Path,
    units: Sequence[SQLUnit],
    occurrences: list[QueryOccurrence],
    counts: Counter[str],
    commit: str,
    parser_version: str,
    probe: bool,
    probe_dir: Path | None,
    *,
    census_occurrences: int,
    limit: int | None,
) -> dict[str, Any]:
    supported = [item for item in occurrences if item.status == "verified"]
    planner = [item for item in occurrences if item.planner]
    planner_supported = [item for item in planner if item.status == "verified"]
    shape_groups: dict[str, list[QueryOccurrence]] = defaultdict(list)
    for item in occurrences:
        shape_groups[shape_id(item.source_ast)].append(item)
    distinct_supported = [group for group in shape_groups.values() if all(item.status == "verified" for item in group)]
    planner_shapes = [group for group in shape_groups.values() if any(item.planner for item in group)]
    planner_shapes_supported = [group for group in planner_shapes if all(item.status == "verified" for item in group)]
    family_counts: dict[str, dict[str, int]] = {}
    families = sorted({family for item in occurrences for family in item.families})
    for family in families:
        selected = [item for item in occurrences if family in item.families]
        family_counts[family] = {
            "total": len(selected),
            "supported": sum(item.status == "verified" for item in selected),
            "unsupported": sum(item.status != "verified" for item in selected),
        }
    failures = Counter(item.reason for item in occurrences if item.status != "verified")
    files = sorted({item.file for item in units})
    planner_files = sorted({unit.file for unit in units if _planner_file(unit.file)})
    report = {
        "schema": "qs.postgres-coverage.v1",
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
            "included": "parsable SELECT/VALUES/TABLE, INSERT, UPDATE, DELETE, MERGE and EXPLAIN query occurrences, including PREPARE/view/CTAS/cursor bodies and rule actions",
            "harnesses": sorted(LITERAL_QUERY_HARNESSES),
            "excluded": "DDL, session/control, COPY payloads, psql commands and parser-invalid input; each is counted separately",
            "comparison": "rendered qs SQL is reparsed and compared with source AST; locations, built-in aliases, explicit/default ASC, EXPLAIN boolean spellings, and the documented narrow equivalences are removed",
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
            "supported": len(supported),
            "unsupported": len(occurrences) - len(supported),
            "verified_rate": (len(supported) / len(occurrences) if occurrences else 0.0),
            "planner_total": len(planner),
            "planner_supported": len(planner_supported),
            "planner_rate": (len(planner_supported) / len(planner) if planner else 0.0),
            "distinct_query_shapes": len(shape_groups),
            "distinct_supported_shapes": len(distinct_supported),
            "distinct_verified_rate": (len(distinct_supported) / len(shape_groups) if shape_groups else 0.0),
            "planner_distinct_shapes": len(planner_shapes),
            "planner_distinct_supported_shapes": len(planner_shapes_supported),
            "planner_distinct_rate": (len(planner_shapes_supported) / len(planner_shapes) if planner_shapes else 0.0),
        },
        "families": family_counts,
        "failure_reasons": [
            {"reason": reason, "count": count, "examples": [item.id for item in occurrences if item.reason == reason][:8]}
            for reason, count in failures.most_common()
        ],
        "probe": {"ran": probe, "directory": str(probe_dir) if probe_dir else None, "forbidden_constructors": ["UnsafeSQL", "Fragment", "StatementSQL"]},
        "entries": [_record_json(item, root) for item in occurrences],
        "excluded_units": [
            {"source": unit.file, "line": unit.line, "kind": unit.kind, "detail": unit.detail}
            for unit in units
            if unit.kind != "sql"
        ],
    }
    return report


def _go_string_slice(values: Iterable[str]) -> str:
    encoded = [_go_quote(value) for value in values]
    return "[]string{" + ", ".join(encoded) + "}"


def _export_metrics(occurrences: Sequence[QueryOccurrence]) -> dict[str, int]:
    shape_groups: dict[str, list[QueryOccurrence]] = defaultdict(list)
    for occurrence in occurrences:
        shape_groups[shape_id(occurrence.source_ast)].append(occurrence)
    distinct_supported = sum(
        all(item.status == "verified" for item in group)
        for group in shape_groups.values()
    )
    planner_groups = [group for group in shape_groups.values() if any(item.planner for item in group)]
    planner_distinct_supported = sum(
        all(item.status == "verified" for item in group)
        for group in planner_groups
    )
    return {
        "total": len(occurrences),
        "verified": sum(item.status == "verified" for item in occurrences),
        "construction_error": sum(item.status == "construction_error" for item in occurrences),
        "unsupported": sum(item.status == "unsupported" for item in occurrences),
        "planner_total": sum(item.planner for item in occurrences),
        "planner_verified": sum(item.planner and item.status == "verified" for item in occurrences),
        "distinct_shapes": len(shape_groups),
        "distinct_verified_shapes": distinct_supported,
        "planner_distinct_shapes": len(planner_groups),
        "planner_distinct_verified_shapes": planner_distinct_supported,
    }


def _validate_export_state(
    occurrences: Sequence[QueryOccurrence],
    census_occurrences: int,
    probe_results: dict[int, dict[str, Any]],
    *,
    expected_sql_sha256: str | None,
    commit: str | None,
) -> None:
    """Reject anything that could produce a misleading frozen fixture."""

    if expected_sql_sha256 is None:
        raise SystemExit("--export-go requires --expected-sql-sha256")
    if commit is None or not re.fullmatch(r"[0-9a-f]{40}", commit, re.I):
        raise SystemExit("--export-go requires an explicit 40-character --commit")
    if pglast.__version__ != _PINNED_PGLAST_VERSION:
        raise SystemExit(
            f"--export-go requires pglast {_PINNED_PGLAST_VERSION}, got {pglast.__version__}"
        )
    if tuple(pglast.get_postgresql_version()) != _PINNED_POSTGRES_VERSION:
        raise SystemExit(
            "--export-go requires the PostgreSQL "
            f"{_PINNED_POSTGRES_VERSION[0]}.{_PINNED_POSTGRES_VERSION[1]} parser"
        )
    if len(occurrences) != census_occurrences:
        raise SystemExit("--export-go requires the complete unbounded corpus")
    identifiers = [item.id for item in occurrences]
    expected_ids = set(range(census_occurrences))
    if len(set(identifiers)) != len(identifiers) or set(identifiers) != expected_ids:
        raise SystemExit("--export-go requires contiguous unique occurrence ids")
    probe_ids = set(probe_results)
    probe_expected = {
        item.id for item in occurrences if item.status in {"verified", "construction_error", "arg_mismatch"}
    }
    if probe_ids != probe_expected:
        missing = sorted(probe_expected - probe_ids)
        extra = sorted(probe_ids - probe_expected)
        raise SystemExit(
            "--export-go probe id set mismatch: "
            f"missing={missing[:8]!r} extra={extra[:8]!r}"
        )
    invalid = [
        item
        for item in occurrences
        if item.status not in {"verified", "construction_error", "unsupported"}
    ]
    if invalid:
        raise SystemExit(
            "--export-go requires every occurrence to be probe-classified; "
            f"first id {invalid[0].id} has status {invalid[0].status!r}"
        )
    verified = {item.id for item in occurrences if item.status == "verified"}
    exportable = {
        item.id
        for item in occurrences
        if item.status == "verified" and item.builder and item.generated_sql
    }
    if verified != exportable:
        raise SystemExit("--export-go verified id set differs from exportable builder id set")
    raw = [
        item.id
        for item in occurrences
        if item.builder and re.search(r"\bqs\.(?:UnsafeSQL|Fragment|StatementSQL)\s*\(", item.builder)
    ]
    if raw:
        raise SystemExit(f"--export-go found forbidden raw constructors at ids {raw[:8]!r}")
    for item in occurrences:
        if item.status == "unsupported" and item.builder:
            raise SystemExit(f"unsupported occurrence {item.id} unexpectedly has a builder")
        if item.status in {"verified", "construction_error"} and not item.builder:
            raise SystemExit(f"{item.status} occurrence {item.id} has no builder")
        if item.status == "verified" and item.id not in probe_results:
            raise SystemExit(f"verified occurrence {item.id} has no probe result")


def _fixture_record(occurrence: QueryOccurrence, root: Path) -> dict[str, Any]:
    """Return the complete immutable metadata record stored in JSONL.

    The Go test harness deliberately receives only this fixed data plus the
    typed builder in its owning shard. Keeping the SQL and provenance here
    makes the native test process independent of pglast and avoids retaining a
    second in-memory copy of all builders.
    """

    expected_args = (
        _expected_arguments(occurrence.source_ast, occurrence.id)
        if occurrence.status != "unsupported"
        else ()
    )
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
        "shape": shape_id(occurrence.source_ast),
        "original_sql": occurrence.source_sql,
        "normalization": list(occurrence.normalization),
        "want_sql": occurrence.generated_sql,
        "want_args": list(expected_args),
    }


def _jsonl_bytes(items: Sequence[QueryOccurrence], root: Path) -> bytes:
    """Encode a shard with one deterministic UTF-8 JSON object per line."""

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


def _render_shard_test(index: int, items: Sequence[QueryOccurrence]) -> str:
    """Render one test-only package and its explicit ID-to-builder table."""

    package = f"shard{index:02d}"
    lines = [
        "// Code generated by scripts/postgres_corpus.py; DO NOT EDIT.\n",
        "//\n",
        "// This file owns only the typed qs builders for its stride-8 shard.\n",
        "\n",
        f"package {package}\n",
        "\n",
        "import (\n",
        '\t_ "embed"\n',
        '\t"testing"\n',
    ]
    if any(item.builder for item in items):
        lines.append('\tqs "github.com/jacoelho/qs"\n')
    lines.extend(
        [
            '\t"github.com/jacoelho/qs/internal/postgrescorpus"\n',
            ")\n",
            "\n",
            "// corpusData is the immutable SQL/provenance side of this shard.\n",
            "//go:embed corpus.jsonl\n",
            "var corpusData string\n",
            "\n",
        ]
    )
    for occurrence in items:
        if occurrence.builder:
            lines.append(
                f"func build{occurrence.id:05d}() qs.Statement {{ return {occurrence.builder} }}\n"
            )
    lines.extend(("\n", "func TestPostgreSQLCorpusShard", f"{index:02d}", "(t *testing.T) {\n"))
    lines.extend(("\tt.Parallel()\n", "\tpostgrescorpus.Run(t, ", str(index), ", corpusData, []postgrescorpus.Factory{\n"))
    for occurrence in items:
        build = f"build{occurrence.id:05d}" if occurrence.builder else "nil"
        lines.append(
            f"\t\t{{ID: {occurrence.id}, Status: {_go_quote(occurrence.status)}, Build: {build}}},\n"
        )
    lines.extend(("\t})\n", "}\n"))
    return "".join(lines)


def _shard_counts(items: Sequence[QueryOccurrence]) -> dict[str, int]:
    return {
        "verified": sum(item.status == "verified" for item in items),
        "construction_error": sum(item.status == "construction_error" for item in items),
        "unsupported": sum(item.status == "unsupported" for item in items),
    }


def _remove_generated_path(path: Path) -> None:
    """Remove only files from the previous exporter layout."""

    if path.is_file():
        path.unlink()


def _export_go(
    output: Path,
    occurrences: Sequence[QueryOccurrence],
    *,
    source_root: Path,
    commit: str,
    sql_sha256: str,
    parser_version: str,
    parser_postgres_version: tuple[int, int],
) -> None:
    """Write the checked-in eight-shard native fixture after full verification."""

    output.mkdir(parents=True, exist_ok=True)
    if len(occurrences) == 0:
        raise SystemExit("--export-go requires at least one occurrence")
    metrics = _export_metrics(occurrences)
    shards: list[list[QueryOccurrence]] = [[] for _ in range(_CORPUS_SHARDS)]
    for occurrence in occurrences:
        shard = occurrence.id % _CORPUS_SHARDS
        if shard < 0:
            raise SystemExit(f"--export-go cannot export negative occurrence id {occurrence.id}")
        shards[shard].append(occurrence)
    for index, items in enumerate(shards):
        expected = list(range(index, len(occurrences), _CORPUS_SHARDS))
        actual = [item.id for item in items]
        if actual != expected:
            raise SystemExit(
                f"--export-go shard {index} IDs are not the expected stride-8 sequence"
            )

    # The monolithic files are from the pre-v2 exporter. They are explicitly
    # removed so a stale package cannot silently keep the old memory profile.
    _remove_generated_path(output / "corpus_generated.go")
    _remove_generated_path(output / "corpus_generated_test.go")
    manifest_shards: list[dict[str, Any]] = []
    for index, items in enumerate(shards):
        shard_dir = output / f"shard{index:02d}"
        shard_dir.mkdir(parents=True, exist_ok=True)
        # These names were emitted by earlier development versions. Only the
        # two v2 files are allowed in a shard package.
        for old_name in ("corpus_generated.go", "corpus_generated_test.go", "corpus.jsonl", "doc.go"):
            _remove_generated_path(shard_dir / old_name)
        data = _jsonl_bytes(items, source_root)
        jsonl_path = shard_dir / "corpus.jsonl"
        jsonl_path.write_bytes(data)
        generated = shard_dir / "corpus_generated_test.go"
        generated.write_text(_render_shard_test(index, items), encoding="utf-8")
        try:
            formatted = subprocess.run(
                ["gofmt", "-w", str(generated)],
                capture_output=True,
                text=True,
                timeout=PROBE_TIMEOUT_SECONDS,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise RuntimeError(f"gofmt failed for {generated}: {exc}") from exc
        if formatted.returncode != 0:
            raise RuntimeError(
                f"gofmt failed for {generated} with exit {formatted.returncode}: "
                f"{formatted.stderr[-4000:]}"
            )
        status_counts = _shard_counts(items)
        manifest_shards.append(
            {
                "index": index,
                "package": f"shard{index:02d}",
                "jsonl": f"shard{index:02d}/corpus.jsonl",
                "sha256": hashlib.sha256(data).hexdigest(),
                "count": len(items),
                "first_id": items[0].id if items else -1,
                "last_id": items[-1].id if items else -1,
                **status_counts,
            }
        )
    manifest = {
        "schema": _CORPUS_SCHEMA,
        "revision": commit,
        "sql_sha256": sql_sha256,
        "parser": {
            "package": "pglast",
            "version": parser_version,
            "postgresql": f"{parser_postgres_version[0]}.{parser_postgres_version[1]}",
        },
        "counts": metrics,
        "shards": manifest_shards,
    }
    (output / "manifest.json").write_text(
        json.dumps(manifest, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )


def run(args: argparse.Namespace) -> int:
    _canonical_oracles()
    export_go = getattr(args, "export_go", None)
    if export_go is not None:
        if not args.probe:
            raise SystemExit("--export-go requires the compiled Go probe")
        if args.limit is not None:
            raise SystemExit("--export-go requires the complete corpus; remove --limit")
        if not getattr(args, "expected_sql_sha256", None):
            raise SystemExit("--export-go requires --expected-sql-sha256")
        if not getattr(args, "commit", None):
            raise SystemExit("--export-go requires an explicit --commit")
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
    emitter_errors: Counter[str] = Counter()
    probe_occurrences: list[QueryOccurrence] = []
    for occurrence in occurrences:
        emitter = GoEmitter(occurrence.id)
        try:
            occurrence.builder = emitter.statement(occurrence.node)
            # Check constructor calls rather than substrings: a legitimate
            # string literal may contain words such as ``FragmentDelimiter``.
            if re.search(r"\bqs\.(?:UnsafeSQL|Fragment|StatementSQL)\s*\(", occurrence.builder):
                raise Unsupported("generated builder contains a forbidden raw constructor")
            occurrence.normalization = tuple(sorted(emitter.normalizations))
            occurrence.status = "probe_pending"
            probe_occurrences.append(occurrence)
        except Unsupported as exc:
            occurrence.status = "unsupported"
            occurrence.reason = exc.reason
            emitter_errors[exc.reason] += 1
        except Exception as exc:  # keep one malformed AST from hiding the corpus
            occurrence.status = "unsupported"
            occurrence.reason = f"emitter error: {type(exc).__name__}: {exc}"
            emitter_errors[occurrence.reason] += 1

    probe_results: dict[int, dict[str, Any]] = {}
    probe_path: Path | None = None
    if args.probe and probe_occurrences:
        if args.probe_dir:
            probe_path = args.probe_dir.resolve()
            if probe_path.exists() and not probe_path.is_dir():
                raise SystemExit(f"--probe-dir is not a directory: {probe_path}")
        else:
            probe_path = Path(tempfile.mkdtemp(prefix="qs-postgres-probe-"))
        probe_results = _go_probe(repo, probe_occurrences, probe_path)
        for occurrence in probe_occurrences:
            value = probe_results.get(occurrence.id, {})
            if value.get("error"):
                occurrence.status = "construction_error"
                occurrence.reason = value["error"]
                continue
            rendered = str(value.get("sql", ""))
            occurrence.generated_sql = rendered
            expected_args = list(_expected_arguments(occurrence.source_ast, occurrence.id))
            actual_args = list(value.get("args", ()))
            if actual_args != expected_args:
                occurrence.status = "arg_mismatch"
                occurrence.reason = (
                    "bound arguments differ from source ParamRef order: "
                    f"expected {expected_args!r}, got {actual_args!r}"
                )
                continue
            try:
                rendered_ast = _target_ast(rendered)
                equal, extra = compare_trees(occurrence.source_ast, rendered_ast)
                if equal:
                    occurrence.status = "verified"
                    if extra:
                        occurrence.normalization = tuple(sorted(set(occurrence.normalization) | set(extra)))
                else:
                    occurrence.status = "ast_mismatch"
                    occurrence.reason = "rendered SQL AST differs from source AST"
            except Exception as exc:
                occurrence.status = "reparse_error"
                occurrence.reason = f"rendered SQL did not parse: {exc}"
    else:
        for occurrence in probe_occurrences:
            occurrence.status = "generated"

    commit = args.commit or _discover_commit(root)
    report = make_report(
        root,
        repo,
        units,
        occurrences,
        counts,
        commit,
        pglast.__version__,
        bool(args.probe),
        probe_path,
        census_occurrences=census_occurrences,
        limit=args.limit,
    )
    report["emitter_failure_reasons"] = [{"reason": reason, "count": count} for reason, count in emitter_errors.most_common()]
    minimum_support = _threshold_fraction(getattr(args, "minimum_support", None))
    minimum_planner_support = _threshold_fraction(getattr(args, "minimum_planner_support", None))
    minimum_shape_support = _threshold_fraction(getattr(args, "minimum_shape_support", None))
    minimum_planner_shape_support = _threshold_fraction(
        getattr(args, "minimum_planner_shape_support", None)
    )
    threshold_reasons: list[str] = []
    if (
        minimum_support is not None
        or minimum_planner_support is not None
        or minimum_shape_support is not None
        or minimum_planner_shape_support is not None
    ):
        if args.limit is not None:
            threshold_reasons.append("--limit produces a partial corpus")
        if not args.probe:
            threshold_reasons.append("a threshold requires the compiled Go probe")
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
    report["thresholds"] = {
        "minimum_support": minimum_support,
        "minimum_planner_support": minimum_planner_support,
        "minimum_shape_support": minimum_shape_support,
        "minimum_planner_shape_support": minimum_planner_shape_support,
        "passed": not threshold_reasons,
        "failures": threshold_reasons,
    }
    if export_go is not None:
        _validate_export_state(
            occurrences,
            census_occurrences,
            probe_results,
            expected_sql_sha256=args.expected_sql_sha256,
            commit=args.commit,
        )
        _export_go(
            Path(export_go).resolve(),
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
    if probe_path and not args.probe_dir:
        shutil.rmtree(probe_path, ignore_errors=True)
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
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parent.parent, help="qs repository used to compile the probe")
    parser.add_argument("--report", type=Path, default=Path("docs/postgres-coverage.json"), help="coverage report path")
    parser.add_argument("--commit", help="40-character PostgreSQL commit SHA when the source is not a git checkout")
    parser.add_argument("--expected-sql-sha256", help="fail if the regression SQL tree hash differs from this digest")
    parser.add_argument("--probe-dir", type=Path, help="retain generated Go probes in this directory")
    parser.add_argument(
        "--export-go",
        type=Path,
        help="write the eight-shard native corpus fixture after a complete verified probe",
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
    parser.add_argument("--no-probe", dest="probe", action="store_false", help="only extract and emit builders; do not compile/run Go")
    parser.set_defaults(probe=True)
    args = parser.parse_args(argv)
    if args.limit is not None and args.limit < 1:
        parser.error("--limit must be positive")
    return run(args)


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main())
