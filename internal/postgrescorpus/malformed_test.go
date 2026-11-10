package postgrescorpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// mutationStride samples the corpus; every tree shape family still appears.
const mutationStride = 257

// replacements are the JSON kinds a field could wrongly hold.
var replacements = []any{nil, true, json.Number("7"), "x", []any{}, map[string]any{}, []any{map[string]any{"String": map[string]any{}}}}

// mutate calls visit once per replacement of every value in tree, restoring
// the value afterwards.
func mutate(tree any, visit func()) {
	var walk func(value any)
	replace := func(set func(any), original any) {
		for _, replacement := range replacements {
			set(replacement)
			visit()
		}
		set(original)
		walk(original)
	}
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				replace(func(v any) { typed[key] = v }, child)
			}
		case []any:
			for index, child := range typed {
				replace(func(v any) { typed[index] = v }, child)
			}
		}
	}
	walk(tree)
}

// A fixture edited by hand or produced by another parser version must come
// back as an error or an outcome, never a panic.
func TestRenderSurvivesMalformedTrees(t *testing.T) {
	t.Parallel()
	manifest := pinnedManifest(t)
	for _, file := range manifest.Files {
		t.Run(file.Path, func(t *testing.T) {
			t.Parallel()
			reader, err := os.Open(file.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			mutations, refused := 0, 0
			for item, err := range Cases(reader, file.FirstID, file.Count) {
				if err != nil {
					t.Fatal(err)
				}
				if item.ID%mutationStride != 0 {
					continue
				}
				decoder := json.NewDecoder(bytes.NewReader(item.Tree))
				decoder.UseNumber()
				var tree any
				if err := decoder.Decode(&tree); err != nil {
					t.Fatal(err)
				}
				mutate(tree, func() {
					data, err := json.Marshal(tree)
					if err != nil {
						t.Fatal(err)
					}
					refusal, failure := renderPanic(item.ID, data)
					if failure != "" {
						t.Fatalf("id=%d tree=%s: %s", item.ID, data, failure)
					}
					mutations++
					if refusal {
						refused++
					}
				})
			}
			// Guards against a walk that stops mutating anything.
			if mutations == 0 || refused == 0 {
				t.Fatalf("%d mutations, %d refused as malformed", mutations, refused)
			}
			t.Logf("%d mutations, %d refused as malformed", mutations, refused)
		})
	}
}

func renderPanic(id int, tree []byte) (refused bool, failure string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failure = fmt.Sprintf("panic: %v", recovered)
		}
	}()
	_, err := Render(id, tree)
	return err != nil, ""
}
