package parsetree

import (
	qs "github.com/jacoelho/qs"
)

func (b *builder) relations(items []node) []qs.Relation {
	relations := make([]qs.Relation, len(items))
	for index, item := range items {
		relations[index] = b.relation(item)
	}
	return relations
}

func (b *builder) relation(n node) qs.Relation {
	if b.stopped() {
		return qs.Relation{}
	}
	switch n.tag {
	case "RangeVar":
		return b.table(n)
	case "RangeSubselect":
		return b.derivedTable(n)
	case "RangeFunction":
		return b.tableFunction(n)
	case "RangeTableSample":
		return b.tableSample(n)
	case "JoinExpr":
		return b.join(n)
	case "JsonTable":
		return b.jsonTable(n)
	default:
		b.gapf("unsupported relation node %s", n.tag)
		return qs.Relation{}
	}
}

// table accepts a tagged RangeVar and the untagged target of a write.
func (b *builder) table(n node) qs.Relation {
	var parts []string
	for _, field := range []string{"catalogname", "schemaname", "relname"} {
		if part := n.text(field); part != "" {
			parts = append(parts, part)
		}
	}
	relation := qualified(b, "relation", qs.Table, parts)
	if !n.flag("inh") {
		relation = relation.Only()
	}
	return b.aliased(relation, n)
}

// aliased applies the node's optional alias and column aliases.
func (b *builder) aliased(relation qs.Relation, n node) qs.Relation {
	alias := n.record("alias")
	if !alias.present() {
		return relation
	}
	return relation.As(alias.text("aliasname"), alias.strings("colnames")...)
}

func (b *builder) lateral(relation qs.Relation, n node) qs.Relation {
	if n.flag("lateral") {
		return qs.Lateral(relation)
	}
	return relation
}

func (b *builder) derivedTable(n node) qs.Relation {
	query := b.rowset(n.node("subquery"))
	alias := n.record("alias")
	if !alias.present() {
		return b.lateral(qs.Derived(query), n)
	}
	return b.lateral(qs.Subquery(query, alias.text("aliasname"), alias.strings("colnames")...), n)
}

func (b *builder) tableFunction(n node) qs.Relation {
	items := n.nodes("functions")
	if len(items) == 0 {
		b.gapf("empty table function list")
		return qs.Relation{}
	}
	// Each member pairs a call with its optional column definition list.
	calls := make([]qs.Expr, len(items))
	definitions := make([][]qs.RecordColumn, len(items))
	for index, item := range items {
		pair := item.members()
		if len(pair) != 2 {
			b.gapf("table function member has %d parts", len(pair))
			return qs.Relation{}
		}
		calls[index] = b.expr(pair[0])
		if pair[1].tag == "List" {
			definitions[index] = b.columnDefinitions(pair[1].members())
		}
	}
	// A relation-level definition list belongs to the alias, not to a
	// ROWS FROM member; qs models it on the single-function relation.
	relationDefinitions := n.nodes("coldeflist")
	single := len(items) == 1 && !n.flag("is_rowsfrom")
	var relation qs.Relation
	switch {
	case len(relationDefinitions) != 0:
		if !single || definitions[0] != nil {
			b.gapf("table function relation column definitions have unsupported shape")
			return qs.Relation{}
		}
		relation = qs.TableFunc(calls[0]).DefineColumns(b.columnDefinitions(relationDefinitions)...)
	case single:
		relation = qs.TableFunc(calls[0])
		if definitions[0] != nil {
			relation = relation.DefineColumns(definitions[0]...)
		}
	case n.flag("is_rowsfrom"):
		functions := make([]qs.RecordFunction, len(calls))
		for index, call := range calls {
			functions[index] = qs.Function(call)
			if definitions[index] != nil {
				functions[index] = functions[index].DefineColumns(definitions[index]...)
			}
		}
		relation = qs.RowsFrom(functions...)
	default:
		b.gapf("multiple table functions without ROWS FROM")
		return qs.Relation{}
	}
	if n.flag("ordinality") {
		relation = relation.WithOrdinality()
	}
	if alias := n.record("alias"); alias.present() {
		// The definition list already names the columns; qs rejects a second
		// alias column list.
		if len(relationDefinitions) != 0 {
			relation = relation.As(alias.text("aliasname"))
		} else {
			relation = relation.As(alias.text("aliasname"), alias.strings("colnames")...)
		}
	}
	return b.lateral(relation, n)
}

func (b *builder) columnDefinitions(items []node) []qs.RecordColumn {
	columns := make([]qs.RecordColumn, len(items))
	for index, item := range items {
		if item.tag != "ColumnDef" || item.text("colname") == "" || !item.has("typeName") {
			b.gapf("table function column definition is incomplete")
			return nil
		}
		columns[index] = qs.Def(item.text("colname"), b.dataType(item.record("typeName")))
	}
	return columns
}

func (b *builder) tableSample(n node) qs.Relation {
	relation := b.relation(n.node("relation"))
	method := n.strings("method")
	if len(method) != 1 {
		b.gapf("TABLESAMPLE has no single method name")
		return qs.Relation{}
	}
	relation = relation.TableSample(method[0], b.exprs(n.nodes("args"))...)
	if seed := n.node("repeatable"); seed.present() {
		relation = relation.Repeatable(b.expr(seed))
	}
	return relation
}

func (b *builder) join(n node) qs.Relation {
	left, right := b.relation(n.node("larg")), b.relation(n.node("rarg"))
	kind := n.text("jointype")
	using := n.strings("usingClause")
	condition := n.node("quals")
	var relation qs.Relation
	switch {
	case n.flag("isNatural"):
		switch kind {
		case joinInner:
			relation = qs.NaturalJoin(left, right)
		case "JOIN_LEFT":
			relation = qs.NaturalLeftJoin(left, right)
		case "JOIN_RIGHT":
			relation = qs.NaturalRightJoin(left, right)
		case "JOIN_FULL":
			relation = qs.NaturalFullJoin(left, right)
		default:
			b.gapf("join type %s", kind)
			return qs.Relation{}
		}
	case kind == joinInner && len(using) == 0 && !condition.present():
		relation = qs.CrossJoin(left, right)
	default:
		var pending qs.PendingJoin
		switch kind {
		case joinInner:
			pending = qs.InnerJoin(left, right)
		case "JOIN_LEFT":
			pending = qs.LeftJoin(left, right)
		case "JOIN_RIGHT":
			pending = qs.RightJoin(left, right)
		case "JOIN_FULL":
			pending = qs.FullJoin(left, right)
		default:
			b.gapf("join type %s", kind)
			return qs.Relation{}
		}
		switch {
		case len(using) != 0 && n.has("join_using_alias"):
			relation = pending.UsingAs(n.record("join_using_alias").text("aliasname"), using[0], using[1:]...)
		case len(using) != 0:
			relation = pending.Using(using[0], using[1:]...)
		case condition.present():
			relation = pending.On(b.condition(condition))
		default:
			b.gapf("%s has no join condition", kind)
			return qs.Relation{}
		}
	}
	return b.aliased(relation, n)
}
