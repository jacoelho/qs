package projection_test

import (
	"database/sql"
	"fmt"
	"net/netip"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

func ExampleEmbedNull() {
	type User struct {
		ID    int64
		Name  string
		Email *string
	}
	type Row struct {
		Employee User
		Manager  *User
	}
	users := func(r qs.Relation, prefix string) projection.Projection[User] {
		return projection.New(
			projection.Column(r.Col("id").As(prefix+"id"), func(u *User) *int64 { return &u.ID }),
			projection.Column(
				r.Col("name").As(prefix+"name"),
				func(u *User) *string { return &u.Name },
			),
			projection.ColumnNull(
				r.Col("email").As(prefix+"email"),
				func(u *User, v *string) { u.Email = v },
			),
		)
	}
	employee := qs.Table("users").As("employee")
	manager := qs.Table("users").As("manager")
	p := projection.New(
		projection.Embed(users(employee, ""), func(r *Row) *User { return &r.Employee }),
		projection.EmbedNull(users(manager, "manager_"), func(r *Row, u *User) { r.Manager = u }),
	)
	text, args, err := qs.SelectSlice(p.Expressions()).
		FromExpr(
			qs.LeftJoin(employee, manager).On(employee.Col("manager_id").Eq(manager.Col("id"))),
		).
		ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)
	// Output:
	// SELECT "employee"."id" AS "id", "employee"."name" AS "name", "employee"."email" AS "email", "manager"."id" AS "manager_id", "manager"."name" AS "manager_name", "manager"."email" AS "manager_email" FROM ("users" AS "employee" LEFT JOIN "users" AS "manager" ON ("employee"."manager_id" = "manager"."id"))
	// []
}

func ExampleMap() {
	type Host struct {
		Name string
		Addr netip.Addr
	}
	p := projection.New(
		projection.Column(qs.Col("name"), func(h *Host) *string { return &h.Name }),
		projection.Map(
			projection.Column(qs.Col("addr"), func(s *string) *string { return s }),
			netip.ParseAddr,
			func(h *Host, v netip.Addr) { h.Addr = v },
		),
	)

	// Application code passes rows.Scan.
	scan := func(dest ...any) error {
		_, err := fmt.Sscan("web-1 192.0.2.10", dest...)
		return err
	}
	host, err := p.Scan(scan)
	if err != nil {
		panic(err)
	}
	fmt.Println(host.Name, host.Addr)
	// Output:
	// web-1 192.0.2.10
}

func ExampleProjection_Scan() {
	type Document struct {
		ID      string
		Country string
	}
	p := projection.Map(projection.New(
		projection.Column(qs.Col("id"), func(d *Document) *string { return &d.ID }),
		projection.Column(
			qs.JSONBExpr(qs.Col("payload")).TextKey("country").Expr(),
			func(d *Document) *string { return &d.Country },
		),
	), func(d Document) (string, error) { return d.ID + ":" + d.Country, nil }, func(label *string, v string) { *label = v })

	// Application code passes rows.Scan.
	scan := func(dest ...any) error {
		_, err := fmt.Sscan("document-1 PT", dest...)
		return err
	}
	label, err := p.Scan(scan)
	if err != nil {
		panic(err)
	}
	fmt.Println(label)
	// Output:
	// document-1:PT
}

func ExampleProjection_returning() {
	type Document struct {
		ID      string
		Payload string
	}
	p := projection.New(
		projection.Column(qs.Col("d", "id"), func(d *Document) *string { return &d.ID }),
		projection.Column(qs.Col("d", "payload"), func(d *Document) *string { return &d.Payload }),
	)
	text, args, err := qs.UpdateTable(qs.Table("documents").As("d")).
		Set(qs.SetNull("archived_at")).
		Where(qs.Col("d", "id").Eq(qs.Param("document-1"))).
		Returning(p.Expressions()...).
		ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)
	// Output:
	// UPDATE "documents" AS "d" SET "archived_at" = NULL WHERE ("d"."id" = $1) RETURNING "d"."id", "d"."payload"
	// [document-1]
}

// records stands in for pgx.Rows or *sql.Rows.
type records struct {
	unread  []string
	current string
}

func (r *records) Next() bool {
	if len(r.unread) == 0 {
		return false
	}
	r.current, r.unread = r.unread[0], r.unread[1:]
	return true
}

func (r *records) Scan(dest ...any) error {
	_, err := fmt.Sscan(r.current, dest...)
	return err
}

func (r *records) Err() error { return nil }
func (r *records) Close()     {}

func ExampleProjection_Collect() {
	type Document struct {
		ID      string
		Country string
	}
	p := projection.New(
		projection.Column(qs.Col("id"), func(d *Document) *string { return &d.ID }),
		projection.Column(qs.Col("country"), func(d *Document) *string { return &d.Country }),
	)

	// Application code passes the driver's rows; Collect closes them.
	rows := &records{unread: []string{"document-1 PT", "document-2 ES"}}
	documents, err := p.Collect(rows)
	if err != nil {
		panic(err)
	}
	fmt.Println(documents)
	// Output:
	// [{document-1 PT} {document-2 ES}]
}

func ExampleProjection_ScanInto() {
	type Document struct {
		ID      string
		Title   string
		Version int64
	}
	returning := projection.Column(qs.Col("version"), func(d *Document) *int64 { return &d.Version })
	document := Document{ID: "document-1", Title: "Draft"}
	text, args, err := qs.Update("documents").
		Set(qs.Set("title", qs.Value(document.Title))).
		Where(qs.Col("id").Eq(qs.Param(document.ID))).
		Returning(returning.Expressions()...).
		ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)

	// Application code passes row.Scan.
	scan := func(dest ...any) error {
		_, err := fmt.Sscan("2", dest...)
		return err
	}
	if err := returning.ScanInto(&document, scan); err != nil {
		panic(err)
	}
	fmt.Println(document)
	// Output:
	// UPDATE "documents" SET "title" = $1 WHERE ("id" = $2) RETURNING "version"
	// [Draft document-1]
	// {document-1 Draft 2}
}

// A LEFT JOIN side whose columns are all nullable: the join key, which no field
// holds, decides presence, and NULL in the others reads as the zero value.
func ExampleColumnOrZero() {
	type Human struct {
		Nickname string
		Logins   sql.NullInt64
	}
	type User struct {
		ID    int64
		Human *Human
	}
	humans := qs.Table("humans").As("h")
	human := projection.New(
		projection.ColumnNull(humans.Col("user_id"), func(*Human, *int64) {}),
		projection.ColumnOrZero(
			humans.Col("nickname"),
			func(h *Human) *string { return &h.Nickname },
		),
		projection.ColumnOrZero(
			humans.Col("logins"),
			func(h *Human) *sql.NullInt64 { return &h.Logins },
		),
	)
	users := qs.Table("users").As("u")
	p := projection.New(
		projection.Column(users.Col("id"), func(u *User) *int64 { return &u.ID }),
		projection.EmbedNull(human, func(u *User, h *Human) { u.Human = h }),
	)
	text, _, err := qs.SelectSlice(p.Expressions()).
		FromExpr(
			qs.LeftJoin(users, humans).On(users.Col("id").Eq(humans.Col("user_id"))),
		).
		ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	// Output:
	// SELECT "u"."id", "h"."user_id", "h"."nickname", "h"."logins" FROM ("users" AS "u" LEFT JOIN "humans" AS "h" ON ("u"."id" = "h"."user_id"))
}

// counted reads a trailing window count beside each row a projection reads.
// Embedding the driver's rows, here records, keeps their Close.
type counted struct {
	*records
	total *int64
}

func (c counted) Scan(dest ...any) error { return c.records.Scan(append(dest, c.total)...) }

// A value that belongs to no row, selected after Expressions.
func ExampleProjection_Collect_windowCount() {
	type Document struct {
		ID      string
		Country string
	}
	documents := qs.Table("documents").As("d")
	p := projection.New(
		projection.Column(documents.Col("id"), func(d *Document) *string { return &d.ID }),
		projection.Column(documents.Col("country"), func(d *Document) *string { return &d.Country }),
	)
	text, args, err := qs.SelectSlice(p.Expressions()).
		Columns(qs.CountAll().Over(qs.Window())).
		FromExpr(documents).
		OrderBy(documents.Col("id").Asc()).
		Limit(2).
		ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)

	var total int64
	rows := &records{unread: []string{"document-1 PT 5", "document-2 ES 5"}}
	page, err := p.Collect(counted{records: rows, total: &total})
	if err != nil {
		panic(err)
	}
	fmt.Println(page, total)
	// Output:
	// SELECT "d"."id", "d"."country", count(*) OVER () FROM "documents" AS "d" ORDER BY "d"."id" ASC LIMIT $1
	// [2]
	// [{document-1 PT} {document-2 ES}] 5
}
