package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jacoelho/qs/internal/postgrescorpus"
)

const (
	// SELECT $1
	selectParam = `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"ParamRef":{"number":1}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`
	// SELECT a < b
	selectLess = `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"A_Expr":{"kind":"AEXPR_OP","name":[{"String":{"sval":"<"}}],"lexpr":{"ColumnRef":{"fields":[{"String":{"sval":"a"}}]}},"rexpr":{"ColumnRef":{"fields":[{"String":{"sval":"b"}}]}}}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`
	truncate   = `{"TruncateStmt":{}}`
)

func TestRunAnswersEveryRequestInOrder(t *testing.T) {
	t.Parallel()
	input := `{"id":9,"tree":` + selectParam + "}\n" +
		`{"id":3,"tree":` + truncate + "}\n" +
		`{"id":0,"tree":` + selectLess + "}\n"
	var output strings.Builder
	if err := run(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	want := []response{
		{ID: 9, Outcome: postgrescorpus.OutcomeBuilt, SQL: "SELECT $1", Args: []string{"qs-postgres-corpus-9-param-1"}},
		{ID: 3, Outcome: postgrescorpus.OutcomeOutOfScope, Detail: "TRUNCATE", Args: []string{}},
		{ID: 0, Outcome: postgrescorpus.OutcomeBuilt, SQL: `SELECT ("a" < "b")`, Args: []string{}},
	}
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(lines), len(want), output.String())
	}
	for index, line := range lines {
		var got response
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d %q: %v", index, line, err)
		}
		if !reflect.DeepEqual(got, want[index]) {
			t.Errorf("line %d: got %+v, want %+v", index, got, want[index])
		}
		// The caller reads "args" as a list even when nothing is bound.
		if strings.Contains(line, `"args":null`) {
			t.Errorf("line %d has null args: %s", index, line)
		}
	}
}

func TestRunRejectsBadRequests(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "not_json", input: "SELECT 1\n", want: "request 1"},
		{name: "missing_id", input: `{"tree":` + selectParam + "}\n", want: "request 1: missing id or tree"},
		{name: "missing_tree", input: `{"id":4}` + "\n", want: "request 1: missing id or tree"},
		{name: "unknown_field", input: `{"id":4,"tree":` + selectParam + `,"sql":"x"}` + "\n", want: "request 1"},
		{name: "malformed_tree", input: `{"id":1,"tree":` + selectParam + "}\n" + `{"id":4,"tree":{"SelectStmt":7}}` + "\n", want: "request 2: id=4: malformed parse tree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output strings.Builder
			err := run(strings.NewReader(test.input), &output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v, want %q", err, test.want)
			}
		})
	}
}
