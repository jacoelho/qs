package qx

import (
	"errors"
	"testing"
)

func TestExplainSerialization(t *testing.T) {
	t.Parallel()

	checkSQL(t, Explain(Select(LiteralInt(1))).Analyze(true).Serialize(SerializeText).Buffers(true).Timing(false).Format(ExplainJSON), `EXPLAIN (ANALYZE TRUE, SERIALIZE TEXT, BUFFERS TRUE, TIMING FALSE, FORMAT JSON) SELECT 1`)
	checkSQL(t, Explain(Select(LiteralInt(1))).Serialize(SerializeBinary).Analyze(true), `EXPLAIN (SERIALIZE BINARY, ANALYZE TRUE) SELECT 1`)
	checkSQL(t, Explain(Select(LiteralInt(1))).Serialize(SerializeText).Serialize(SerializeNone), `EXPLAIN (SERIALIZE NONE) SELECT 1`)
	for _, query := range []Statement{
		Explain(Select(LiteralInt(1))).Serialize(SerializeText),
		Explain(Select(LiteralInt(1))).Analyze(true).Serialize(ExplainSerialization(99)),
	} {
		if _, _, err := query.ToSQL(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid serialization accepted: %v", err)
		}
	}
	if _, _, err := ToSQLWith(Explain(Select(LiteralInt(1))).Analyze(true).Serialize(SerializeBinary), Options{PostgreSQL: PostgreSQL16}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("serialization on PostgreSQL 16: %v", err)
	}
}
