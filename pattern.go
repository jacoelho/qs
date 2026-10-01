package qx

import "unicode/utf8"

func Like(column, pattern string) Condition        { return Col(column).Like(pattern) }
func NotLike(column, pattern string) Condition     { return Col(column).NotLike(pattern) }
func ILike(column, pattern string) Condition       { return Col(column).ILike(pattern) }
func NotILike(column, pattern string) Condition    { return Col(column).NotILike(pattern) }
func (e Expr) Like(pattern string) Condition       { return compare(e, "LIKE", Param(pattern)) }
func (e Expr) NotLike(pattern string) Condition    { return compare(e, "NOT LIKE", Param(pattern)) }
func (e Expr) ILike(pattern string) Condition      { return compare(e, "ILIKE", Param(pattern)) }
func (e Expr) NotILike(pattern string) Condition   { return compare(e, "NOT ILIKE", Param(pattern)) }
func (e Expr) LikeExpr(pattern Expr) Condition     { return compare(e, "LIKE", pattern) }
func (e Expr) NotLikeExpr(pattern Expr) Condition  { return compare(e, "NOT LIKE", pattern) }
func (e Expr) ILikeExpr(pattern Expr) Condition    { return compare(e, "ILIKE", pattern) }
func (e Expr) NotILikeExpr(pattern Expr) Condition { return compare(e, "NOT ILIKE", pattern) }
func (e Expr) SimilarTo(pattern Expr) Condition    { return compare(e, "SIMILAR TO", pattern) }
func (e Expr) NotSimilarTo(pattern Expr) Condition { return compare(e, "NOT SIMILAR TO", pattern) }

// Escape applies only to LIKE/ILIKE/SIMILAR TO predicates. An empty escape
// disables escaping; otherwise PostgreSQL requires one character.
func (c Condition) Escape(character string) Condition {
	if !utf8.ValidString(character) || utf8.RuneCountInString(character) > 1 {
		return AsCondition(invalidExpr("ESCAPE", "requires zero or one character"))
	}
	return c.EscapeExpr(Param(character))
}

// EscapeExpr supplies an expression; PostgreSQL validates its character count.
func (c Condition) EscapeExpr(character Expr) Condition {
	if c.expr.kind != exprBinary {
		return AsCondition(invalidExpr("ESCAPE", "requires a pattern predicate"))
	}
	switch c.expr.text {
	case "LIKE", "NOT LIKE", "ILIKE", "NOT ILIKE", "SIMILAR TO", "NOT SIMILAR TO":
	default:
		return AsCondition(invalidExpr("ESCAPE", "requires a pattern predicate"))
	}
	e := c.expr
	return AsCondition(Fragment(UnsafeSQL("("), e.node.left, UnsafeSQL(" "+e.text+" "), e.node.right, UnsafeSQL(" ESCAPE "), character, UnsafeSQL(")")))
}

func (e Expr) Regex(pattern string) Condition               { return compare(e, "~", Param(pattern)) }
func (e Expr) RegexInsensitive(pattern string) Condition    { return compare(e, "~*", Param(pattern)) }
func (e Expr) NotRegex(pattern string) Condition            { return compare(e, "!~", Param(pattern)) }
func (e Expr) NotRegexInsensitive(pattern string) Condition { return compare(e, "!~*", Param(pattern)) }
