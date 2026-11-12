package qs_test

import (
	"fmt"

	"github.com/jacoelho/qs"
)

func ExampleInsertInto_arrayUpsert() {
	// The driver encodes each array. Keep lengths equal and bound batch size.
	ids := []int32{42, 43}
	names := []string{"Ana", "Ben"}
	query, args, err := qs.InsertInto("users").
		FromArrays(
			qs.NamedArray("id", qs.ArrayParam(ids, qs.TypeInt4)),
			qs.NamedArray("name", qs.ArrayParam(names, qs.TypeText)),
		).
		OnConflict(
			qs.ConflictColumns("id").
				TargetWhere(qs.Col("deleted_at").IsNull()).
				DoUpdate(qs.Set("name", qs.Write(qs.Excluded("name")))),
		).
		Returning("id", "name").
		ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(query)
	fmt.Println(args)
	// Output:
	// INSERT INTO "users" ("id", "name") SELECT * FROM unnest(($1)::integer[], ($2)::text[]) ON CONFLICT ("id") WHERE ("deleted_at" IS NULL) DO UPDATE SET "name" = "excluded"."name" RETURNING "id", "name"
	// [[42 43] [Ana Ben]]
}

func ExampleSelectBuilder_GroupBy_parameterized() {
	bucket := qs.DateTrunc(qs.Param("day"), qs.Col("created_at"))
	query, args, err := qs.Select(bucket.As("bucket"), qs.CountAll()).
		From("events").
		GroupBy(qs.Ordinal(1)).
		OrderBy(qs.Ordinal(1).Asc()).
		ToSQL()
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
	feed := qs.Select("id", "created_at").
		From("posts").
		Where(qs.Col("author_id").Eq(qs.Param(7)))
	order := []qs.Order{qs.Col("posts", "created_at").Desc(), qs.Col("posts", "id").Desc()}
	// Seek after the last row shown: created_at 1700000000, id 42.
	next := feed.OrderBy(order...).SeekAfter(int64(1700000000), int64(42)).Limit(20)
	// Seek after the first row shown in reverse order, then reverse the rows.
	previous := feed.OrderBy(order[0].Reverse(), order[1].Reverse()).
		SeekAfter(int64(1700000500), int64(61)).
		Limit(20)
	// The count omits the ORDER BY clause, the cursor and LIMIT. Column keys
	// cannot change the rows, so none move into the counted projection.
	total := qs.CountRows(next)
	for _, q := range []qs.SelectBuilder{next, previous, total} {
		query, args, err := q.ToSQL()
		fmt.Println(query)
		fmt.Println(args, err)
	}
	// Output:
	// SELECT "id", "created_at" FROM "posts" WHERE ("author_id" = $1) AND (("posts"."created_at", "posts"."id") < ($2, $3)) ORDER BY "posts"."created_at" DESC, "posts"."id" DESC LIMIT $4
	// [7 1700000000 42 20] <nil>
	// SELECT "id", "created_at" FROM "posts" WHERE ("author_id" = $1) AND (("posts"."created_at", "posts"."id") > ($2, $3)) ORDER BY "posts"."created_at" ASC, "posts"."id" ASC LIMIT $4
	// [7 1700000500 61 20] <nil>
	// SELECT count(*) FROM (SELECT "id", "created_at" FROM "posts" WHERE ("author_id" = $1)) AS "matching"
	// [7] <nil>
}
