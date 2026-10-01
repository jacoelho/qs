package qx

// ExplainFormat controls the server's plan representation.
type ExplainFormat uint8

const (
	ExplainText ExplainFormat = iota
	ExplainJSON
	ExplainXML
	ExplainYAML
)

type explainOption struct {
	name    string
	value   string
	version PostgreSQLVersion
	bare    bool
}

// ExplainSerialization controls output-conversion measurement during ANALYZE.
type ExplainSerialization uint8

const (
	SerializeNone ExplainSerialization = iota
	SerializeText
	SerializeBinary
)

// ExplainBuilder only constructs EXPLAIN. ANALYZE executes the wrapped query
// when the caller sends it to PostgreSQL, including any mutations.
type ExplainBuilder struct {
	query   Statement
	options []explainOption
}

func Explain(query Statement) *ExplainBuilder { return &ExplainBuilder{query: query} }
func (b *ExplainBuilder) option(name string, value bool, version PostgreSQLVersion) *ExplainBuilder {
	text := "FALSE"
	if value {
		text = "TRUE"
	}
	return b.setOption(name, text, version)
}
func (b *ExplainBuilder) setOption(name, value string, version PostgreSQLVersion) *ExplainBuilder {
	for i := range b.options {
		if b.options[i].name == name {
			b.options[i] = explainOption{name: name, value: value, version: version}
			return b
		}
	}
	b.options = append(b.options, explainOption{name: name, value: value, version: version})
	return b
}

// Analyze requests execution as well as planning, including wrapped mutations.
func (b *ExplainBuilder) Analyze(value bool) *ExplainBuilder {
	return b.option("ANALYZE", value, PostgreSQL12)
}

// Verbose includes output expressions and qualified object names in the plan.
func (b *ExplainBuilder) Verbose(value bool) *ExplainBuilder {
	return b.option("VERBOSE", value, PostgreSQL12)
}

// Costs includes planner cost and cardinality estimates.
func (b *ExplainBuilder) Costs(value bool) *ExplainBuilder {
	return b.option("COSTS", value, PostgreSQL12)
}

// Settings includes planner settings that differ from built-in defaults.
func (b *ExplainBuilder) Settings(value bool) *ExplainBuilder {
	return b.option("SETTINGS", value, PostgreSQL12)
}

// Buffers requests buffer usage statistics; PostgreSQL controls their applicability.
func (b *ExplainBuilder) Buffers(value bool) *ExplainBuilder {
	return b.option("BUFFERS", value, PostgreSQL12)
}

// WAL requests write-ahead log statistics (PostgreSQL 13+).
func (b *ExplainBuilder) WAL(value bool) *ExplainBuilder { return b.option("WAL", value, PostgreSQL13) }

// Timing controls per-node execution timing during ANALYZE.
func (b *ExplainBuilder) Timing(value bool) *ExplainBuilder {
	return b.option("TIMING", value, PostgreSQL12)
}

// Summary controls planning and execution summary statistics.
func (b *ExplainBuilder) Summary(value bool) *ExplainBuilder {
	return b.option("SUMMARY", value, PostgreSQL12)
}

// GenericPlan requests a parameter-independent plan; it cannot combine with ANALYZE.
func (b *ExplainBuilder) GenericPlan(value bool) *ExplainBuilder {
	return b.option("GENERIC_PLAN", value, PostgreSQL16)
}

// Memory requests planner memory usage statistics (PostgreSQL 17+).
func (b *ExplainBuilder) Memory(value bool) *ExplainBuilder {
	return b.option("MEMORY", value, PostgreSQL17)
}

// Serialize requires PostgreSQL 17+. With no mode it emits bare SERIALIZE,
// which defaults to TEXT. All modes except NONE require Analyze(true).
func (b *ExplainBuilder) Serialize(modes ...ExplainSerialization) *ExplainBuilder {
	if len(modes) == 0 {
		b.setOption("SERIALIZE", "TRUE", PostgreSQL17)
		for i := range b.options {
			if b.options[i].name == "SERIALIZE" {
				b.options[i].bare = true
			}
		}
		return b
	}
	var value string
	if len(modes) == 1 {
		switch modes[0] {
		case SerializeNone:
			value = "NONE"
		case SerializeText:
			value = "TEXT"
		case SerializeBinary:
			value = "BINARY"
		}
	}
	return b.setOption("SERIALIZE", value, PostgreSQL17)
}
func (b *ExplainBuilder) Format(format ExplainFormat) *ExplainBuilder {
	var value string
	switch format {
	case ExplainText:
		value = "TEXT"
	case ExplainJSON:
		value = "JSON"
	case ExplainXML:
		value = "XML"
	case ExplainYAML:
		value = "YAML"
	}
	return b.setOption("FORMAT", value, PostgreSQL12)
}
func (b *ExplainBuilder) append(w *renderer) {
	switch b.query.(type) {
	case *SelectBuilder, *InsertBuilder, *UpdateBuilder, *DeleteBuilder, *MergeBuilder, *SetBuilder, *ValuesBuilder, *TableBuilder,
		*ExecuteBuilder, *CreateTableAsBuilder, *MaterializedViewBuilder, *DeclareCursorBuilder, *SelectIntoBuilder, *SQLStatement:
	default:
		w.fail(ErrInvalid, "EXPLAIN", "unsupported wrapped statement")
		return
	}
	analyze, generic := false, false
	for _, o := range b.options {
		if o.name == "ANALYZE" {
			analyze = o.value == "TRUE"
		}
		if o.name == "GENERIC_PLAN" {
			generic = o.value == "TRUE"
		}
	}
	if !w.require(!(analyze && generic), "EXPLAIN", "ANALYZE and GENERIC_PLAN are incompatible") {
		return
	}
	for _, o := range b.options {
		if !w.require(o.value != "", "EXPLAIN", "unknown option value") {
			return
		}
		if o.name == "SERIALIZE" && o.value != "NONE" && !w.require(analyze, "EXPLAIN", "serialization requires ANALYZE") {
			return
		}
	}
	w.text("EXPLAIN")
	if len(b.options) > 0 {
		w.text(" (")
		for i, o := range b.options {
			if i > 0 {
				w.text(", ")
			}
			w.feature(o.version, "EXPLAIN "+o.name)
			w.text(o.name)
			if !o.bare {
				w.byte(' ')
				w.text(o.value)
			}
		}
		w.byte(')')
	}
	w.byte(' ')
	// EXPLAIN is a transparent wrapper for top-level data-modifying CTE rules.
	w.statementDepth--
	w.statement(b.query)
	w.statementDepth++
}

// TruncateBuilder constructs PostgreSQL TRUNCATE. It is not affected by RLS.
type TruncateBuilder struct {
	tables    []Relation
	restart   bool
	identity  bool
	cascade   bool
	behaviour bool
}

func Truncate(names ...string) *TruncateBuilder { b := &TruncateBuilder{}; return b.Tables(names...) }
func (b *TruncateBuilder) Tables(names ...string) *TruncateBuilder {
	for _, n := range names {
		b.tables = append(b.tables, Table(n))
	}
	return b
}
func (b *TruncateBuilder) TablesExpr(tables ...Relation) *TruncateBuilder {
	b.tables = append(b.tables, tables...)
	return b
}
func (b *TruncateBuilder) RestartIdentity() *TruncateBuilder {
	b.identity = true
	b.restart = true
	return b
}
func (b *TruncateBuilder) ContinueIdentity() *TruncateBuilder {
	b.identity = true
	b.restart = false
	return b
}
func (b *TruncateBuilder) Cascade() *TruncateBuilder { b.behaviour = true; b.cascade = true; return b }
func (b *TruncateBuilder) Restrict() *TruncateBuilder {
	b.behaviour = true
	b.cascade = false
	return b
}
func (b *TruncateBuilder) append(w *renderer) {
	if !w.require(len(b.tables) > 0, "TRUNCATE", "requires at least one table") {
		return
	}
	w.text("TRUNCATE TABLE ")
	for i, t := range b.tables {
		if i > 0 {
			w.text(", ")
		}
		if !w.require(t.alias == "", "TRUNCATE", "aliases are not supported") {
			return
		}
		w.target(t, false)
	}
	if b.identity {
		if b.restart {
			w.text(" RESTART IDENTITY")
		} else {
			w.text(" CONTINUE IDENTITY")
		}
	}
	if b.behaviour {
		if b.cascade {
			w.text(" CASCADE")
		} else {
			w.text(" RESTRICT")
		}
	}
}
