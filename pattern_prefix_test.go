package qs

import "testing"

func TestLikePrefixPattern(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		prefix string
		want   string
	}{
		{"plain", "jo", "jo%"},
		{"percent", "a%", "a!%%"},
		{"underscore", "a_", "a!_%"},
		{"escape", "a!", "a!!%"},
		{"backslash_literal", `a\b`, `a\b%`},
		{"repeated_metacharacters", "!!%%__", "!!!!!%!%!_!_%"},
		{"unicode", "Jöhn", "Jöhn%"},
		{"empty", "", "%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, Select(Col("name").LikePrefix(tc.prefix).Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, tc.want)
		})
	}
}

func TestLikePrefixAPIs(t *testing.T) {
	t.Parallel()
	runCases(t, []renderCase{
		{"like_top_level", Select(Col("name").LikePrefix("jo").Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"jo%"}},
		{"ilike_top_level", Select(Col("name").ILikePrefix("jo").Expr()), `SELECT ("name" ILIKE $1 ESCAPE E'!')`, []any{"jo%"}},
		{"like_expr", Select(Col("name").LikePrefix("jo").Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"jo%"}},
		{"ilike_expr", Select(Col("name").ILikePrefix("jo").Expr()), `SELECT ("name" ILIKE $1 ESCAPE E'!')`, []any{"jo%"}},
	})
}

func TestLikePrefixArgumentOrder(t *testing.T) {
	t.Parallel()
	checkSQL(t, Select(Param("left").ILikePrefix("jo").Expr()), `SELECT ($1 ILIKE $2 ESCAPE E'!')`, "left", "jo%")
}

func TestLikeSuffixAndContainsAPIs(t *testing.T) {
	t.Parallel()
	const text = "a!%_"
	runCases(t, []renderCase{
		{"like_suffix_top_level", Select(Col("name").LikeSuffix(text).Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"%a!!!%!_"}},
		{"ilike_suffix_top_level", Select(Col("name").ILikeSuffix(text).Expr()), `SELECT ("name" ILIKE $1 ESCAPE E'!')`, []any{"%a!!!%!_"}},
		{"like_suffix_expr", Select(Col("name").LikeSuffix(text).Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"%a!!!%!_"}},
		{"ilike_suffix_expr", Select(Col("name").ILikeSuffix(text).Expr()), `SELECT ("name" ILIKE $1 ESCAPE E'!')`, []any{"%a!!!%!_"}},
		{"like_contains_top_level", Select(Col("name").LikeContains(text).Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"%a!!!%!_%"}},
		{"ilike_contains_top_level", Select(Col("name").ILikeContains(text).Expr()), `SELECT ("name" ILIKE $1 ESCAPE E'!')`, []any{"%a!!!%!_%"}},
		{"like_contains_expr", Select(Col("name").LikeContains(text).Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"%a!!!%!_%"}},
		{"ilike_contains_expr", Select(Col("name").ILikeContains(text).Expr()), `SELECT ("name" ILIKE $1 ESCAPE E'!')`, []any{"%a!!!%!_%"}},
	})
}

func TestLikeSuffixAndContainsEmpty(t *testing.T) {
	t.Parallel()
	runCases(t, []renderCase{
		{"empty_suffix", Select(Col("name").LikeSuffix("").Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"%"}},
		{"empty_contains", Select(Col("name").LikeContains("").Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"%%"}},
	})
}

func TestLikeContainsNotArgumentOrder(t *testing.T) {
	t.Parallel()
	checkSQL(t, Select(Param("left").ILikeContains("a!%_").Not().Expr()), `SELECT (NOT ($1 ILIKE $2 ESCAPE E'!'))`, "left", "%a!!!%!_%")
}

func TestLiteralLikeRejectsEscapeOverride(t *testing.T) {
	t.Parallel()
	checkError(t, Select(Col("name").LikeContains("value").Escape("").Expr()), ErrInvalid)
}
