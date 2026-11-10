package postgrescorpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

const (
	manifestSchema   = "qs-postgres-corpus-v3"
	fixtureDirectory = "testdata"
)

// ParserManifest pins the independent development-time SQL oracle.
type ParserManifest struct {
	Package string `json:"package"`
	Version string `json:"version"`
	// PostgreSQL is the grammar version the parser package reports.
	PostgreSQL string `json:"postgresql"`
}

// StatusCounts counts occurrences per Status.
type StatusCounts struct {
	Verified   int `json:"verified"`
	OutOfScope int `json:"out_of_scope"`
	Gap        int `json:"gap"`
	Rejected   int `json:"rejected"`
}

func (c *StatusCounts) add(status Status) {
	switch status {
	case StatusVerified:
		c.Verified++
	case StatusOutOfScope:
		c.OutOfScope++
	case StatusGap:
		c.Gap++
	case StatusRejected:
		c.Rejected++
	}
}

func (c *StatusCounts) merge(other StatusCounts) {
	c.Verified += other.Verified
	c.OutOfScope += other.OutOfScope
	c.Gap += other.Gap
	c.Rejected += other.Rejected
}

func (c *StatusCounts) total() int { return c.Verified + c.OutOfScope + c.Gap + c.Rejected }

func (c *StatusCounts) valid() bool {
	return c.Verified >= 0 && c.OutOfScope >= 0 && c.Gap >= 0 && c.Rejected >= 0
}

// CountsManifest records occurrence coverage and whole-corpus shape coverage.
// A shape counts as verified only when every occurrence of it is verified.
type CountsManifest struct {
	Total int `json:"total"`
	StatusCounts
	PlannerTotal                  int `json:"planner_total"`
	PlannerVerified               int `json:"planner_verified"`
	DistinctShapes                int `json:"distinct_shapes"`
	DistinctVerifiedShapes        int `json:"distinct_verified_shapes"`
	PlannerDistinctShapes         int `json:"planner_distinct_shapes"`
	PlannerDistinctVerifiedShapes int `json:"planner_distinct_verified_shapes"`
}

// FileManifest pins one fixture file and its contiguous ID range.
type FileManifest struct {
	// Path is relative to the manifest.
	Path string `json:"path"`
	// SHA256 is the lowercase digest of the complete file.
	SHA256  string `json:"sha256"`
	FirstID int    `json:"first_id"`
	Count   int    `json:"count"`
	StatusCounts
}

// Manifest is the canonical inventory of the pinned corpus fixtures.
type Manifest struct {
	Schema string `json:"schema"`
	// Revision is the PostgreSQL source revision.
	Revision string `json:"revision"`
	// SQLSHA256 is the digest of the source regression SQL files.
	SQLSHA256 string         `json:"sql_sha256"`
	Parser    ParserManifest `json:"parser"`
	Files     []FileManifest `json:"files"`
	Counts    CountsManifest `json:"counts"`
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
	if m.Schema != manifestSchema {
		return fmt.Errorf("unsupported corpus schema %q", m.Schema)
	}
	if !validHex(m.Revision, 40) || !validHex(m.SQLSHA256, 64) || m.Parser.Package != "pglast" || m.Parser.Version == "" || m.Parser.PostgreSQL == "" {
		return errors.New("incomplete corpus source/parser provenance")
	}
	if err := m.Counts.validate(); err != nil {
		return err
	}
	return m.validateFiles()
}

func (c CountsManifest) validate() error {
	if c.Total < 1 || !c.valid() || c.total() != c.Total ||
		c.PlannerTotal < 0 || c.PlannerTotal > c.Total || c.PlannerVerified < 0 || c.PlannerVerified > c.PlannerTotal ||
		c.DistinctShapes < 1 || c.DistinctShapes > c.Total || c.DistinctVerifiedShapes < 0 || c.DistinctVerifiedShapes > c.DistinctShapes ||
		c.PlannerDistinctShapes < 0 || c.PlannerDistinctShapes > c.DistinctShapes || c.PlannerDistinctVerifiedShapes < 0 || c.PlannerDistinctVerifiedShapes > c.PlannerDistinctShapes {
		return errors.New("invalid corpus counts")
	}
	return nil
}

func fixturePath(index int) string {
	return fmt.Sprintf("%s/corpus-%02d.jsonl", fixtureDirectory, index)
}

func (m Manifest) validateFiles() error {
	if len(m.Files) == 0 {
		return errors.New("corpus manifest lists no fixture files")
	}
	var statuses StatusCounts
	next := 0
	for index, file := range m.Files {
		if file.Path != fixturePath(index) || !validHex(file.SHA256, 64) {
			return fmt.Errorf("fixture file %d: invalid path or hash", index)
		}
		if file.FirstID != next || file.Count < 1 || !file.valid() || file.total() != file.Count {
			return fmt.Errorf("%s: invalid occurrence inventory/counts", file.Path)
		}
		next += file.Count
		statuses.merge(file.StatusCounts)
	}
	if next != m.Counts.Total || statuses != m.Counts.StatusCounts {
		return errors.New("fixture file counts disagree with global counts")
	}
	return nil
}

type shapeCoverage struct {
	planner bool
	failed  bool
}

// Audit checks the fixture inventory, every file hash and record, and the
// global union of shapes against the manifest. It holds one record at a time.
func Audit(root fs.FS, manifest Manifest) error {
	if err := manifest.validate(); err != nil {
		return err
	}
	if err := auditInventory(root, manifest); err != nil {
		return err
	}
	var counts CountsManifest
	shapes := make(map[string]shapeCoverage, manifest.Counts.DistinctShapes)
	for _, file := range manifest.Files {
		var local StatusCounts
		err := auditFile(root, file, func(item Case) {
			counts.Total++
			counts.add(item.Status)
			local.add(item.Status)
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
		})
		if err != nil {
			return err
		}
		if local != file.StatusCounts {
			return fmt.Errorf("%s: status counts %+v disagree with manifest %+v", file.Path, local, file.StatusCounts)
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

// auditInventory rejects fixture files the manifest does not list; a listed
// file that is missing fails when it is opened.
func auditInventory(root fs.FS, manifest Manifest) error {
	entries, err := fs.ReadDir(root, fixtureDirectory)
	if err != nil {
		return fmt.Errorf("read fixture inventory: %w", err)
	}
	listed := make(map[string]bool, len(manifest.Files))
	for _, file := range manifest.Files {
		listed[file.Path] = true
	}
	for _, entry := range entries {
		if name := path.Join(fixtureDirectory, entry.Name()); !listed[name] {
			return fmt.Errorf("unlisted fixture artifact %s", name)
		}
	}
	return nil
}

func auditFile(root fs.FS, file FileManifest, consume func(Case)) error {
	reader, err := root.Open(file.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", file.Path, err)
	}
	hash := sha256.New()
	var streamErr error
	for item, err := range Cases(io.TeeReader(reader, hash), file.FirstID, file.Count) {
		if err != nil {
			streamErr = fmt.Errorf("%s: %w", file.Path, err)
			break
		}
		consume(item)
	}
	if err := errors.Join(streamErr, reader.Close()); err != nil {
		return err
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != file.SHA256 {
		return fmt.Errorf("%s: hash %s, want %s", file.Path, actual, file.SHA256)
	}
	return nil
}
