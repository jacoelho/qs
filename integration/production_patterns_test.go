//go:build postgres

package integration_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/jacoelho/qs"
)

func TestUnusedWindowAllowsRowLock(t *testing.T) {
	t.Parallel()
	session := connect(t)
	if _, err := session.Exec(session, `CREATE TEMP TABLE jobs (id integer PRIMARY KEY);
		INSERT INTO jobs VALUES (42)`); err != nil {
		t.Fatal(err)
	}
	statement := qs.SelectCols("id").From("jobs").
		Window("unused", qs.Window().OrderBy(qs.Asc("id"))).
		Lock(qs.ForUpdate())
	sql, args, err := session.render(t, statement)
	if err != nil {
		t.Fatal(err)
	}
	var id int
	if err := session.QueryRow(session, sql, args...).Scan(&id); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if id != 42 {
		t.Fatalf("got id %d; want 42", id)
	}
}

func TestNestedWindowAllowsOuterRowLock(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		query func() qs.Statement
	}{
		{
			"scalar",
			func() qs.Statement {
				return qs.Select(qs.Col("id"), qs.Scalar(qs.Select(qs.RowNumber().Over(qs.Window())))).
					From("jobs").Lock(qs.ForUpdate())
			},
		},
		{
			"cte",
			func() qs.Statement {
				ranked := qs.CTE("ranked", qs.Select(qs.RowNumber().Over(qs.Window()).As("position")))
				return qs.Select(qs.Col("id"), qs.Scalar(qs.SelectCols("position").FromExpr(ranked.Ref()))).
					With(ranked).From("jobs").Lock(qs.ForUpdate())
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := connect(t)
			if _, err := session.Exec(session, `CREATE TEMP TABLE jobs (id integer PRIMARY KEY);
				INSERT INTO jobs VALUES (42)`); err != nil {
				t.Fatal(err)
			}
			sql, args, err := session.render(t, tc.query())
			if err != nil {
				t.Fatal(err)
			}
			var id, position int
			if err := session.QueryRow(session, sql, args...).Scan(&id, &position); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			if id != 42 || position != 1 {
				t.Fatalf("got id %d, position %d; want 42, 1", id, position)
			}
		})
	}
}

// River combines array-fed insertion with partial-index conflict inference:
// https://github.com/riverqueue/river/blob/5c2d873192b4fb41c4b1320341f28408751532d5/riverdriver/riverpgxv5/internal/dbsqlc/river_job.sql#L278-L336
func TestColumnarPartialIndexUpsert(t *testing.T) {
	t.Parallel()
	session := connect(t)
	if _, err := session.Exec(session, `CREATE TEMP TABLE jobs (
		id integer NOT NULL, name text NOT NULL, deleted_at timestamptz);
		CREATE UNIQUE INDEX jobs_active_id ON jobs (id) WHERE deleted_at IS NULL;
		INSERT INTO jobs VALUES (1, 'old', NULL),
			(1, 'archived one', '2024-01-01'), (2, 'archived two', '2024-01-01')`); err != nil {
		t.Fatal(err)
	}
	upsert := func(ids []int32, names []string) *qs.InsertSelect {
		return qs.InsertInto("jobs").Columns("id", "name").From(qs.Select(
			qs.Unnest(qs.ArrayParam(ids, qs.TypeInt4)),
			qs.Unnest(qs.ArrayParam(names, qs.TypeText)),
		)).OnConflict(qs.ConflictColumns("id").TargetWhere(qs.IsNull("deleted_at")).
			DoUpdate(qs.Set("name", qs.Write(qs.Excluded("name"))))).
			ReturningCols("id", "name")
	}
	sql, args, err := session.render(t, upsert([]int32{1, 2, 3}, []string{"updated", "new two", "new three"}))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := session.Query(session, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	got := make(map[int]string)
	count := 0
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		got[id] = name
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[int]string{1: "updated", 2: "new two", 3: "new three"}
	if count != 3 || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %d rows %#v; want %#v", count, got, want)
	}
	archived := queryStrings(t, session, qs.SelectCols("name").From("jobs").
		Where(qs.IsNotNull("deleted_at")).OrderBy(qs.Asc("id")))
	if !reflect.DeepEqual(archived, []string{"archived one", "archived two"}) {
		t.Fatalf("archived rows changed: %#v", archived)
	}
	sql, args, err = session.render(t, upsert([]int32{}, []string{}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Exec(session, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if result.RowsAffected() != 0 {
		t.Fatalf("empty input affected %d rows", result.RowsAffected())
	}
}

// Coder pages parent rows before correlated lateral aggregation:
// https://github.com/coder/coder/blob/1acacc9c307e5d30e8b85fd4b821770a40422d85/coderd/database/queries/aibridge.sql#L354-L559
func TestPaginateBeforeLateralAggregation(t *testing.T) {
	t.Parallel()
	session := connect(t)
	if _, err := session.Exec(session, `CREATE TEMP TABLE parents (id integer PRIMARY KEY);
		CREATE TEMP TABLE events (parent_id integer NOT NULL, amount integer NOT NULL);
		CREATE TEMP TABLE comments (parent_id integer NOT NULL, body text NOT NULL);
		INSERT INTO parents VALUES (1), (2), (3), (4);
		INSERT INTO events VALUES (1, 100), (1, 200), (1, 300),
			(2, 11), (2, 12), (2, 1), (4, 400);
		INSERT INTO comments VALUES (1, 'outside page'),
			(2, 'first'), (2, 'second'), (4, 'after page')`); err != nil {
		t.Fatal(err)
	}
	page := qs.CTE("page", qs.SelectCols("id").From("parents").
		Where(qs.Gt("id", 1)).OrderBy(qs.Asc("id")).Limit(2))
	summary := qs.Select(
		qs.Sum(qs.Col("amount")).Filter(qs.Gt("amount", 10)).As("total"),
		qs.CountAll().As("entries"),
	).From("events").Where(qs.EqColumns("events.parent_id", "page.id")).
		GroupBy("events.parent_id")
	comments := qs.Select(qs.CountAll().As("entries")).From("comments").
		Where(qs.EqColumns("comments.parent_id", "page.id")).GroupBy("comments.parent_id")
	statement := qs.Select(qs.Col("page.id"),
		qs.Coalesce(qs.Col("summary.total"), qs.LiteralInt(0)),
		qs.Coalesce(qs.Col("summary.entries"), qs.LiteralInt(0)),
		qs.Coalesce(qs.Col("comment_summary.entries"), qs.LiteralInt(0)),
	).With(page).FromExpr(page.Ref()).
		LeftJoinExpr(qs.Lateral(summary.As("summary")), qs.True()).
		LeftJoinExpr(qs.Lateral(comments.As("comment_summary")), qs.True()).
		OrderBy(qs.Asc("page.id"))
	sql, args, err := session.render(t, statement)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := session.Query(session, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	var got [][4]int64
	for rows.Next() {
		var row [4]int64
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3]); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := [][4]int64{{2, 23, 3, 2}, {3, 0, 0, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got page %#v; want %#v", got, want)
	}
}

func TestGroupParameterizedExpressionByOrdinal(t *testing.T) {
	t.Parallel()
	session := connect(t)
	if _, err := session.Exec(session, `CREATE TEMP TABLE events (created_at timestamptz NOT NULL);
		INSERT INTO events VALUES ('2024-01-01 01:00:00+00'),
			('2024-01-01 23:00:00+00'), ('2024-01-02 12:00:00+00')`); err != nil {
		t.Fatal(err)
	}
	bucket := qs.DateTrunc(qs.Param("day"), qs.Col("created_at"))
	statement := qs.Select(bucket.As("bucket"), qs.CountAll()).From("events").
		GroupByExpr(qs.Ordinal(1)).OrderBy(qs.Ordinal(1).Asc())
	sql, args, err := session.render(t, statement)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := session.Query(session, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	type dayCount struct {
		day   string
		count int64
	}
	var got []dayCount
	for rows.Next() {
		var day time.Time
		var count int64
		if err := rows.Scan(&day, &count); err != nil {
			t.Fatal(err)
		}
		got = append(got, dayCount{day: day.UTC().Format(time.DateOnly), count: count})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []dayCount{{"2024-01-01", 2}, {"2024-01-02", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}
