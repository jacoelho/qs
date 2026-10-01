// Package postgrescorpus contains the bounded native harness for the pinned
// PostgreSQL regression corpus.
//
// The corpus metadata is frozen as JSONL in eight test-only shard packages.
// The shard builders and tests are deliberately excluded from production
// builds; this package owns only the streaming decoder and audit logic.
package postgrescorpus
