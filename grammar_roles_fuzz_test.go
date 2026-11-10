package qs

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// These generated goldens use question marks only for parameters.
func rolePlaceholders(golden string, prefix int) string {
	parts := strings.Split(golden, "?")
	var result strings.Builder
	for i, part := range parts {
		if i > 0 {
			fmt.Fprintf(&result, "$%d", prefix+i)
		}
		result.WriteString(part)
	}
	return result.String()
}

func checkRoleFuzz(t *testing.T, statement Statement, valid bool, golden string, expected []any, input []byte) {
	t.Helper()
	want, appended := rolePlaceholders(golden, 0), rolePlaceholders(golden, 1)
	sql, args, err := statement.ToSQL()
	if valid {
		if err != nil || sql != want || !reflect.DeepEqual(args, expected) {
			t.Fatalf("input=%x SQL=%q args=%#v err=%v; want=%q %#v", input, sql, args, err, want, expected)
		}
	} else if !errors.Is(err, ErrInvalid) || sql != "" || args != nil {
		t.Fatalf("input=%x invalid: SQL=%q args=%#v err=%v", input, sql, args, err)
	}
	prefix := append(make([]byte, 0, 8192), "prefix "...)
	spare := 0
	if len(input) > 0 {
		spare = int(input[0] % 5)
	}
	storage := make([]any, 1+spare)
	storage[0] = "prefix"
	binds := storage[:1]
	out, values, err := statement.AppendSQL(prefix, binds)
	if valid {
		wantArgs := append([]any{"prefix"}, expected...)
		if err != nil || string(out) != "prefix "+appended || !reflect.DeepEqual(values, wantArgs) {
			t.Fatalf("input=%x append SQL=%q args=%#v err=%v; want=%q %#v", input, out, values, err, "prefix "+appended, wantArgs)
		}
		return
	}
	if !errors.Is(err, ErrInvalid) || string(out) != "prefix " || !reflect.DeepEqual(values, []any{"prefix"}) || string(prefix) != "prefix " || binds[0] != "prefix" {
		t.Fatalf("input=%x rollback SQL=%q args=%#v err=%v", input, out, values, err)
	}
	for i, value := range storage[1:] {
		if value != nil {
			t.Fatalf("input=%x appended slot %d retained %T", input, i+1, value)
		}
	}
}

func FuzzCaseRoles(f *testing.F) {
	for _, seed := range [][]byte{nil, {0}, {1, 0}, {0, 0, 1, 1}, {0, 128, 0}, {1, 1, 1}} {
		f.Add(false, seed)
		f.Add(true, seed)
	}
	f.Fuzz(func(t *testing.T, simple bool, steps []byte) {
		if len(steps) > 16 {
			t.Skip()
		}
		// A CASE has no builder until its first branch, so an ELSE before
		// that branch cannot be written and the step is dropped.
		var searched SearchedCaseBuilder
		var scalar SimpleCaseBuilder
		golden := "SELECT CASE"
		var args []any
		if simple {
			golden += " ?"
			args = append(args, 17)
		}
		branches, valid := 0, true
		elseValue, hasElse := 0, false
		for i, step := range steps {
			value := i + 101
			if step&1 != 0 {
				if branches == 0 {
					continue
				}
				searched = searched.Else(Param(value))
				scalar = scalar.Else(Param(value))
				elseValue, hasElse = value, true
				continue
			}
			condition, operand := Col("v").Eq(Param(value)), Param(value)
			if step&128 != 0 {
				condition, operand = Condition{}, Expr{}
				valid = false
			}
			if branches == 0 {
				searched = Case().When(condition, Param(value+50))
				scalar = CaseOf(Param(17)).WhenValue(operand, Param(value+50))
			} else {
				searched = searched.When(condition, Param(value+50))
				scalar = scalar.WhenValue(operand, Param(value+50))
			}
			branches++
			if simple {
				golden += " WHEN ? THEN ?"
			} else {
				golden += ` WHEN ("v" = ?) THEN ?`
			}
			args = append(args, value, value+50)
		}
		if hasElse {
			golden += " ELSE ?"
			args = append(args, elseValue)
		}
		golden += " END"
		captured := searched.End()
		if simple {
			captured = scalar.End()
		}
		// Extending a builder after End must not reach the captured expression.
		_ = searched.When(True(), Param(999)).Else(Param(998)).End()
		_ = scalar.WhenValue(Param(997), Param(996)).Else(Param(995)).End()
		// With no branch there is no builder that Case or CaseOf returned; the
		// zero builders stand in and must fail.
		checkRoleFuzz(t, Select(captured), valid && branches > 0, golden, args, steps)
	})
}

func FuzzMergeRoles(f *testing.F) {
	for _, seed := range [][]byte{nil, {0}, {1}, {2}, {3}, {4}, {5}, {6}, {7}, {8}, {9}, {10}, {11}, {0, 0}, {16, 0}, {0, 1, 3}, {18, 6}, {193}, {129}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, steps []byte) {
		if len(steps) > 16 {
			t.Skip()
		}
		golden := `MERGE INTO "t" USING "s" ON TRUE`
		var args []any
		branches := make([]MergeWhen, 0, len(steps))
		closed := [4]bool{}
		valid := len(steps) > 0
		for i, step := range steps {
			category := step % 4
			canonical := category
			if category == 2 {
				canonical = 1
			}
			if closed[canonical] {
				valid = false
			}
			conditional := step&16 != 0
			if !conditional {
				closed[canonical] = true
			}
			labels := []string{"MATCHED", "NOT MATCHED", "NOT MATCHED BY TARGET", "NOT MATCHED BY SOURCE"}
			golden += " WHEN " + labels[category]
			value := 101 + i
			matched, missing := Matched(), NotMatched()
			if category == 3 {
				matched = NotMatchedBySource()
			}
			if category == 2 {
				missing = NotMatchedByTarget()
			}
			if conditional {
				matched = matched.And(Col("p").Eq(Param(value)))
				missing = missing.And(Col("p").Eq(Param(value)))
				golden += ` AND ("p" = ?)`
				args = append(args, value)
			}
			golden += " THEN "
			var branch MergeWhen
			if category == 0 || category == 3 {
				switch (step >> 2) % 3 {
				case 0:
					branch = matched.ThenUpdate(Set("v", Write(Param(value+50))))
					golden += `UPDATE SET "v" = ?`
					args = append(args, value+50)
				case 1:
					branch = matched.ThenDelete()
					golden += "DELETE"
				case 2:
					branch = matched.ThenDoNothing()
					golden += "DO NOTHING"
				}
			} else {
				action := (step >> 2) % 4
				switch action {
				case 0, 1:
					golden += `INSERT ("v")`
					override := (step >> 6) % 3
					switch override {
					case 1:
						selector := missing.OverridingSystemValue()
						golden += " OVERRIDING SYSTEM VALUE"
						if action == 0 {
							branch = selector.ThenInsert(Set("v", Write(Param(value+50))))
						} else {
							branch = selector.ThenInsertValues([]string{"v"}, Write(Param(value+50)))
						}
					case 2:
						selector := missing.OverridingUserValue()
						golden += " OVERRIDING USER VALUE"
						if action == 0 {
							branch = selector.ThenInsert(Set("v", Write(Param(value+50))))
						} else {
							branch = selector.ThenInsertValues([]string{"v"}, Write(Param(value+50)))
						}
					default:
						if action == 0 {
							branch = missing.ThenInsert(Set("v", Write(Param(value+50))))
						} else {
							branch = missing.ThenInsertValues([]string{"v"}, Write(Param(value+50)))
						}
					}
					golden += " VALUES (?)"
					args = append(args, value+50)
				case 2:
					branch = missing.ThenInsertDefault()
					golden += "INSERT DEFAULT VALUES"
				case 3:
					branch = missing.ThenDoNothing()
					golden += "DO NOTHING"
				}
			}
			branches = append(branches, branch)
		}
		checkRoleFuzz(t, MergeInto("t").Using(Table("s")).On(True()).When(branches...), valid, golden, args, steps)
	})
}

func FuzzJoinRoles(f *testing.F) {
	for kind := range byte(9) {
		f.Add([]byte{kind, kind + 16, kind + 32, kind + 64})
	}
	f.Add([]byte{})
	f.Add([]byte{128})
	f.Add([]byte{0, 8, 128})
	f.Fuzz(func(t *testing.T, steps []byte) {
		if len(steps) > 16 {
			t.Skip()
		}
		relation := Table("t0")
		golden := `"t0"`
		var args []any
		valid := true
		regular := []func(Relation, Relation) PendingJoin{InnerJoin, LeftJoin, RightJoin, FullJoin}
		complete := []func(Relation, Relation) Relation{CrossJoin, NaturalJoin, NaturalLeftJoin, NaturalRightJoin, NaturalFullJoin}
		labels := []string{" JOIN ", " LEFT JOIN ", " RIGHT JOIN ", " FULL JOIN ", " CROSS JOIN ", " NATURAL JOIN ", " NATURAL LEFT JOIN ", " NATURAL RIGHT JOIN ", " NATURAL FULL JOIN "}
		for i, step := range steps {
			kind := step % 9
			name := fmt.Sprintf("t%d", i+1)
			right := Table(name)
			golden = "(" + golden + labels[kind] + `"` + name + `"`
			if kind < 4 {
				pending := regular[kind](relation, right)
				switch (step >> 4) % 3 {
				case 0:
					relation = pending.On(Col("v").Eq(Param(101 + i)))
					golden += ` ON ("v" = ?)`
					args = append(args, 101+i)
				case 1:
					relation = pending.Using("id", "tenant")
					golden += ` USING ("id", "tenant")`
				case 2:
					relation = pending.UsingAs("u", "id", "tenant")
					golden += ` USING ("id", "tenant") AS "u"`
				}
			} else {
				relation = complete[kind-4](relation, right)
			}
			golden += ")"
			if step&64 != 0 {
				relation = relation.As("j")
				golden += ` AS "j"`
			}
			if step&128 != 0 {
				relation = relation.As("bad\x00alias")
				valid = false
			}
		}
		checkRoleFuzz(t, Select(Star()).FromExpr(relation), valid, "SELECT * FROM "+golden, args, steps)
	})
}
