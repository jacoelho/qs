package postgrescorpus

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	qs "github.com/jacoelho/qs"
)

// Corpus statuses retain both verified queries and explicit coverage gaps.
const (
	StatusVerified        = "verified"
	StatusConstructionErr = "construction_error"
	StatusUnsupported     = "unsupported"
	ShardCount            = 8
	DefaultGroupSize      = 256
)

// Case holds one occurrence's frozen SQL, arguments and source provenance.
type Case struct {
	Source            string   `json:"source"`
	Origin            string   `json:"origin"`
	Wrapper           string   `json:"wrapper"`
	Statement         string   `json:"statement"`
	ExpectedOutput    string   `json:"expected_output"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason"`
	Shape             string   `json:"shape"`
	OriginalSQL       string   `json:"original_sql"`
	WantSQL           string   `json:"want_sql"`
	Families          []string `json:"families"`
	Normalization     []string `json:"normalization"`
	WantArgs          []string `json:"want_args"`
	ID                int      `json:"id"`
	Line              int      `json:"line"`
	Planner           bool     `json:"planner"`
	ExpectedErrorHint bool     `json:"expected_error_hint"`
}

// Factory explicitly associates a generated public-constructor builder with
// an occurrence and its frozen coverage status.
type Factory struct {
	Build  func() qs.Statement
	Status string
	ID     int
}

// ShardSpec supplies the complete ordered inventory for one JSONL stream.
type ShardSpec struct {
	Name        string
	ExpectedIDs []int
	Index       int
	GroupSize   int
}

// StreamShard decodes bounded groups into fresh Case values. A nil factories
// slice is reserved for the metadata audit; shard tests supply every factory.
// The consumer must finish each group before returning.
func StreamShard(reader io.Reader, spec ShardSpec, factories []Factory, consume func(int, []Case) error) error {
	if err := validateSpec(spec, factories); err != nil {
		return err
	}
	groupSize := spec.GroupSize
	if groupSize == 0 {
		groupSize = DefaultGroupSize
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	group := make([]Case, 0, groupSize)
	record, groupIndex := 0, 0
	flush := func() error {
		if len(group) == 0 {
			return nil
		}
		if consume == nil {
			return fmt.Errorf("%s: nil group consumer", spec.Name)
		}
		if err := consume(groupIndex, group); err != nil {
			return fmt.Errorf("%s group %d: %w", spec.Name, groupIndex, err)
		}
		groupIndex++
		group = make([]Case, 0, groupSize)
		return nil
	}
	for {
		// Decode must not reuse slice storage retained by parallel children.
		var item Case
		err := decoder.Decode(&item)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%s record %d: decode: %w", spec.Name, record, err)
		}
		if record >= len(spec.ExpectedIDs) {
			return fmt.Errorf("%s record %d id=%d: extra record", spec.Name, record, item.ID)
		}
		if item.ID != spec.ExpectedIDs[record] {
			return fmt.Errorf("%s record %d id=%d: expected shard ID %d", spec.Name, record, item.ID, spec.ExpectedIDs[record])
		}
		if err := validateCase(item); err != nil {
			return fmt.Errorf("%s record %d id=%d: %w", spec.Name, record, item.ID, err)
		}
		if factories != nil {
			if err := validateFactory(item, factories[record]); err != nil {
				return fmt.Errorf("%s record %d id=%d: %w", spec.Name, record, item.ID, err)
			}
		}
		group = append(group, item)
		record++
		if len(group) == groupSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if record != len(spec.ExpectedIDs) {
		return fmt.Errorf("%s: clean EOF after %d records, want %d", spec.Name, record, len(spec.ExpectedIDs))
	}
	return flush()
}

func validateSpec(spec ShardSpec, factories []Factory) error {
	if spec.Name == "" || spec.Index < 0 || spec.Index >= ShardCount {
		return fmt.Errorf("invalid corpus shard %q index %d", spec.Name, spec.Index)
	}
	if spec.GroupSize < 0 || spec.GroupSize > DefaultGroupSize {
		return fmt.Errorf("%s: group size %d is outside 0..%d", spec.Name, spec.GroupSize, DefaultGroupSize)
	}
	for index, id := range spec.ExpectedIDs {
		if id != spec.Index+index*ShardCount {
			return fmt.Errorf("%s: invalid expected ID %d at position %d", spec.Name, id, index)
		}
	}
	if factories != nil && len(factories) != len(spec.ExpectedIDs) {
		return fmt.Errorf("%s: factory count %d, want %d", spec.Name, len(factories), len(spec.ExpectedIDs))
	}
	return nil
}

func validateCase(item Case) error {
	if item.ID < 0 || item.Source == "" || item.Line < 1 || item.Origin == "" || item.Statement == "" {
		return errors.New("incomplete source provenance")
	}
	if item.OriginalSQL == "" || item.Shape == "" {
		return errors.New("missing source SQL or shape")
	}
	switch item.Status {
	case StatusVerified:
		if item.WantSQL == "" {
			return errors.New("verified case has no SQL expectation")
		}
	case StatusConstructionErr, StatusUnsupported:
		if item.Reason == "" {
			return errors.New("coverage gap has no reason")
		}
	default:
		return fmt.Errorf("unknown status %q", item.Status)
	}
	return nil
}

func validateFactory(item Case, factory Factory) error {
	if factory.ID != item.ID || factory.Status != item.Status {
		return fmt.Errorf("factory association is id=%d status=%q, want id=%d status=%q", factory.ID, factory.Status, item.ID, item.Status)
	}
	if (factory.Build == nil) != (item.Status == StatusUnsupported) {
		return fmt.Errorf("factory presence disagrees with status %q", item.Status)
	}
	return nil
}

// VerifyCase compares rendered SQL and arguments with the frozen oracle.
func VerifyCase(item Case, factory Factory) error {
	if err := validateFactory(item, factory); err != nil {
		return err
	}
	if item.Status != StatusVerified {
		return fmt.Errorf("id=%d: cannot verify status %q", item.ID, item.Status)
	}
	statement := factory.Build()
	if statement == nil {
		return fmt.Errorf("id=%d source=%s:%d: factory returned nil statement", item.ID, item.Source, item.Line)
	}
	gotSQL, gotArgs, err := statement.ToSQL()
	if err != nil {
		return fmt.Errorf("id=%d source=%s:%d origin=%s: ToSQL: %w; source=%q", item.ID, item.Source, item.Line, item.Origin, err, item.OriginalSQL)
	}
	if gotSQL != item.WantSQL {
		return fmt.Errorf("id=%d source=%s:%d origin=%s: SQL mismatch; got=%q want=%q source=%q", item.ID, item.Source, item.Line, item.Origin, gotSQL, item.WantSQL, item.OriginalSQL)
	}
	if len(gotArgs) != len(item.WantArgs) {
		return fmt.Errorf("id=%d source=%s:%d: argument count got %d want %d", item.ID, item.Source, item.Line, len(gotArgs), len(item.WantArgs))
	}
	for index, want := range item.WantArgs {
		got, ok := gotArgs[index].(string)
		if !ok || got != want {
			return fmt.Errorf("id=%d source=%s:%d: argument %d got %#v want %q", item.ID, item.Source, item.Line, index, gotArgs[index], want)
		}
	}
	return nil
}

// Run checks one embedded shard and schedules parallel cases in serial groups.
// Each group completes before the next is decoded, bounding retained metadata.
func Run(t *testing.T, index int, data string, factories []Factory) {
	t.Helper()
	file, err := os.Open("../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, readErr := ReadManifest(file)
	closeErr := file.Close()
	if fileErr := errors.Join(readErr, closeErr); fileErr != nil {
		t.Fatal(fileErr)
	}
	if index < 0 || index >= len(manifest.Shards) {
		t.Fatalf("invalid shard index %d", index)
	}
	shard := manifest.Shards[index]
	if hash := fmt.Sprintf("%x", sha256.Sum256([]byte(data))); hash != shard.SHA256 {
		t.Fatalf("%s: JSONL hash %s, want %s", shard.Package, hash, shard.SHA256)
	}
	if factories == nil {
		t.Fatal("missing shard factories")
	}
	err = StreamShard(strings.NewReader(data), shard.spec(), factories, func(group int, cases []Case) error {
		t.Run(fmt.Sprintf("group-%02d", group), func(t *testing.T) {
			// This parent stays serial so t.Run waits for all parallel children.
			for _, item := range cases {
				t.Run(fmt.Sprintf("%05d", item.ID), func(t *testing.T) {
					t.Parallel()
					if item.Status != StatusVerified {
						t.Skip(item.Reason)
					}
					if verifyErr := VerifyCase(item, factories[item.ID/ShardCount]); verifyErr != nil {
						t.Fatal(verifyErr)
					}
				})
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
