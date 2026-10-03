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
			checkSQL(t, Select(LikePrefix("name", tc.prefix).Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, tc.want)
		})
	}
}

func TestLikePrefixAPIs(t *testing.T) {
	t.Parallel()
	runCases(t, []renderCase{
		{"like_top_level", Select(LikePrefix("name", "jo").Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"jo%"}},
		{"ilike_top_level", Select(ILikePrefix("name", "jo").Expr()), `SELECT ("name" ILIKE $1 ESCAPE E'!')`, []any{"jo%"}},
		{"like_expr", Select(Col("name").LikePrefix("jo").Expr()), `SELECT ("name" LIKE $1 ESCAPE E'!')`, []any{"jo%"}},
		{"ilike_expr", Select(Col("name").ILikePrefix("jo").Expr()), `SELECT ("name" ILIKE $1 ESCAPE E'!')`, []any{"jo%"}},
	})
}

func TestLikePrefixArgumentOrder(t *testing.T) {
	t.Parallel()
	checkSQL(t, Select(Param("left").ILikePrefix("jo").Expr()), `SELECT ($1 ILIKE $2 ESCAPE E'!')`, "left", "jo%")
}
