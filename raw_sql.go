package qs

import (
	"strings"
	"unicode/utf8"
)

// rawSQLProblem reports why trusted SQL cannot be rendered verbatim, or "".
//
// The renderer owns parameter numbering, so a "$1" written in trusted SQL would
// silently share a slot with a bound value. Each trusted part must also be
// lexically complete: an open quote or comment would swallow whatever qs
// renders after it, including a parameter. Whether a complete part joins its
// neighbour into one token is tokensJoin's question, asked where parts meet.
// The scan follows PostgreSQL's lexer with standard_conforming_strings on, the
// default since 9.1.
func rawSQLProblem(sql string) string {
	if strings.TrimSpace(sql) == "" || strings.IndexByte(sql, 0) >= 0 || !utf8.ValidString(sql) {
		return "empty, NUL-containing or invalid UTF-8 raw SQL"
	}
	if !strings.ContainsAny(sql, `$'"-/`) {
		return ""
	}
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '\'':
			end, ok := skipQuoted(sql, i, '\'', false)
			if !ok {
				return "unterminated string in raw SQL"
			}
			i = end
		case c == '"':
			end, ok := skipQuoted(sql, i, '"', false)
			if !ok {
				return "unterminated quoted identifier in raw SQL"
			}
			i = end
		case c == '-' && strings.HasPrefix(sql[i:], "--"):
			newline := strings.IndexAny(sql[i:], "\n\r")
			if newline < 0 {
				return "raw SQL ends inside a -- comment, which would hide what follows it"
			}
			i += newline + 1
		case c == '/' && strings.HasPrefix(sql[i:], "/*"):
			end, ok := skipBlockComment(sql, i)
			if !ok {
				return "unterminated /* comment in raw SQL"
			}
			i = end
		case c == '$':
			end, problem := skipDollar(sql, i)
			if problem != "" {
				return problem
			}
			i = end
		case identifierStart(c):
			start := i
			for i < len(sql) && identifierPart(sql[i]) {
				i++
			}
			// E'...' is the one string form with backslash escapes.
			if i < len(sql) && sql[i] == '\'' && i-start == 1 && (c == 'e' || c == 'E') {
				end, ok := skipQuoted(sql, i, '\'', true)
				if !ok {
					return "unterminated string in raw SQL"
				}
				i = end
			}
		case c >= '0' && c <= '9':
			// A number is not an identifier: "1$1" is 1 followed by $1.
			for i < len(sql) && (sql[i] >= '0' && sql[i] <= '9') {
				i++
			}
		default:
			i++
		}
	}
	return ""
}

// skipQuoted returns the index after the quoted token opening at start. A
// doubled quote is an escaped quote; a backslash escapes only in an E-string.
func skipQuoted(sql string, start int, quote byte, backslash bool) (int, bool) {
	for i := start + 1; i < len(sql); i++ {
		switch {
		case backslash && sql[i] == '\\':
			i++
		case sql[i] != quote:
		case i+1 < len(sql) && sql[i+1] == quote:
			i++
		default:
			return i + 1, true
		}
	}
	return 0, false
}

// skipBlockComment returns the index after the comment opening at start.
// PostgreSQL block comments nest.
func skipBlockComment(sql string, start int) (int, bool) {
	depth := 0
	for i := start; i+1 < len(sql); i++ {
		switch sql[i : i+2] {
		case "/*":
			depth++
			i++
		case "*/":
			depth--
			i++
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// skipDollar handles a "$" that does not continue an identifier: a placeholder,
// which is rejected, or the opening tag of a dollar-quoted string, whose body
// is skipped. Anything else is not SQL on its own.
func skipDollar(sql string, start int) (int, string) {
	i := start + 1
	if i < len(sql) && sql[i] >= '0' && sql[i] <= '9' {
		return 0, "raw SQL contains a numbered placeholder; qs numbers parameters itself, so pass the value as Param inside Fragment"
	}
	if i < len(sql) && identifierStart(sql[i]) {
		for i < len(sql) && identifierPart(sql[i]) && sql[i] != '$' {
			i++
		}
	}
	if i >= len(sql) || sql[i] != '$' {
		return 0, "raw SQL contains a $ that is neither part of an identifier nor a dollar-quote tag"
	}
	tag := sql[start : i+1]
	body := strings.Index(sql[i+1:], tag)
	if body < 0 {
		return 0, "unterminated dollar-quoted string in raw SQL"
	}
	return i + 1 + body + len(tag), ""
}

func identifierStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= utf8.RuneSelf
}

func identifierPart(c byte) bool {
	return identifierStart(c) || c == '$' || (c >= '0' && c <= '9')
}

// tokensJoin reports whether a part ending in prev and a part starting with
// next would be read as one token, or open one, when rendered side by side:
//
//   - a name, number or parameter running into a name, number or parameter,
//     as in t and $1, $1 and 0, or 1 and 0;
//   - a name running into a string, which may turn it into an E, B, X, N or
//     U& string;
//   - a closing quote meeting an opening quote of its kind, which is an
//     escaped quote;
//   - the halves of -- and /*.
func tokensJoin(prev, next byte) bool {
	switch {
	case identifierPart(prev):
		return identifierPart(next) || next == '\''
	case prev == '&':
		return next == '\'' || next == '"'
	case prev == '\'' || prev == '"' || prev == '-':
		return next == prev
	case prev == '/':
		return next == '*'
	default:
		return false
	}
}
