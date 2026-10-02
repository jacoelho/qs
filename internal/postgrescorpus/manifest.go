package postgrescorpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// ParserManifest pins the independent development-time SQL oracle.
type ParserManifest struct {
	// Package is the parser package name, currently pglast.
	Package string `json:"package"`
	// Version is the parser package version used to produce the corpus.
	Version string `json:"version"`
	// PostgreSQL is the PostgreSQL grammar/version reported by the parser package.
	PostgreSQL string `json:"postgresql"`
}

// CountsManifest records occurrence coverage and whole-corpus shape coverage.
type CountsManifest struct {
	// Total is the number of occurrences across all shards.
	Total int `json:"total"`
	// Verified is the number of occurrences with StatusVerified.
	Verified int `json:"verified"`
	// ConstructionError is the number of occurrences with StatusConstructionErr.
	ConstructionError int `json:"construction_error"`
	// Unsupported is the number of occurrences with StatusUnsupported.
	Unsupported int `json:"unsupported"`
	// PlannerTotal is the number of planner-focused occurrences.
	PlannerTotal int `json:"planner_total"`
	// PlannerVerified is the number of verified planner-focused occurrences.
	PlannerVerified int `json:"planner_verified"`
	// DistinctShapes is the number of unique normalized query shapes.
	DistinctShapes int `json:"distinct_shapes"`
	// DistinctVerifiedShapes is the number of shapes whose every occurrence is verified.
	DistinctVerifiedShapes int `json:"distinct_verified_shapes"`
	// PlannerDistinctShapes is the number of shapes containing at least one planner occurrence.
	PlannerDistinctShapes int `json:"planner_distinct_shapes"`
	// PlannerDistinctVerifiedShapes is the number of planner shapes whose every occurrence is verified.
	PlannerDistinctVerifiedShapes int `json:"planner_distinct_verified_shapes"`
}

// ShardManifest pins one package's complete strided occurrence inventory.
type ShardManifest struct {
	// Package is the shard package directory name, such as shard00.
	Package string `json:"package"`
	// JSONL is the shard's manifest-relative JSONL path.
	JSONL string `json:"jsonl"`
	// SHA256 is the lowercase SHA-256 digest of the complete JSONL file.
	SHA256 string `json:"sha256"`
	// Index is the zero-based shard index.
	Index int `json:"index"`
	// Count is the number of records in the shard.
	Count int `json:"count"`
	// FirstID is the first global occurrence ID assigned to the shard.
	FirstID int `json:"first_id"`
	// LastID is the last global occurrence ID assigned to the shard.
	LastID int `json:"last_id"`
	// Verified is the number of verified records in the shard.
	Verified int `json:"verified"`
	// ConstructionError is the number of construction-error records in the shard.
	ConstructionError int `json:"construction_error"`
	// Unsupported is the number of unsupported records in the shard.
	Unsupported int `json:"unsupported"`
}

// Manifest is the canonical inventory of the pinned corpus artifacts.
type Manifest struct {
	// Schema identifies the manifest format and validation rules.
	Schema string `json:"schema"`
	// Revision is the source PostgreSQL repository revision used for the corpus.
	Revision string `json:"revision"`
	// SQLSHA256 is the digest of the source regression SQL corpus.
	SQLSHA256 string `json:"sql_sha256"`
	// Parser records the independent parser provenance for the corpus.
	Parser ParserManifest `json:"parser"`
	// Shards lists every strided JSONL and generated-test shard.
	Shards []ShardManifest `json:"shards"`
	// Counts records the global occurrence and shape coverage totals.
	Counts CountsManifest `json:"counts"`
}

// ReadManifest decodes and validates one manifest, rejecting unknown fields
// and trailing JSON values.
func ReadManifest(reader io.Reader) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode corpus manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, errors.New("corpus manifest has trailing JSON")
		}
		return Manifest{}, fmt.Errorf("decode corpus manifest trailer: %w", err)
	}
	if err := manifest.validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validHex(value string, length int) bool {
	if len(value) != length || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (m Manifest) validate() error {
	if m.Schema != "qs-postgres-corpus-v2" {
		return fmt.Errorf("unsupported corpus schema %q", m.Schema)
	}
	if !validHex(m.Revision, 40) || !validHex(m.SQLSHA256, 64) || m.Parser.Package != "pglast" || m.Parser.Version == "" || m.Parser.PostgreSQL == "" {
		return errors.New("incomplete corpus source/parser provenance")
	}
	if err := m.Counts.validate(); err != nil {
		return err
	}
	return m.validateShards()
}

func (c CountsManifest) validate() error {
	if c.Total < 1 || c.Verified < 0 || c.ConstructionError < 0 || c.Unsupported < 0 ||
		c.Verified+c.ConstructionError+c.Unsupported != c.Total ||
		c.PlannerTotal < 0 || c.PlannerTotal > c.Total || c.PlannerVerified < 0 || c.PlannerVerified > c.PlannerTotal ||
		c.DistinctShapes < 1 || c.DistinctShapes > c.Total || c.DistinctVerifiedShapes < 0 || c.DistinctVerifiedShapes > c.DistinctShapes ||
		c.PlannerDistinctShapes < 0 || c.PlannerDistinctShapes > c.DistinctShapes || c.PlannerDistinctVerifiedShapes < 0 || c.PlannerDistinctVerifiedShapes > c.PlannerDistinctShapes {
		return errors.New("invalid corpus counts")
	}
	return nil
}

func (m Manifest) validateShards() error {
	c := m.Counts
	if len(m.Shards) != ShardCount {
		return fmt.Errorf("corpus has %d shards, want %d", len(m.Shards), ShardCount)
	}
	var verified, constructionError, unsupported int
	for index, shard := range m.Shards {
		name := fmt.Sprintf("shard%02d", index)
		count := (c.Total + ShardCount - 1 - index) / ShardCount
		first, last := index, index+(count-1)*ShardCount
		if count == 0 {
			first, last = -1, -1
		}
		if shard.Index != index || shard.Package != name || shard.JSONL != name+"/corpus.jsonl" || !validHex(shard.SHA256, 64) {
			return fmt.Errorf("invalid shard %d name/path/hash", index)
		}
		if shard.Count != count || shard.FirstID != first || shard.LastID != last ||
			shard.Verified < 0 || shard.ConstructionError < 0 || shard.Unsupported < 0 || shard.Verified+shard.ConstructionError+shard.Unsupported != count {
			return fmt.Errorf("%s: invalid occurrence inventory/counts", name)
		}
		verified += shard.Verified
		constructionError += shard.ConstructionError
		unsupported += shard.Unsupported
	}
	if verified != c.Verified || constructionError != c.ConstructionError || unsupported != c.Unsupported {
		return errors.New("shard status counts disagree with global counts")
	}
	return nil
}

func (s ShardManifest) spec() ShardSpec {
	ids := make([]int, s.Count)
	for index := range ids {
		ids[index] = s.Index + index*ShardCount
	}
	return ShardSpec{Name: s.Package, Index: s.Index, ExpectedIDs: ids}
}

type shapeCoverage struct {
	planner bool
	failed  bool
}

// Audit checks the actual shard inventory, every JSONL hash and occurrence,
// and the global union of shapes against the manifest. It never retains all
// decoded cases at once.
func Audit(root fs.FS, manifest Manifest) error {
	if err := manifest.validate(); err != nil {
		return err
	}
	if err := auditPackages(root); err != nil {
		return err
	}
	var counts CountsManifest
	shapes := make(map[string]shapeCoverage, manifest.Counts.DistinctShapes)
	for _, shard := range manifest.Shards {
		local := [3]int{}
		err := auditShard(root, shard, func(_ int, cases []Case) error {
			for _, item := range cases {
				counts.Total++
				switch item.Status {
				case StatusVerified:
					counts.Verified++
					local[0]++
				case StatusConstructionErr:
					counts.ConstructionError++
					local[1]++
				case StatusUnsupported:
					counts.Unsupported++
					local[2]++
				}
				if item.Planner {
					counts.PlannerTotal++
					if item.Status == StatusVerified {
						counts.PlannerVerified++
					}
				}
				shape := shapes[item.Shape]
				shape.planner = shape.planner || item.Planner
				shape.failed = shape.failed || item.Status != StatusVerified
				shapes[item.Shape] = shape
			}
			return nil
		})
		if err != nil {
			return err
		}
		if local != [3]int{shard.Verified, shard.ConstructionError, shard.Unsupported} {
			return fmt.Errorf("%s: status counts %v disagree with manifest", shard.Package, local)
		}
	}
	counts.DistinctShapes = len(shapes)
	for _, shape := range shapes {
		if !shape.failed {
			counts.DistinctVerifiedShapes++
		}
		if shape.planner {
			counts.PlannerDistinctShapes++
			if !shape.failed {
				counts.PlannerDistinctVerifiedShapes++
			}
		}
	}
	if counts != manifest.Counts {
		return fmt.Errorf("corpus counts %+v disagree with manifest %+v", counts, manifest.Counts)
	}
	return nil
}

func auditPackages(root fs.FS) error {
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		return fmt.Errorf("read corpus inventory: %w", err)
	}
	seen := make(map[string]bool, ShardCount)
	for _, entry := range entries {
		name := entry.Name()
		if name == "corpus_generated.go" || name == "corpus_generated_test.go" {
			return fmt.Errorf("stale monolithic corpus %s", name)
		}
		if !entry.IsDir() {
			continue
		}
		seen[name] = true
		files, err := fs.ReadDir(root, name)
		if err != nil {
			return fmt.Errorf("read %s inventory: %w", name, err)
		}
		if len(files) != 2 || files[0].Name() != "corpus.jsonl" || files[1].Name() != "corpus_generated_test.go" || files[0].IsDir() || files[1].IsDir() {
			return fmt.Errorf("%s: unexpected artifact inventory", name)
		}
	}
	if len(seen) != ShardCount {
		return fmt.Errorf("found %d corpus packages, want %d", len(seen), ShardCount)
	}
	for index := range ShardCount {
		name := fmt.Sprintf("shard%02d", index)
		if !seen[name] {
			return fmt.Errorf("missing corpus package %s", name)
		}
	}
	return nil
}

func auditShard(root fs.FS, shard ShardManifest, consume func(int, []Case) error) error {
	file, err := root.Open(shard.JSONL)
	if err != nil {
		return fmt.Errorf("open %s: %w", shard.JSONL, err)
	}
	hash := sha256.New()
	streamErr := StreamShard(io.TeeReader(file, hash), shard.spec(), nil, consume)
	if err := errors.Join(streamErr, file.Close()); err != nil {
		return err
	}
	if actual := fmt.Sprintf("%x", hash.Sum(nil)); actual != shard.SHA256 {
		return fmt.Errorf("%s: JSONL hash %s, want %s", shard.Package, actual, shard.SHA256)
	}
	return nil
}
