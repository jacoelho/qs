package qs

import (
	"strings"
	"testing"
)

// Trusted SQL renders verbatim when every "$" in it is inside a token that
// PostgreSQL does not read as a parameter.
func TestUnsafeSQLRendersTextWithoutPlaceholders(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		`a OR b`,
		`price = '$1'`,
		`note = 'it''s $1'`,
		`E'a\'$1'`,
		`path = 'C:\' AND x`,
		`"col$1" = "weird ""$2"""`,
		`t$1.a = tab$.b`,
		`body = $$ select $1 $$`,
		`body = $fn$ select $1 $$ still $fn$`,
		`a /* $1 /* nested $2 */ $3 */ = b`,
		"a -- $1\n = b",
		`a - b / c`,
		`U&'d\0061t\+000061 $1'`,
		`名字 = 'x'`,
	} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, Select(UnsafeSQL(text), Param(9)), "SELECT "+text+", $1", 9)
		})
	}
}

// A placeholder in trusted SQL would share a slot with a bound value, and an
// incomplete token would swallow what qs renders after it.
func TestUnsafeSQLRejectsPlaceholdersAndIncompleteTokens(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"placeholder":                 `x = $1`,
		"placeholder after number":    `x = 1$1`,
		"placeholder after operator":  `x=$12`,
		"placeholder after string":    `'a'$1`,
		"placeholder after E-string":  `E'a\\'$1`,
		"placeholder after comment":   `/* c */$1`,
		"placeholder after dollar":    `$$a$$ $1`,
		"trailing dollar":             `x = $`,
		"stray dollar":                `x = $ 1`,
		"unterminated string":         `x = 'abc`,
		"unterminated E-string":       `x = E'abc\'`,
		"unterminated identifier":     `"abc`,
		"unterminated block comment":  `x /* abc`,
		"unterminated nested comment": `x /* a /* b */`,
		"unterminated line comment":   `x -- abc`,
		"unterminated dollar quote":   `$fn$ abc $$`,
		"empty":                       ``,
		"blank":                       " \n\t",
		"NUL":                         "a\x00b",
		"invalid UTF-8":               "a\xffb",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, Select(UnsafeSQL(text), Param(9)), ErrInvalid)
		})
	}
}

// Each part is complete on its own, and two complete parts must not be read
// as one token where they meet: that would hide a clause, renumber a
// parameter or change a value without any error.
func TestFragmentRejectsPartsThatJoinIntoOneToken(t *testing.T) {
	t.Parallel()
	for name, fragment := range map[string]Expr{
		"open placeholder":            Fragment(UnsafeSQL("x = $"), LiteralInt(1)),
		"open string":                 Fragment(UnsafeSQL("x = '"), Param(1), UnsafeSQL("'")),
		"line comment from two signs": Fragment(UnsafeSQL("a > b -"), LiteralInt(-1)),
		"block comment":               Fragment(UnsafeSQL("a /"), UnsafeSQL("* b")),
		"parameter grows a digit":     Fragment(UnsafeSQL("x = "), Param(1), UnsafeSQL("0")),
		"number grows a digit":        Fragment(LiteralInt(1), UnsafeSQL("0")),
		"name swallows parameter":     Fragment(UnsafeSQL("x = t"), Param(1)),
		"name opens dollar quote":     Fragment(UnsafeSQL("x"), UnsafeSQL("$$ a $$")),
		"strings merge":               Fragment(LiteralString("a"), UnsafeSQL("'b'")),
		"identifiers merge":           Fragment(Col("x"), UnsafeSQL(`"y" > 0`)),
		"names merge":                 Fragment(UnsafeSQL("interval"), UnsafeSQL("day")),
		"name becomes string prefix":  Fragment(UnsafeSQL("e"), UnsafeSQL(`'a\'`)),
		"unicode string prefix":       Fragment(UnsafeSQL("U&"), UnsafeSQL(`'a'`)),
		"across nested fragments": Fragment(
			Fragment(UnsafeSQL("x = "), Param(1)),
			UnsafeSQL("0"),
		),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, Select(fragment, Param(9)).From("t").OrderBy(Col("t", "id").Asc()), ErrInvalid)
		})
	}
}

// A space, or punctuation that ends a token, keeps neighbours apart.
func TestFragmentRendersSeparatedParts(t *testing.T) {
	t.Parallel()
	runCases(t, []renderCase{
		{
			"value_between_text",
			Select(Fragment(UnsafeSQL("similarity(name, "), Param("ana"), UnsafeSQL(") > 0.3"))),
			`SELECT similarity(name, $1) > 0.3`,
			[]any{"ana"},
		},
		{"negative_after_spaced_minus", Select(Fragment(UnsafeSQL("a - "), LiteralInt(-1))),
			`SELECT a - -1`, nil},
		{"keyword_before_string", Select(Fragment(UnsafeSQL("interval "), LiteralString("1 day"))),
			`SELECT interval E'1 day'`, nil},
		{
			"call_syntax",
			Select(Fragment(UnsafeSQL("ARRAY"), UnsafeSQL("[1, 2]"), UnsafeSQL("::int[]"))),
			`SELECT ARRAY[1, 2]::int[]`,
			nil,
		},
		{"parameter_then_cast", Select(Fragment(Param(1), UnsafeSQL("::int"))),
			`SELECT $1::int`, []any{1}},
	})
}

func FuzzUnsafeSQL(f *testing.F) {
	for _, text := range []string{"a = b", "x = $1", "'$1'", "$$ $1 $$", "$a$ $1 $a$", "/* /* */ $1", "E'\\'' $1", `"a""$1"`, "t$1", "1$1", "-- $1\n$2", "$"} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 4096 {
			t.Skip()
		}
		sql, args, err := Select(UnsafeSQL(text), Param(9)).ToSQL()
		if err != nil {
			if sql != "" || args != nil {
				t.Fatal("failed render returned output")
			}
			return
		}
		if sql != "SELECT "+text+", $1" || len(args) != 1 {
			t.Fatalf("accepted text is not verbatim: %q", sql)
		}
		// Without any quoting or comment syntax, every "$digit" that does not
		// continue an identifier is a placeholder PostgreSQL would number.
		if strings.ContainsAny(text, `'"-/`) || strings.Contains(text, "$$") {
			return
		}
		for i := 0; i+1 < len(text); i++ {
			if text[i] == '$' && identifierStart(text[i+1]) {
				return // may open a tagged dollar quote
			}
		}
		for i := 0; i+1 < len(text); i++ {
			if text[i] != '$' || text[i+1] < '0' || text[i+1] > '9' {
				continue
			}
			if i == 0 || !identifierStart(text[i-1]) && text[i-1] != '$' && (text[i-1] < '0' || text[i-1] > '9') {
				t.Fatalf("placeholder accepted in %q", text)
			}
		}
	})
}
