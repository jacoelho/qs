package qs

// ExecuteBuilder constructs PostgreSQL's EXECUTE command. The prepared
// statement name is one identifier component and is quoted automatically.
// Arguments may be SQL expressions, but external bind parameters are rejected
// because PostgreSQL's EXECUTE argument grammar cannot receive them through
// the extended protocol.
type ExecuteBuilder struct {
	name string
	args []Expr
}

// Execute constructs an EXECUTE command for a prepared statement name.
func Execute(name string, args ...Expr) *ExecuteBuilder {
	return &ExecuteBuilder{name: name, args: cloneSlice(args)}
}

func (b *ExecuteBuilder) append(w *renderer) {
	w.text("EXECUTE ")
	w.identifierPart(b.name)
	if len(b.args) > 0 {
		w.text(" (")
		start := len(w.args)
		w.exprs(b.args, ", ")
		w.byte(')')
		if len(w.args) > start {
			w.fail(ErrInvalid, "EXECUTE", "external bind parameters are not supported in EXECUTE arguments; use SQL literals or parameter-free expressions")
		}
	}
}

// utilityPersistence is shared only by utility commands that can create a
// physical table. Keeping it private prevents a generic option bag from
// leaking into the public builder API.
type utilityPersistence uint8

const (
	utilityLogged utilityPersistence = iota
	utilityTemporary
	utilityUnlogged
)

// OnCommitAction is the PostgreSQL temporary-table transaction policy.
type OnCommitAction uint8

const (
	// OnCommitPreserveRows retains rows in a temporary destination after commit.
	OnCommitPreserveRows OnCommitAction = iota + 1
	// OnCommitDeleteRows deletes rows in a temporary destination after commit.
	OnCommitDeleteRows
	// OnCommitDrop drops a temporary destination after commit.
	OnCommitDrop
)

type createTableAsDestination struct {
	name        string
	access      string
	tablespace  string
	columns     []string
	persistence utilityPersistence
	ifNotExists bool
	onCommit    OnCommitAction
	hasOnCommit bool
	noData      bool
	hasData     bool
}

type materializedViewDestination struct {
	name        string
	access      string
	tablespace  string
	columns     []string
	ifNotExists bool
	noData      bool
	hasData     bool
}

func appendUtilityColumns(dst *[]string, names ...string) {
	*dst = append(*dst, names...)
}

func appendUtilityColumnList(w *renderer, clause string, columns []string) {
	if len(columns) == 0 {
		return
	}
	for i, name := range columns {
		for j := range i {
			if columns[j] == name {
				w.fail(ErrInvalid, clause, "duplicate destination column")
				return
			}
		}
	}
	w.text(" (")
	w.names(columns)
	w.byte(')')
}

func appendUtilityTarget(w *renderer, clause, name string, columns []string) {
	if !w.require(name != "", clause, "requires a destination name") {
		return
	}
	w.identifierPath(name, false)
	appendUtilityColumnList(w, clause, columns)
}

func appendUtilityOnCommit(w *renderer, clause string, persistence utilityPersistence, action OnCommitAction, hasAction bool) {
	if !hasAction {
		return
	}
	if !w.require(persistence == utilityTemporary, clause, "ON COMMIT requires a temporary destination") {
		return
	}
	w.text(" ON COMMIT ")
	switch action {
	case OnCommitPreserveRows:
		w.text("PRESERVE ROWS")
	case OnCommitDeleteRows:
		w.text("DELETE ROWS")
	case OnCommitDrop:
		w.text("DROP")
	default:
		w.fail(ErrInvalid, clause, "unknown ON COMMIT action")
	}
}

func appendUtilityData(w *renderer, noData, hasData bool) {
	if !hasData {
		return
	}
	if noData {
		w.text(" WITH NO DATA")
	} else {
		w.text(" WITH DATA")
	}
}

// CreateTableAsBuilder constructs CREATE TABLE AS, including its EXECUTE
// source form. The two constructors intentionally share the private source
// storage so cloning and validation have one implementation.
type CreateTableAsBuilder struct {
	source      queryUtilitySource
	destination createTableAsDestination
}

// CreateTableAs constructs CREATE TABLE AS from a rowset.
func CreateTableAs(name string, query Rowset) *CreateTableAsBuilder {
	return &CreateTableAsBuilder{
		destination: createTableAsDestination{name: name},
		source:      queryUtilitySource{query: query},
	}
}

// CreateTableAsExecute constructs CREATE TABLE AS from EXECUTE.
func CreateTableAsExecute(name string, query *ExecuteBuilder) *CreateTableAsBuilder {
	return &CreateTableAsBuilder{
		destination: createTableAsDestination{name: name},
		source:      queryUtilitySource{execute: query, executeSource: true},
	}
}

// Temporary makes the CTAS destination temporary, replacing its persistence mode.
func (b *CreateTableAsBuilder) Temporary() *CreateTableAsBuilder {
	b.destination.persistence = utilityTemporary
	return b
}

// Unlogged makes the CTAS destination unlogged, replacing its persistence mode.
func (b *CreateTableAsBuilder) Unlogged() *CreateTableAsBuilder {
	b.destination.persistence = utilityUnlogged
	return b
}

// IfNotExists adds IF NOT EXISTS to the CTAS destination.
func (b *CreateTableAsBuilder) IfNotExists() *CreateTableAsBuilder {
	b.destination.ifNotExists = true
	return b
}

// Columns appends CTAS destination column aliases.
func (b *CreateTableAsBuilder) Columns(names ...string) *CreateTableAsBuilder {
	appendUtilityColumns(&b.destination.columns, names...)
	return b
}

// Using sets the CTAS table access method.
func (b *CreateTableAsBuilder) Using(accessMethod string) *CreateTableAsBuilder {
	b.destination.access = accessMethod
	return b
}

// Tablespace sets the CTAS destination tablespace.
func (b *CreateTableAsBuilder) Tablespace(name string) *CreateTableAsBuilder {
	b.destination.tablespace = name
	return b
}

// OnCommit sets the temporary-table commit policy, replacing any prior policy.
func (b *CreateTableAsBuilder) OnCommit(action OnCommitAction) *CreateTableAsBuilder {
	b.destination.onCommit = action
	b.destination.hasOnCommit = true
	return b
}

// WithNoData requests that CTAS create the destination without copying rows.
func (b *CreateTableAsBuilder) WithNoData() *CreateTableAsBuilder {
	b.destination.noData = true
	b.destination.hasData = true
	return b
}

// WithData requests that CTAS copy rows into the destination.
func (b *CreateTableAsBuilder) WithData() *CreateTableAsBuilder {
	b.destination.noData = false
	b.destination.hasData = true
	return b
}

func (b *CreateTableAsBuilder) append(w *renderer) {
	const clause = "CREATE TABLE AS"
	if !w.require(b.source.statement() != nil, clause, "requires a query source") {
		return
	}
	width := b.source.width(w.options.MaxDepth)
	if width >= 0 && !w.require(len(b.destination.columns) <= width, clause, "too many destination columns") {
		return
	}
	if b.destination.persistence == utilityLogged && b.destination.hasOnCommit {
		w.fail(ErrInvalid, clause, "ON COMMIT requires a temporary destination")
		return
	}
	w.text("CREATE ")
	switch b.destination.persistence {
	case utilityLogged:
	case utilityTemporary:
		w.text("TEMPORARY ")
	case utilityUnlogged:
		w.text("UNLOGGED ")
	default:
		w.fail(ErrInvalid, clause, "unknown destination persistence")
		return
	}
	w.text("TABLE ")
	if b.destination.ifNotExists {
		w.text("IF NOT EXISTS ")
	}
	appendUtilityTarget(w, clause, b.destination.name, b.destination.columns)
	if w.err != nil {
		return
	}
	if b.destination.access != "" {
		w.text(" USING ")
		w.identifierPart(b.destination.access)
	}
	if b.destination.hasOnCommit {
		appendUtilityOnCommit(w, clause, b.destination.persistence, b.destination.onCommit, true)
	}
	if b.destination.tablespace != "" {
		w.text(" TABLESPACE ")
		w.identifierPart(b.destination.tablespace)
	}
	w.text(" AS ")
	w.appendTransparentStatement(b.source.statement())
	if w.err != nil {
		return
	}
	appendUtilityData(w, b.destination.noData, b.destination.hasData)
}

// MaterializedViewBuilder is deliberately separate from CTAS: PostgreSQL
// materialized views have no temporary or unlogged destination form.
type MaterializedViewBuilder struct {
	query       Rowset
	destination materializedViewDestination
}

// MaterializedViewAs constructs CREATE MATERIALIZED VIEW from a rowset.
func MaterializedViewAs(name string, query Rowset) *MaterializedViewBuilder {
	return &MaterializedViewBuilder{destination: materializedViewDestination{name: name}, query: query}
}

// IfNotExists adds IF NOT EXISTS to the materialized-view destination.
func (b *MaterializedViewBuilder) IfNotExists() *MaterializedViewBuilder {
	b.destination.ifNotExists = true
	return b
}

// Columns appends materialized-view column aliases.
func (b *MaterializedViewBuilder) Columns(names ...string) *MaterializedViewBuilder {
	appendUtilityColumns(&b.destination.columns, names...)
	return b
}

// Using sets the materialized-view access method.
func (b *MaterializedViewBuilder) Using(accessMethod string) *MaterializedViewBuilder {
	b.destination.access = accessMethod
	return b
}

// Tablespace sets the materialized-view destination tablespace.
func (b *MaterializedViewBuilder) Tablespace(name string) *MaterializedViewBuilder {
	b.destination.tablespace = name
	return b
}

// WithNoData requests that the materialized view be created without rows.
func (b *MaterializedViewBuilder) WithNoData() *MaterializedViewBuilder {
	b.destination.noData = true
	b.destination.hasData = true
	return b
}

// WithData requests that the materialized view be populated.
func (b *MaterializedViewBuilder) WithData() *MaterializedViewBuilder {
	b.destination.noData = false
	b.destination.hasData = true
	return b
}

func (b *MaterializedViewBuilder) append(w *renderer) {
	const clause = "CREATE MATERIALIZED VIEW"
	if !w.require(b.query != nil, clause, "requires a query source") {
		return
	}
	width := statementWidth(b.query, w.options.MaxDepth)
	if width >= 0 && !w.require(len(b.destination.columns) <= width, clause, "too many destination columns") {
		return
	}
	w.text("CREATE MATERIALIZED VIEW ")
	if b.destination.ifNotExists {
		w.text("IF NOT EXISTS ")
	}
	appendUtilityTarget(w, clause, b.destination.name, b.destination.columns)
	if w.err != nil {
		return
	}
	if b.destination.access != "" {
		w.text(" USING ")
		w.identifierPart(b.destination.access)
	}
	if b.destination.tablespace != "" {
		w.text(" TABLESPACE ")
		w.identifierPart(b.destination.tablespace)
	}
	w.text(" AS ")
	start := len(w.args)
	w.statement(b.query)
	if w.err != nil {
		return
	}
	if !w.require(len(w.args) == start, clause, "query parameters are not allowed") {
		return
	}
	appendUtilityData(w, b.destination.noData, b.destination.hasData)
}

// CursorScrollMode controls the optional SCROLL spelling in DECLARE CURSOR.
// The zero value leaves PostgreSQL's cursor default unspecified.
type CursorScrollMode uint8

const (
	// CursorScrollUnspecified leaves PostgreSQL's cursor scroll behavior unspecified.
	CursorScrollUnspecified CursorScrollMode = iota
	// CursorScroll requests a scrollable cursor.
	CursorScroll
	// CursorNoScroll requests a non-scrollable cursor.
	CursorNoScroll
)

// DeclareCursorBuilder constructs DECLARE CURSOR. The source is a Rowset so
// cursor options cannot accidentally wrap an arbitrary statement.
type DeclareCursorBuilder struct {
	query       Rowset
	name        string
	scroll      CursorScrollMode
	binary      bool
	insensitive bool
	hold        bool
	hasHold     bool
}

// DeclareCursor constructs DECLARE CURSOR for a rowset.
func DeclareCursor(name string, query Rowset) *DeclareCursorBuilder {
	return &DeclareCursorBuilder{name: name, query: query}
}

// Scroll replaces the cursor's scroll mode.
func (b *DeclareCursorBuilder) Scroll(mode CursorScrollMode) *DeclareCursorBuilder {
	b.scroll = mode
	return b
}

// Binary requests binary cursor output.
func (b *DeclareCursorBuilder) Binary() *DeclareCursorBuilder {
	b.binary = true
	return b
}

// Insensitive requests an insensitive cursor.
func (b *DeclareCursorBuilder) Insensitive() *DeclareCursorBuilder {
	b.insensitive = true
	return b
}

// WithHold requests that the cursor remain usable after commit.
func (b *DeclareCursorBuilder) WithHold() *DeclareCursorBuilder {
	b.hold = true
	b.hasHold = true
	return b
}

// WithoutHold requests that the cursor close at commit.
func (b *DeclareCursorBuilder) WithoutHold() *DeclareCursorBuilder {
	b.hold = false
	b.hasHold = true
	return b
}

func (b *DeclareCursorBuilder) append(w *renderer) {
	const clause = "DECLARE CURSOR"
	if !w.require(b.name != "", clause, "requires a cursor name") {
		return
	}
	if !w.require(b.query != nil, clause, "requires a query source") {
		return
	}
	if !w.require(b.scroll <= CursorNoScroll, clause, "unknown scroll mode") {
		return
	}
	if b.hold || b.scroll == CursorScroll || b.insensitive {
		if !w.require(!rowsetHasLocks(w, b.query, 0), clause, "HOLD, SCROLL and INSENSITIVE cannot be combined with row locking") {
			return
		}
	}
	w.text("DECLARE ")
	w.identifierPart(b.name)
	if b.binary {
		w.text(" BINARY")
	}
	if b.insensitive {
		w.text(" INSENSITIVE")
	}
	switch b.scroll {
	case CursorScrollUnspecified:
	case CursorScroll:
		w.text(" SCROLL")
	case CursorNoScroll:
		w.text(" NO SCROLL")
	}
	w.text(" CURSOR")
	if b.hasHold {
		if b.hold {
			w.text(" WITH HOLD")
		} else {
			w.text(" WITHOUT HOLD")
		}
	}
	w.text(" FOR ")
	w.statement(b.query)
}

// SelectIntoBuilder is the terminal SELECT ... INTO form. Its source stays
// a normal SelectBuilder, and destination options belong only to this value.
type SelectIntoBuilder struct {
	source      *SelectBuilder
	destination selectIntoTarget
}

// Temporary makes the SELECT INTO destination temporary.
func (b *SelectIntoBuilder) Temporary() *SelectIntoBuilder {
	b.destination.persistence = utilityTemporary
	return b
}

// Unlogged makes the SELECT INTO destination unlogged.
func (b *SelectIntoBuilder) Unlogged() *SelectIntoBuilder {
	b.destination.persistence = utilityUnlogged
	return b
}

func (b *SelectIntoBuilder) append(w *renderer) {
	const clause = "SELECT INTO"
	if !w.require(b.source != nil, clause, "requires a SELECT source") {
		return
	}
	if !w.enter() {
		return
	}
	defer func() { w.depth-- }()
	b.source.appendInto(w, &selectIntoDestination{destination: b.destination, clause: clause})
}

// selectIntoDestination is a small renderer-only view of the destination.
// Keeping this separate from SelectBuilder makes INTO impossible to retain on
// a reusable rowset.
type selectIntoTarget struct {
	name        string
	persistence utilityPersistence
}

type selectIntoDestination struct {
	clause      string
	destination selectIntoTarget
}

func (d *selectIntoDestination) append(w *renderer) {
	destination := &d.destination
	w.text(" INTO ")
	switch destination.persistence {
	case utilityLogged:
	case utilityTemporary:
		w.text("TEMPORARY ")
	case utilityUnlogged:
		w.text("UNLOGGED ")
	default:
		w.fail(ErrInvalid, d.clause, "unknown destination persistence")
		return
	}
	if !w.require(destination.name != "", d.clause, "requires a destination name") {
		return
	}
	w.identifierPath(destination.name, false)
}

// queryUtilitySource is shared by CTAS's rowset and EXECUTE forms. It keeps
// the source role sealed while allowing Clone to preserve shared subgraphs.
type queryUtilitySource struct {
	query         Rowset
	execute       *ExecuteBuilder
	executeSource bool
}

func (s queryUtilitySource) statement() Statement {
	if s.executeSource {
		return s.execute
	}
	return s.query
}

func (s queryUtilitySource) width(maxDepth int) int {
	if s.executeSource {
		return -1
	}
	return statementWidth(s.query, maxDepth)
}

func rowsetHasLocks(w *renderer, query Rowset, depth int) bool {
	if query == nil {
		return false
	}
	if depth >= w.options.MaxDepth {
		w.fail(ErrDepth, "DECLARE CURSOR", "query structure exceeds MaxDepth")
		return false
	}
	switch b := query.(type) {
	case *SelectBuilder:
		return b != nil && len(b.tail.locks) > 0
	case *TableBuilder:
		return b != nil && len(b.tail.locks) > 0
	case *SetBuilder:
		if b == nil {
			return false
		}
		return rowsetHasLocks(w, b.left, depth+1) || rowsetHasLocks(w, b.right, depth+1)
	default:
		return false
	}
}
