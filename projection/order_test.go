package projection_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

type contact struct {
	Summary string
	ID      int64
}

// contacts declares the set before the Column it reads, so a set that ran before that
// Column was assigned would format a zero ID.
func contacts() projection.Projection[contact] {
	return projection.New(
		projection.ColumnNull(qs.Col("nickname"), func(c *contact, v *string) {
			if v != nil {
				c.Summary = fmt.Sprintf("%s#%d", *v, c.ID)
			}
		}),
		projection.Column(qs.Col("id"), func(c *contact) *int64 { return &c.ID }),
	)
}

func TestSetSeesEveryColumnOfItsRow(t *testing.T) {
	t.Parallel()

	want := contact{Summary: "ada#7", ID: 7}
	row := fakeRow("ada", int64(7))

	t.Run("flat", func(t *testing.T) {
		t.Parallel()
		got, err := contacts().Scan(row)
		if err != nil || got != want {
			t.Fatalf("got %+v, %v, want %+v", got, err, want)
		}
	})
	t.Run("group", func(t *testing.T) {
		t.Parallel()
		p := projection.EmbedNull(contacts(), func(d **contact, c *contact) { *d = c })
		got, err := p.Scan(row)
		if err != nil || got == nil || *got != want {
			t.Fatalf("got %+v, %v, want %+v", got, err, want)
		}
	})
	t.Run("nested group", func(t *testing.T) {
		t.Parallel()
		type holder struct{ Contact *contact }
		p := projection.EmbedNull(
			projection.EmbedNull(contacts(), func(h *holder, c *contact) { h.Contact = c }),
			func(d **holder, h *holder) { *d = h },
		)
		got, err := p.Scan(row)
		if err != nil || got == nil || got.Contact == nil {
			t.Fatalf("got %+v, %v, want a present group", got, err)
		}
		if *got.Contact != want {
			t.Fatalf("got %+v, want %+v", *got.Contact, want)
		}
	})
	t.Run("embed in group", func(t *testing.T) {
		t.Parallel()
		type holder struct{ Contact contact }
		p := projection.EmbedNull(
			projection.Embed(contacts(), func(h *holder) *contact { return &h.Contact }),
			func(d **holder, h *holder) { *d = h },
		)
		got, err := p.Scan(row)
		if err != nil || got == nil || got.Contact != want {
			t.Fatalf("got %+v, %v, want %+v", got, err, want)
		}
	})
}

// A group's set receives its parent row, whose Columns may be declared after the group.
func TestGroupSetSeesParentColumns(t *testing.T) {
	t.Parallel()

	type parent struct {
		Child   *contact
		OwnerID int64
		ID      int64
	}
	declared := projection.New(
		projection.EmbedNull(contacts(), func(p *parent, c *contact) { p.Child, p.OwnerID = c, p.ID }),
		projection.Column(qs.Col("parent_id"), func(p *parent) *int64 { return &p.ID }),
	)
	row := fakeRow("ada", int64(7), int64(42))

	flat, err := declared.Scan(row)
	if err != nil || flat.OwnerID != 42 || flat.ID != 42 {
		t.Fatalf("flat: got %+v, %v, want OwnerID 42", flat, err)
	}
	grouped, err := projection.EmbedNull(declared, func(d **parent, p *parent) { *d = p }).Scan(row)
	if err != nil || grouped == nil || grouped.OwnerID != 42 || grouped.ID != 42 {
		t.Fatalf("group: got %+v, %v, want OwnerID 42", grouped, err)
	}
}

type account struct {
	ID   int64
	Name string
}

type holder struct{ User account }

var (
	accountID = projection.Column(qs.Col("id"), func(a *account) *int64 { return &a.ID })
	userOf    = func(h *holder) *account { return &h.User }

	// named uppercases the name and delivers it into one field of its row.
	named = projection.Map(
		projection.Column(qs.Col("name"), func(s *string) *string { return s }),
		func(s string) (string, error) { return strings.ToUpper(s), nil },
		func(a *account, v string) { a.Name = v },
	)
)

func appendSuffix() projection.Projection[account] {
	return projection.ColumnNull(qs.Col("suffix"), func(a *account, v *string) { a.Name += *v })
}

// readFlatAndGrouped checks that p reads row to want, read directly and as the only
// content of a group.
func readFlatAndGrouped[D any](t *testing.T, p projection.Projection[D], want D, row ...any) {
	t.Helper()

	flat, err := p.Scan(fakeRow(row...))
	if err != nil || !reflect.DeepEqual(flat, want) {
		t.Errorf("flat: got %+v, %v, want %+v", flat, err, want)
	}
	grouped, err := projection.EmbedNull(p, func(d **D, v *D) { *d = v }).Scan(fakeRow(row...))
	if err != nil || grouped == nil || !reflect.DeepEqual(*grouped, want) {
		t.Errorf("group: got %+v, %v, want %+v", grouped, err, want)
	}
}

// Map delivers through a set the caller writes, so what other declarations read into
// the same value survives. With the previous Map(inner, convert), which assigned its
// whole value, the shared-Embed forms read {ID:0 Name:ada} with a nil error.
func TestMapKeepsWhatOtherDeclarationsRead(t *testing.T) {
	t.Parallel()

	wantHolder := holder{User: account{ID: 7, Name: "ADA"}}
	wantAccount := account{ID: 7, Name: "ADA"}

	t.Run("two Embeds of one field", func(t *testing.T) {
		t.Parallel()
		p := projection.New(projection.Embed(accountID, userOf), projection.Embed(named, userOf))
		readFlatAndGrouped(t, p, wantHolder, int64(7), "ada")
	})
	t.Run("two Embeds of one field, reversed", func(t *testing.T) {
		t.Parallel()
		p := projection.New(projection.Embed(named, userOf), projection.Embed(accountID, userOf))
		readFlatAndGrouped(t, p, wantHolder, "ada", int64(7))
	})
	t.Run("Column into a field of an embedded Map", func(t *testing.T) {
		t.Parallel()
		p := projection.New(
			projection.Embed(named, userOf),
			projection.Column(qs.Col("id"), func(h *holder) *int64 { return &h.User.ID }),
		)
		readFlatAndGrouped(t, p, wantHolder, "ada", int64(7))
	})
	t.Run("Column then Map", func(t *testing.T) {
		t.Parallel()
		readFlatAndGrouped(t, projection.New(accountID, named), wantAccount, int64(7), "ada")
	})
	t.Run("Map then Column", func(t *testing.T) {
		t.Parallel()
		readFlatAndGrouped(t, projection.New(named, accountID), wantAccount, "ada", int64(7))
	})
	t.Run("Column then Embedded Map of the same row", func(t *testing.T) {
		t.Parallel()
		p := projection.New(accountID, projection.Embed(named, func(a *account) *account { return a }))
		readFlatAndGrouped(t, p, wantAccount, int64(7), "ada")
	})
}

// A set that assigns its whole value replaces what was read into it before the set
// ran, without an error: the replacement is a statement at the call site.
func TestMapWholeValueSetReplacesWhatWasRead(t *testing.T) {
	t.Parallel()

	whole := projection.Map(
		projection.Column(qs.Col("name"), func(s *string) *string { return s }),
		func(s string) (account, error) { return account{Name: strings.ToUpper(s)}, nil },
		func(d *account, v account) { *d = v },
	)
	got, err := projection.New(accountID, whole).Scan(fakeRow(int64(7), "ada"))
	if want := (account{ID: 0, Name: "ADA"}); err != nil || got != want {
		t.Fatalf("got %+v, %v, want %+v", got, err, want)
	}

	got, err = projection.New(whole, appendSuffix()).Scan(fakeRow("ada", "!"))
	if want := (account{Name: "ADA!"}); err != nil || got != want {
		t.Fatalf("set after a whole-value Map: got %+v, %v, want %+v", got, err, want)
	}
}

func TestMapSetRunsInDeclarationOrder(t *testing.T) {
	t.Parallel()

	tagged := projection.Map(
		projection.Column(qs.Col("name"), func(s *string) *string { return s }),
		func(s string) (string, error) { return strings.ToUpper(s), nil },
		func(a *account, v string) { a.Name = fmt.Sprintf("%s#%d", v, a.ID) },
	)

	t.Run("set after Map", func(t *testing.T) {
		t.Parallel()
		readFlatAndGrouped(t, projection.New(named, appendSuffix()), account{Name: "ADA!"}, "ada", "!")
	})
	t.Run("set before Map", func(t *testing.T) {
		t.Parallel()
		readFlatAndGrouped(t, projection.New(appendSuffix(), named), account{Name: "ADA"}, "!", "ada")
	})
	t.Run("Map sees a Column declared after it", func(t *testing.T) {
		t.Parallel()
		readFlatAndGrouped(t, projection.New(tagged, accountID), account{ID: 7, Name: "ADA#7"}, "ada", int64(7))
	})
	t.Run("Map sees a ColumnOrZero declared after it", func(t *testing.T) {
		t.Parallel()
		p := projection.New(tagged, projection.ColumnOrZero(qs.Col("id"), func(a *account) *int64 { return &a.ID }))
		readFlatAndGrouped(t, p, account{ID: 7, Name: "ADA#7"}, "ada", int64(7))
	})
	t.Run("Map inside a group runs before the group's set", func(t *testing.T) {
		t.Parallel()
		p := projection.EmbedNull(projection.New(tagged, accountID), func(d *account, v *account) {
			if v != nil {
				*d = *v
			}
		})
		got, err := p.Scan(fakeRow("ada", int64(7)))
		if want := (account{ID: 7, Name: "ADA#7"}); err != nil || got != want {
			t.Fatalf("got %+v, %v, want %+v", got, err, want)
		}
	})
}
