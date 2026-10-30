package projection_test

import (
	"fmt"

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
			projection.Column(r.Col("name").As(prefix+"name"), func(u *User) *string { return &u.Name }),
			projection.ColumnNull(r.Col("email").As(prefix+"email"), func(u *User, v *string) { u.Email = v }),
		)
	}
	employee := qs.Table("users").As("employee")
	manager := qs.Table("users").As("manager")
	p := projection.New(
		projection.Embed(users(employee, ""), func(r *Row) *User { return &r.Employee }),
		projection.EmbedNull(users(manager, "manager_"), func(r *Row, u *User) { r.Manager = u }),
	)
	text, args, err := qs.Select(p.Expressions()...).FromExpr(
		qs.LeftJoin(employee, manager).On(employee.Col("manager_id").EqExpr(manager.Col("id"))),
	).ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)
	// Output:
	// SELECT "employee"."id" AS "id", "employee"."name" AS "name", "employee"."email" AS "email", "manager"."id" AS "manager_id", "manager"."name" AS "manager_name", "manager"."email" AS "manager_email" FROM ("users" AS "employee" LEFT JOIN "users" AS "manager" ON ("employee"."manager_id" = "manager"."id"))
	// []
}

func ExampleProjection_Scan() {
	type Document struct {
		ID      string
		Country string
	}
	p := projection.Map(projection.New(
		projection.Column(qs.Col("id"), func(d *Document) *string { return &d.ID }),
		projection.Column(qs.JSONBCol("payload").TextKey("country").Expr(), func(d *Document) *string { return &d.Country }),
	), func(d Document) (string, error) { return d.ID + ":" + d.Country, nil })

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
		projection.Column(qs.Col("d.id"), func(d *Document) *string { return &d.ID }),
		projection.Column(qs.Col("d.payload"), func(d *Document) *string { return &d.Payload }),
	)
	text, args, err := qs.UpdateTable(qs.Table("documents").As("d")).
		Set(qs.SetNull("archived_at")).Where(qs.Eq("d.id", "document-1")).
		Returning(p.Expressions()...).ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)
	// Output:
	// UPDATE "documents" AS "d" SET "archived_at" = NULL WHERE ("d"."id" = $1) RETURNING "d"."id", "d"."payload"
	// [document-1]
}
