package client

import (
	"fmt"

	"github.com/jacoelho/qs"
)

type repository struct{ base qs.SelectBuilder }

func filters(tenant string, deleted bool) (string, []any, error) {
	q := qs.Select("id", "name").From("users")
	q.Where(qs.Col("tenant").Eq(qs.Param(tenant))) // want `result of qs.SelectBuilder.Where is discarded`
	if !deleted {
		q.Where(qs.Col("deleted_at").IsNull()).Limit(10) // want `result of qs.SelectBuilder.Limit is discarded`
	}
	q = q.OrderBy(qs.AscCols("name")...)
	return q.ToSQL()
}

func genericMethods(r repository, u qs.Relation) {
	r.base.From(u)                                  // want `result of qs.SelectBuilder.From is discarded`
	r.base.Join("teams", qs.True())                 // want `result of qs.SelectBuilder.Join is discarded`
	qs.InsertInto("users").Set(qs.SetDefault("id")) // want `result of qs.InsertTarget.Set is discarded`
	qs.InsertInto(u)                                // want `result of qs.InsertInto is discarded`
	(qs.Update("users").Set(qs.SetDefault("id")))   // want `result of qs.PendingUpdate.Set is discarded`
}

func values(e qs.Expr, c qs.Condition, o qs.Order, w qs.WindowSpec) {
	e.As("x")                // want `result of qs.Expr.As is discarded`
	c.And(qs.True())         // want `result of qs.Condition.And is discarded`
	o.NullsLast()            // want `result of qs.Order.NullsLast is discarded`
	w.PartitionBy(e)         // want `result of qs.WindowSpec.PartitionBy is discarded`
	qs.Col("a")              // want `result of qs.Col is discarded`
	qs.ToSQL(qs.Select("a")) // want `result of qs.ToSQL is discarded`
}

func instantiated(q qs.SelectBuilder, e qs.Expr) {
	q.Columns[qs.Expr](e)           // want `result of qs.SelectBuilder.Columns is discarded`
	qs.Select[string]("id")         // want `result of qs.Select is discarded`
	qs.ToSQL(q.Columns[qs.Expr](e)) // want `result of qs.ToSQL is discarded`
	build := q.Limit
	build(1)
}

func kept(q qs.SelectBuilder) qs.SelectBuilder {
	_ = q.Where(qs.True())
	qs.MustToSQL(q)
	narrowed := q.Where(qs.True())
	fmt.Println(narrowed.ToSQL())
	defer fmt.Println("done")
	return q.Limit(1)
}
