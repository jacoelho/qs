package parsetree

import (
	"reflect"
	"slices"

	qs "github.com/jacoelho/qs"
)

// insertTarget is the source-selection method set shared by an INSERT target
// with and without a column list.
type insertTarget interface {
	DefaultValues() qs.InsertDefaults
	From(source qs.Rowset) qs.InsertSelect
	Rows(rows ...[]qs.WriteValue) qs.InsertRows
}

func (b *builder) insert(n node) qs.Statement {
	table := qs.InsertInto(b.table(n.record("relation")))
	if columns := n.nodes("cols"); len(columns) != 0 {
		return insertInto(b, n, b.insertColumns(table, columns))
	}
	return insertInto(b, n, table)
}

func insertInto[T insertTarget](b *builder, n node, target T) qs.Statement {
	source := n.node("selectStmt")
	override := n.text("override")
	switch {
	case !source.present():
		if override != "OVERRIDING_NOT_SET" {
			b.gapf("DEFAULT VALUES cannot specify OVERRIDING")
		}
		insert := target.DefaultValues()
		return finishInsert(b, n, insert, qs.InsertDefaults.OnConflict[qs.ConflictNothing], qs.InsertDefaults.OnConflict[qs.ConflictUpdate], qs.InsertDefaults.Returning[qs.Expr])
	case source.tag != tagSelect:
		b.gapf("INSERT source is not SelectStmt")
		return placeholder
	case valuesOnly(source):
		rows := source.nodes("valuesLists")
		if len(rows) == 0 {
			b.gapf("INSERT has empty VALUES")
			return placeholder
		}
		insert := target.Rows(b.writeValues(rows[0].members()))
		for _, row := range rows[1:] {
			insert = insert.Rows(b.writeValues(row.members()))
		}
		switch override {
		case overridingSystemValue:
			insert = insert.OverridingSystemValue()
		case overridingUserValue:
			insert = insert.OverridingUserValue()
		}
		return finishInsert(b, n, insert, qs.InsertRows.OnConflict[qs.ConflictNothing], qs.InsertRows.OnConflict[qs.ConflictUpdate], qs.InsertRows.Returning[qs.Expr])
	default:
		insert := target.From(b.selectStmt(source))
		switch override {
		case overridingSystemValue:
			insert = insert.OverridingSystemValue()
		case overridingUserValue:
			insert = insert.OverridingUserValue()
		}
		return finishInsert(b, n, insert, qs.InsertSelect.OnConflict[qs.ConflictNothing], qs.InsertSelect.OnConflict[qs.ConflictUpdate], qs.InsertSelect.Returning[qs.Expr])
	}
}

// valuesOnly reports a bare VALUES list, the only source whose rows may
// hold DEFAULT. A decorated VALUES is an ordinary query source.
func valuesOnly(source node) bool {
	if !source.has("valuesLists") {
		return false
	}
	return !slices.ContainsFunc([]string{"withClause", "sortClause", "limitOffset", "limitCount", "lockingClause"}, source.has)
}

func (b *builder) insertColumns(table qs.InsertTarget, columns []node) qs.InsertColumnsTarget {
	plain := true
	for _, column := range columns {
		plain = plain && !column.has("indirection")
	}
	if plain {
		names := make([]string, len(columns))
		for index, column := range columns {
			names[index] = column.text("name")
		}
		return table.Columns(names...)
	}
	// Target expressions keep composite fields and array subscripts.
	targets := make([]qs.Expr, len(columns))
	for index, column := range columns {
		targets[index] = b.writeTarget(column)
	}
	return table.Columns(targets...)
}

// insertTail is the method set shared by every completed INSERT form.
type insertTail[T any] interface {
	qs.Statement
	commonTables[T]
	returningRows[T]
}

// finishInsert applies the clauses common to all INSERT sources. OnConflict
// and Returning are generic methods, which no interface can list, so their
// instantiations arrive as functions.
func finishInsert[T insertTail[T]](
	b *builder,
	n node,
	insert T,
	onNothing func(T, qs.ConflictNothing) T,
	onUpdate func(T, qs.ConflictUpdate) T,
	returning func(T, ...qs.Expr) T,
) qs.Statement {
	if clause := n.record("onConflictClause"); clause.present() {
		nothing, update, isUpdate := b.conflict(clause)
		if isUpdate {
			insert = onUpdate(insert, update)
		} else {
			insert = onNothing(insert, nothing)
		}
	}
	insert = returnRows(b, insert, returning, n)
	return withQueries(b, insert, n)
}

// conflictTarget is the non-generic part of the action method set of targets
// that allow DO UPDATE; conflictAction takes DoUpdate as a function.
type conflictTarget interface {
	DoNothing() qs.ConflictNothing
}

func (b *builder) conflict(n node) (qs.ConflictNothing, qs.ConflictUpdate, bool) {
	infer := n.record("infer")
	switch {
	case !infer.present():
		if n.text("action") != "ONCONFLICT_NOTHING" {
			b.gapf("ON CONFLICT DO UPDATE has no target")
		}
		return qs.AnyConflict().DoNothing(), qs.ConflictUpdate{}, false
	case infer.text("conname") != "":
		return conflictAction(b, n, qs.ConflictConstraint(infer.text("conname")), qs.ConflictConstraintTarget.DoUpdate[[]qs.Assignment])
	default:
		return b.conflictInference(n, infer)
	}
}

// conflictAction completes an inferred conflict target with its action.
func conflictAction[T conflictTarget](b *builder, n node, target T, doUpdate func(T, []qs.Assignment, ...qs.Assignment) qs.ConflictUpdate) (qs.ConflictNothing, qs.ConflictUpdate, bool) {
	if b.stopped() {
		return qs.ConflictNothing{}, qs.ConflictUpdate{}, false
	}
	if n.text("action") == "ONCONFLICT_NOTHING" {
		return target.DoNothing(), qs.ConflictUpdate{}, false
	}
	assignments := b.assignments(n.nodes("targetList"))
	if len(assignments) == 0 {
		b.gapf("ON CONFLICT DO UPDATE has no assignments")
		return qs.ConflictNothing{}, qs.ConflictUpdate{}, false
	}
	update := doUpdate(target, assignments)
	if where := n.node("whereClause"); where.present() {
		update = update.Where(b.condition(where))
	}
	return qs.ConflictNothing{}, update, true
}

func (b *builder) conflictInference(n, infer node) (qs.ConflictNothing, qs.ConflictUpdate, bool) {
	elements := infer.nodes("indexElems")
	if len(elements) == 0 {
		b.gapf("empty conflict inference")
		return qs.ConflictNothing{}, qs.ConflictUpdate{}, false
	}
	plain := true
	for _, element := range elements {
		if element.text("ordering") != "SORTBY_DEFAULT" || element.text("nulls_ordering") != "SORTBY_NULLS_DEFAULT" {
			b.gapf("conflict index element ordering")
			return qs.ConflictNothing{}, qs.ConflictUpdate{}, false
		}
		plain = plain && element.text("name") != "" && !element.has("expr") && !element.has("collation") && !element.has("opclass")
	}
	where := infer.node("whereClause")
	// ConflictColumns is the shorthand for an entirely plain name list.
	if plain {
		names := make([]string, len(elements))
		for index, element := range elements {
			names[index] = element.text("name")
		}
		target := qs.ConflictColumns(names)
		if where.present() {
			target = target.TargetWhere(b.condition(where))
		}
		return conflictAction(b, n, target, qs.ConflictColumnsTarget.DoUpdate[[]qs.Assignment])
	}
	index := make([]qs.IndexElement, len(elements))
	for position, element := range elements {
		var item qs.IndexElement
		switch {
		case element.text("name") != "":
			item = qs.IndexColumn(element.text("name"))
		case element.has("expr"):
			item = qs.IndexExpr(b.expr(element.node("expr")))
		default:
			b.gapf("conflict index element has no expression")
			return qs.ConflictNothing{}, qs.ConflictUpdate{}, false
		}
		if collation := element.strings("collation"); len(collation) != 0 {
			item = item.Collate(collation[0], collation[1:]...)
		}
		if class := element.strings("opclass"); len(class) != 0 {
			item = item.OpClass(class[0], class[1:]...)
		}
		index[position] = item
	}
	target := qs.ConflictIndex(index)
	if where.present() {
		target = target.TargetWhere(b.condition(where))
	}
	return conflictAction(b, n, target, qs.ConflictIndexTarget.DoUpdate[[]qs.Assignment])
}

// returningRows is the non-generic part of the RETURNING method set of every
// data-modifying statement; returnRows takes Returning as a function.
type returningRows[T any] interface {
	ReturningRows(aliases qs.ReturningAliases) T
}

func returnRows[T returningRows[T]](b *builder, statement T, returning func(T, ...qs.Expr) T, n node) T {
	clause := n.record("returningClause")
	if !clause.present() {
		return statement
	}
	var aliases qs.ReturningAliases
	for _, option := range clause.nodes("options") {
		alias := option.text("value")
		var slot *string
		switch kind := option.text("option"); kind {
		case "RETURNING_OPTION_OLD":
			// ReturningAliases renders OLD before NEW.
			if aliases.New != "" {
				b.gapf("RETURNING row options are in unsupported order")
			}
			slot = &aliases.Old
		case "RETURNING_OPTION_NEW":
			slot = &aliases.New
		default:
			b.gapf("RETURNING row option %s", kind)
			return statement
		}
		switch {
		case *slot != "":
			b.gapf("duplicate RETURNING row option")
		case alias == "":
			b.gapf("RETURNING row option has no alias")
		}
		*slot = alias
	}
	if aliases != (qs.ReturningAliases{}) {
		statement = statement.ReturningRows(aliases)
	}
	if targets := clause.nodes("exprs"); len(targets) != 0 {
		statement = returning(statement, b.projection(targets, "RETURNING")...)
	}
	return statement
}

// pendingMutation is the row-selection method set of a pending UPDATE or
// DELETE, each of which completes the statement T.
type pendingMutation[T any] interface {
	Where(first qs.Condition, rest ...qs.Condition) T
	WhereCurrentOf(cursor string) T
	AllRows() T
}

func filter[T any, P pendingMutation[T]](b *builder, statement P, n node) T {
	where := n.node("whereClause")
	switch {
	case !where.present():
		// qs has no statement for a mutation until its rows are selected.
		return statement.AllRows()
	case where.tag == tagConstant && where.record("boolval").flag("boolval"):
		// qs spells every row as AllRows, which renders no WHERE, and refuses
		// TRUE as the only condition.
		b.gapf("mutation with WHERE TRUE; AllRows renders no WHERE")
		return statement.AllRows()
	case where.tag == "CurrentOfExpr":
		cursor := where.text("cursor_name")
		if cursor == "" {
			b.gapf("CURRENT OF has no cursor name")
		}
		return statement.WhereCurrentOf(cursor)
	default:
		return statement.Where(b.condition(where))
	}
}

func (b *builder) update(n node) qs.Statement {
	assignments := b.assignments(n.nodes("targetList"))
	if len(assignments) == 0 {
		b.gapf("UPDATE has no assignments")
	}
	update := qs.Update(b.table(n.record("relation"))).Set(assignments)
	if from := n.nodes("fromClause"); len(from) != 0 {
		update = update.From(b.relations(from)...)
	}
	return withQueries(b, returnRows(b, filter(b, update, n), qs.UpdateBuilder.Returning[qs.Expr], n), n)
}

func (b *builder) delete(n node) qs.Statement {
	statement := qs.DeleteFrom(b.table(n.record("relation")))
	if using := n.nodes("usingClause"); len(using) != 0 {
		statement = statement.Using(b.relations(using)...)
	}
	return withQueries(b, returnRows(b, filter(b, statement, n), qs.DeleteBuilder.Returning[qs.Expr], n), n)
}

func (b *builder) merge(n node) qs.Statement {
	merge := qs.MergeInto(b.table(n.record("relation"))).Using(b.relation(n.node("sourceRelation")))
	condition := n.node("joinCondition")
	if !condition.present() {
		b.gapf("MERGE has no join condition")
		return placeholder
	}
	merge = merge.On(b.condition(condition))
	clauses := n.nodes("mergeWhenClauses")
	if len(clauses) == 0 {
		b.gapf("MERGE has no WHEN branches")
		return placeholder
	}
	branches := make([]qs.MergeWhen, len(clauses))
	for index, clause := range clauses {
		branches[index] = b.mergeBranch(clause)
	}
	merge = returnRows(b, merge.When(branches...), qs.MergeBuilder.Returning[qs.Expr], n)
	if with := n.record("withClause"); with.present() {
		if with.flag("recursive") {
			b.gapf("MERGE has no WITH RECURSIVE")
		}
		merge = merge.With(b.ctes(with)...)
	}
	return merge
}

func (b *builder) mergeBranch(n node) qs.MergeWhen {
	action := n.text("commandType")
	switch kind := n.text("matchKind"); kind {
	case "MERGE_WHEN_MATCHED", "MERGE_WHEN_NOT_MATCHED_BY_SOURCE":
		branch := qs.Matched()
		if kind == "MERGE_WHEN_NOT_MATCHED_BY_SOURCE" {
			branch = qs.NotMatchedBySource()
		}
		if condition := n.node("condition"); condition.present() {
			branch = branch.And(b.condition(condition))
		}
		switch action {
		case "CMD_UPDATE":
			return branch.ThenUpdate(b.assignments(n.nodes("targetList")))
		case "CMD_DELETE":
			return branch.ThenDelete()
		case "CMD_NOTHING":
			return branch.ThenDoNothing()
		}
	case "MERGE_WHEN_NOT_MATCHED_BY_TARGET":
		branch := qs.NotMatchedByTarget()
		if condition := n.node("condition"); condition.present() {
			branch = branch.And(b.condition(condition))
		}
		switch action {
		case "CMD_NOTHING":
			return branch.ThenDoNothing()
		case "CMD_INSERT":
			return b.mergeInsert(branch, n)
		}
	default:
		b.gapf("MERGE match kind %s", kind)
		return qs.MergeWhen{}
	}
	b.gapf("MERGE action %s", action)
	return qs.MergeWhen{}
}

// mergeInserter is the non-generic part of the method set shared by a NOT
// MATCHED branch with and without an OVERRIDING clause; ThenInsert is a
// generic method, so mergeInsert carries it as a function.
type mergeInserter interface {
	ThenInsertValues(columns []string, first qs.WriteValue, rest ...qs.WriteValue) qs.MergeWhen
}

// mergeInsert reads MERGE's INSERT, which stores target names in targetList
// and their values separately, unlike a plain INSERT.
func (b *builder) mergeInsert(branch qs.MergeNotMatched, n node) qs.MergeWhen {
	targets := n.nodes("targetList")
	values := b.writeValues(n.nodes("values"))
	var inserter mergeInserter = branch
	thenInsert := func(assignments []qs.Assignment) qs.MergeWhen { return branch.ThenInsert(assignments) }
	overridden := func(override qs.MergeInsertOverride) {
		inserter = override
		thenInsert = func(assignments []qs.Assignment) qs.MergeWhen { return override.ThenInsert(assignments) }
	}
	switch override := n.text("override"); override {
	case overridingSystemValue:
		overridden(branch.OverridingSystemValue())
	case overridingUserValue:
		overridden(branch.OverridingUserValue())
	case "OVERRIDING_NOT_SET":
		if len(targets) == 0 && len(values) == 0 {
			return branch.ThenInsertDefault()
		}
	}
	switch {
	case len(targets) == 0 && len(values) == 0:
		b.gapf("MERGE INSERT DEFAULT VALUES with OVERRIDING")
		return qs.MergeWhen{}
	case len(values) == 0, len(targets) != 0 && len(targets) != len(values):
		b.gapf("MERGE INSERT target/value width differs")
		return qs.MergeWhen{}
	}
	plain := true
	for _, target := range targets {
		plain = plain && !target.has("indirection")
	}
	if plain {
		columns := make([]string, len(targets))
		for index, target := range targets {
			columns[index] = target.text("name")
		}
		return inserter.ThenInsertValues(columns, values[0], values[1:]...)
	}
	assignments := make([]qs.Assignment, len(targets))
	for index, target := range targets {
		assignments[index] = qs.Assign(b.writeTarget(target), values[index])
	}
	return thenInsert(assignments)
}

// assignments builds SET lists. A row assignment (a, b) = ... arrives as one
// target per column, each repeating the shared source.
func (b *builder) assignments(targets []node) []qs.Assignment {
	var assignments []qs.Assignment
	for index := 0; index < len(targets); {
		target := targets[index]
		reference := target.node("val")
		if reference.tag != "MultiAssignRef" {
			assignments = append(assignments, qs.Assign(b.writeTarget(target), b.writeValue(reference)))
			index++
			continue
		}
		width := int(reference.integer("ncolumns"))
		if width < 1 || index+width > len(targets) {
			b.gapf("complex row assignment target")
			return nil
		}
		group := targets[index : index+width]
		source := reference.node("source")
		columns := make([]qs.Expr, width)
		for column, item := range group {
			member := item.node("val")
			if member.tag != "MultiAssignRef" || member.integer("colno") != int64(column+1) ||
				member.integer("ncolumns") != int64(width) || !reflect.DeepEqual(member.fields["source"], reference.fields["source"]) {
				b.gapf("complex row assignment target")
				return nil
			}
			columns[column] = b.writeTarget(item)
		}
		switch {
		case source.tag == "SubLink" && source.text("subLinkType") == "EXPR_SUBLINK":
			assignments = append(assignments, qs.AssignRowFrom(columns, b.rowset(source.node("subselect"))))
		case source.tag == tagRow:
			assignments = append(assignments, qs.AssignRow(columns, b.writeRow(source)))
		default:
			b.gapf("row assignment source %s", source.tag)
			return nil
		}
		index += width
	}
	return assignments
}

// writeTarget builds an assignment target with its field and subscript steps.
func (b *builder) writeTarget(n node) qs.Expr {
	name := n.text("name")
	if name == "" {
		b.gapf("assignment target has no name")
		return qs.Expr{}
	}
	target := qs.Col(name)
	for _, step := range n.nodes("indirection") {
		switch step.tag {
		case tagString:
			target = target.Field(step.text("sval"))
		case "A_Indices":
			target = b.subscript(target, step)
		default:
			b.gapf("composite assignment target")
			return qs.Expr{}
		}
	}
	return target
}

// writeValue keeps DEFAULT, which is valid only directly in a write slot.
func (b *builder) writeValue(n node) qs.WriteValue {
	if n.tag == "SetToDefault" {
		return qs.Default()
	}
	return qs.Write(b.expr(n))
}

func (b *builder) writeValues(items []node) []qs.WriteValue {
	values := make([]qs.WriteValue, len(items))
	for index, item := range items {
		values[index] = b.writeValue(item)
	}
	return values
}

// writeRow keeps ROW(...) and bare (...) apart, as row does for expressions.
func (b *builder) writeRow(n node) qs.WriteRowValue {
	values := b.writeValues(n.nodes("args"))
	implicit := n.text("row_format") == "COERCE_IMPLICIT_CAST"
	switch {
	case len(values) == 0:
		b.gapf("empty row write")
	case !implicit:
		return qs.WriteRow(values[0], values[1:]...)
	case len(values) < 2:
		b.gapf("tuple write requires at least two values")
	default:
		return qs.WriteTuple(values[0], values[1], values[2:]...)
	}
	return qs.WriteRowValue{}
}
