package qs

import (
	"strings"
	"unicode/utf8"
)

const literalLikeEscape = "!"

type literalLikeMode uint8

const (
	literalLikePrefix literalLikeMode = iota
	literalLikeSuffix
	literalLikeContains
)

// Like compares e with a bound LIKE pattern.
func (e Expr) Like(pattern string) Condition { return compare(e, "LIKE", Param(pattern)) }

// NotLike compares e with a bound pattern using NOT LIKE.
func (e Expr) NotLike(pattern string) Condition { return compare(e, "NOT LIKE", Param(pattern)) }

// ILike compares e with a bound case-insensitive pattern.
func (e Expr) ILike(pattern string) Condition { return compare(e, "ILIKE", Param(pattern)) }

// NotILike compares e with a bound pattern using NOT ILIKE.
func (e Expr) NotILike(pattern string) Condition { return compare(e, "NOT ILIKE", Param(pattern)) }

// LikePrefix compares e with a literal prefix using LIKE. It escapes !, %,
// and _ with the fixed ! escape character and appends %, so an empty prefix
// matches every non-NULL value.
func (e Expr) LikePrefix(prefix string) Condition {
	return e.Like(literalLikePattern(prefix, literalLikePrefix)).EscapeExpr(LiteralString(literalLikeEscape))
}

// ILikePrefix compares e with a literal prefix using ILIKE. It escapes !, %,
// and _ with the fixed ! escape character and appends %, so an empty prefix
// matches every non-NULL value.
func (e Expr) ILikePrefix(prefix string) Condition {
	return e.ILike(literalLikePattern(prefix, literalLikePrefix)).EscapeExpr(LiteralString(literalLikeEscape))
}

// LikeSuffix compares e with a literal suffix using LIKE. It escapes !, %, and _
// with fixed ESCAPE '!'. An empty suffix matches every non-NULL text value.
func (e Expr) LikeSuffix(text string) Condition {
	return e.Like(literalLikePattern(text, literalLikeSuffix)).EscapeExpr(LiteralString(literalLikeEscape))
}

// ILikeSuffix compares e with a literal suffix using ILIKE. It escapes !, %, and _
// with fixed ESCAPE '!'. An empty suffix matches every non-NULL text value.
func (e Expr) ILikeSuffix(text string) Condition {
	return e.ILike(literalLikePattern(text, literalLikeSuffix)).EscapeExpr(LiteralString(literalLikeEscape))
}

// LikeContains compares e with a literal substring using LIKE. It escapes !, %, and _
// with fixed ESCAPE '!'. Empty text matches every non-NULL text value.
func (e Expr) LikeContains(text string) Condition {
	return e.Like(literalLikePattern(text, literalLikeContains)).EscapeExpr(LiteralString(literalLikeEscape))
}

// ILikeContains compares e with a literal substring using ILIKE. It escapes !, %, and _
// with fixed ESCAPE '!'. Empty text matches every non-NULL text value.
func (e Expr) ILikeContains(text string) Condition {
	return e.ILike(literalLikePattern(text, literalLikeContains)).EscapeExpr(LiteralString(literalLikeEscape))
}

// LikeExpr compares e with a SQL expression using LIKE.
func (e Expr) LikeExpr(pattern Expr) Condition { return comparison(e, "LIKE", pattern) }

// NotLikeExpr compares e with a SQL expression using NOT LIKE.
func (e Expr) NotLikeExpr(pattern Expr) Condition { return comparison(e, "NOT LIKE", pattern) }

// ILikeExpr compares e with a SQL expression using ILIKE.
func (e Expr) ILikeExpr(pattern Expr) Condition { return comparison(e, "ILIKE", pattern) }

// NotILikeExpr compares e with a SQL expression using NOT ILIKE.
func (e Expr) NotILikeExpr(pattern Expr) Condition { return comparison(e, "NOT ILIKE", pattern) }

// SimilarTo compares e with a SQL expression using SIMILAR TO.
func (e Expr) SimilarTo(pattern Expr) Condition { return comparison(e, "SIMILAR TO", pattern) }

// NotSimilarTo compares e with a SQL expression using NOT SIMILAR TO.
func (e Expr) NotSimilarTo(pattern Expr) Condition { return comparison(e, "NOT SIMILAR TO", pattern) }

// Escape applies only to LIKE/ILIKE/SIMILAR TO predicates. An empty escape
// disables escaping; otherwise PostgreSQL requires one character.
func (c Condition) Escape(character string) Condition {
	if !utf8.ValidString(character) || utf8.RuneCountInString(character) > 1 {
		return invalidExpr("ESCAPE", "requires zero or one character").Condition()
	}
	return c.EscapeExpr(Param(character))
}

// EscapeExpr supplies an expression; PostgreSQL validates its character count.
func (c Condition) EscapeExpr(character Expr) Condition {
	n, ok := c.expr.node().(*binaryExpression)
	if !ok {
		return invalidExpr("ESCAPE", "requires a pattern predicate").Condition()
	}
	switch n.operator {
	case "LIKE", "NOT LIKE", "ILIKE", "NOT ILIKE", "SIMILAR TO", "NOT SIMILAR TO":
	default:
		return invalidExpr("ESCAPE", "requires a pattern predicate").Condition()
	}
	return enclosed(n.left, UnsafeSQL(" "+n.operator+" "), n.right, UnsafeSQL(" ESCAPE "), character).Condition()
}

func literalLikePattern(text string, mode literalLikeMode) string {
	var pattern strings.Builder
	wildcardCount := 1
	if mode == literalLikeContains {
		wildcardCount = 2
	}
	pattern.Grow(len(text) + wildcardCount)
	if mode != literalLikePrefix {
		pattern.WriteByte('%')
	}
	for i := range len(text) {
		switch text[i] {
		case '!', '%', '_':
			pattern.WriteString(literalLikeEscape)
		}
		pattern.WriteByte(text[i])
	}
	if mode != literalLikeSuffix {
		pattern.WriteByte('%')
	}
	return pattern.String()
}

// Regex compares e with a bound POSIX regular expression.
func (e Expr) Regex(pattern string) Condition { return compare(e, "~", Param(pattern)) }

// RegexInsensitive compares e with a bound case-insensitive regular expression.
func (e Expr) RegexInsensitive(pattern string) Condition { return compare(e, "~*", Param(pattern)) }

// NotRegex compares e with a bound regular expression using the negated match.
func (e Expr) NotRegex(pattern string) Condition { return compare(e, "!~", Param(pattern)) }

// NotRegexInsensitive compares e with a bound case-insensitive regular
// expression using the negated match.
func (e Expr) NotRegexInsensitive(pattern string) Condition { return compare(e, "!~*", Param(pattern)) }
