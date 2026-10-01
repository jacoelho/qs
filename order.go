package qx

type direction uint8

const (
	ascending direction = iota + 1
	descending
	usingOperator
)

type nullOrdering uint8

const (
	defaultNulls nullOrdering = iota
	nullsFirst
	nullsLast
)

// Order is a structural ORDER BY term. Identifiers and directions never share a
// raw string, so a user-supplied direction cannot inject SQL syntax.
type Order struct {
	expr      Expr
	direction direction
	nulls     nullOrdering
	operator  string
}

func Asc(column string) Order  { return Col(column).Asc() }
func Desc(column string) Order { return Col(column).Desc() }
func (e Expr) Asc() Order      { return Order{expr: e, direction: ascending} }
func (e Expr) Desc() Order     { return Order{expr: e, direction: descending} }
func (e Expr) Using(operator string) Order {
	return Order{expr: e, direction: usingOperator, operator: operator}
}
func (o Order) NullsFirst() Order { o.nulls = nullsFirst; return o }
func (o Order) NullsLast() Order  { o.nulls = nullsLast; return o }

// Ordinal is a 1-based output-column index for ORDER BY or GROUP BY, not a bind.
func Ordinal(index int) Expr {
	if index < 1 {
		return invalidExpr("ordinal", "index must be positive")
	}
	return LiteralInt(int64(index))
}

func (w *renderer) orders(terms []Order) {
	for i, term := range terms {
		if i != 0 {
			w.text(", ")
		}
		w.expr(term.expr)
		switch term.direction {
		case ascending:
			w.text(" ASC")
		case descending:
			w.text(" DESC")
		case usingOperator:
			if !w.require(validOperator(term.operator), "ORDER BY", "invalid symbolic ordering operator") {
				return
			}
			w.text(" USING ")
			w.text(term.operator)
		default:
			w.fail(ErrInvalid, "ORDER BY", "zero ordering term")
			return
		}
		switch term.nulls {
		case defaultNulls:
		case nullsFirst:
			w.text(" NULLS FIRST")
		case nullsLast:
			w.text(" NULLS LAST")
		default:
			w.fail(ErrInvalid, "ORDER BY", "invalid NULL ordering")
		}
	}
}
