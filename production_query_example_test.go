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

func ExampleSelectBuilder_SeekAfter() {
	// Every key is NOT NULL and the last key is unique, so pages are disjoint.
	feed := qs.SelectCols("id", "created_at").From("posts").Where(qs.Eq("author_id", 7))
	order := []qs.Order{qs.Desc("created_at"), qs.Desc("id")}
	// Seek after the last row shown: created_at 1700000000, id 42.
	next := feed.Clone().OrderBy(order...).SeekAfter(int64(1700000000), int64(42)).Limit(20)
	// Seek after the first row shown in reverse order, then reverse the rows.
	previous := feed.Clone().OrderBy(order[0].Reverse(), order[1].Reverse()).
		SeekAfter(int64(1700000500), int64(61)).Limit(20)
	// The count ignores ORDER BY, the cursor and LIMIT.
	total := qs.CountRows(next)
	for _, q := range []*qs.SelectBuilder{next, previous, total} {
		query, args, err := q.ToSQL()
		fmt.Println(query)
		fmt.Println(args, err)
	}
	// Output:
	// SELECT "id", "created_at" FROM "posts" WHERE ("author_id" = $1) AND (("created_at", "id") < ($2, $3)) ORDER BY "created_at" DESC, "id" DESC LIMIT $4
	// [7 1700000000 42 20] <nil>
	// SELECT "id", "created_at" FROM "posts" WHERE ("author_id" = $1) AND (("created_at", "id") > ($2, $3)) ORDER BY "created_at" ASC, "id" ASC LIMIT $4
	// [7 1700000500 61 20] <nil>
	// SELECT count(*) FROM (SELECT "id", "created_at" FROM "posts" WHERE ("author_id" = $1)) AS "matching"
	// [7] <nil>
}
