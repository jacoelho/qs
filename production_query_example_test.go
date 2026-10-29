package qs_test

import (
	"fmt"

	"github.com/jacoelho/qs"
)

func ExampleInsertInto_arrayUpsert() {
	// The driver encodes each array. Keep lengths equal and bound batch size.
	ids := []int32{42, 43}
	names := []string{"Ana", "Ben"}
	rows := qs.Select(
		qs.Unnest(qs.ArrayParam(ids, qs.TypeInt4)),
		qs.Unnest(qs.ArrayParam(names, qs.TypeText)),
	)
	query, args, err := qs.InsertInto("users").Columns("id", "name").From(rows).
		OnConflict(qs.ConflictColumns("id").TargetWhere(qs.IsNull("deleted_at")).
			DoUpdate(qs.Set("name", qs.Write(qs.Excluded("name"))))).
		ReturningCols("id", "name").ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(query)
	fmt.Println(args)
	// Output:
	// INSERT INTO "users" ("id", "name") SELECT unnest(($1)::integer[]), unnest(($2)::text[]) ON CONFLICT ("id") WHERE ("deleted_at" IS NULL) DO UPDATE SET "name" = "excluded"."name" RETURNING "id", "name"
	// [[42 43] [Ana Ben]]
}

func ExampleSelectBuilder_GroupByExpr_parameterized() {
	bucket := qs.DateTrunc(qs.Param("day"), qs.Col("created_at"))
	query, args, err := qs.Select(bucket.As("bucket"), qs.CountAll()).From("events").
		GroupByExpr(qs.Ordinal(1)).OrderBy(qs.Ordinal(1).Asc()).ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(query)
	fmt.Println(args)
	// Output:
	// SELECT date_trunc($1, "created_at") AS "bucket", count(*) FROM "events" GROUP BY 1 ORDER BY 1 ASC
	// [day]
}
