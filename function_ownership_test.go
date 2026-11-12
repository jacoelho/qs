package qs

import "testing"

func TestFunctionVariadicArgumentsOwnInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		build func([]Expr) Expr
		want  string
		args  []any
	}{
		{
			name:  "Lag",
			build: func(values []Expr) Expr { return Lag(Param("value"), values...) },
			want:  `SELECT lag($1, $2, $3)`,
			args:  []any{"value", "second", "third"},
		},
		{
			name:  "Lead",
			build: func(values []Expr) Expr { return Lead(Param("value"), values...) },
			want:  `SELECT lead($1, $2, $3)`,
			args:  []any{"value", "second", "third"},
		},
		{
			name:  "Coalesce",
			build: func(values []Expr) Expr { return Coalesce(Param("first"), values...) },
			want:  `SELECT coalesce($1, $2, $3)`,
			args:  []any{"first", "second", "third"},
		},
		{
			name:  "Greatest",
			build: func(values []Expr) Expr { return Greatest(Param("first"), values...) },
			want:  `SELECT greatest($1, $2, $3)`,
			args:  []any{"first", "second", "third"},
		},
		{
			name:  "Least",
			build: func(values []Expr) Expr { return Least(Param("first"), values...) },
			want:  `SELECT least($1, $2, $3)`,
			args:  []any{"first", "second", "third"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := []Expr{Param("second"), Param("third")}
			expr := tc.build(values)
			values[0] = Param("changed")
			checkSQL(t, Select(expr), tc.want, tc.args...)
		})
	}
}

func TestFunctionOptionalArgumentsOwnInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		build func([]Expr) Expr
		want  string
		args  []any
	}{
		{
			name:  "Substring",
			build: func(values []Expr) Expr { return Substring(Param("value"), Param(1), values...) },
			want:  `SELECT substr($1, $2, $3)`,
			args:  []any{"value", 1, 3},
		},
		{
			name:  "Round",
			build: func(values []Expr) Expr { return Round(Param(1.5), values...) },
			want:  `SELECT round($1, $2)`,
			args:  []any{1.5, 2},
		},
		{
			name: "DateTrunc",
			build: func(values []Expr) Expr {
				return DateTrunc(
					Param("hour"),
					Param("value"),
					values...,
				)
			},
			want: `SELECT date_trunc($1, $2, $3)`,
			args: []any{"hour", "value", "UTC"},
		},
		{
			name:  "Age",
			build: func(values []Expr) Expr { return Age(Param("left"), values...) },
			want:  `SELECT age($1, $2)`,
			args:  []any{"left", "right"},
		},
		{
			name:  "GenerateSeries",
			build: func(values []Expr) Expr { return GenerateSeries(Param(1), Param(3), values...) },
			want:  `SELECT generate_series($1, $2, $3)`,
			args:  []any{1, 3, 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := []Expr{Param(tc.args[len(tc.args)-1])}
			expr := tc.build(values)
			values[0] = Param("changed")
			checkSQL(t, Select(expr), tc.want, tc.args...)
		})
	}
}

func TestFunctionArgumentsKeepTheQueryTheyWereGiven(t *testing.T) {
	t.Parallel()

	child := Select(Param("child"))
	expr := Coalesce(Scalar(child), Param("fallback"))
	filtered := Coalesce(Scalar(child.Where(True())), Param("fallback"))
	checkSQL(t, Select(expr), `SELECT coalesce((SELECT $1), $2)`, "child", "fallback")
	checkSQL(t, Select(filtered), `SELECT coalesce((SELECT $1 WHERE TRUE), $2)`, "child", "fallback")
}
