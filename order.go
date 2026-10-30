package qs

type direction uint8

const (
	ascending direction = iota + 1
	descending
	usingOperator
	// reversedUsing records Reverse of a USING term, whose reverse operator qs
	// cannot derive. It renders as an error and reverses back to usingOperator.
	reversedUsing
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
	operator  string
	expr      Expr
	direction direction
	nulls     nullOrdering
}

// Asc returns an ascending order term for a column path.
func Asc(column string) Order { return Col(column).Asc() }

// Desc returns a descending order term for a column path.
func Desc(column string) Order { return Col(column).Desc() }

// Asc returns an ascending order term for an expression.
func (e Expr) Asc() Order { return Order{expr: e, direction: ascending} }

// Desc returns a descending order term for an expression.
func (e Expr) Desc() Order { return Order{expr: e, direction: descending} }

// Using returns an order term that uses a validated PostgreSQL operator.
func (e Expr) Using(operator string) Order {
	return Order{expr: e, direction: usingOperator, operator: operator}
}

// NullsFirst places NULL values before non-NULL values.
func (o Order) NullsFirst() Order { o.nulls = nullsFirst; return o }

// NullsLast places NULL values after non-NULL values.
func (o Order) NullsLast() Order { o.nulls = nullsLast; return o }

// Reverse returns the term that orders rows in exactly the opposite sequence.
// ASC and DESC swap, and explicit NULLS FIRST/LAST swap; default NULL placement
// already follows the direction. Reversing twice returns the original term.
// qs cannot derive the reverse of a USING operator, so a reversed USING term
// fails when rendered.
func (o Order) Reverse() Order {
	switch o.direction {
	case ascending:
		o.direction = descending
	case descending:
		o.direction = ascending
	case usingOperator:
		o.direction = reversedUsing
	case reversedUsing:
		o.direction = usingOperator
	}
	switch o.nulls {
	case nullsFirst:
		o.nulls = nullsLast
	case nullsLast:
		o.nulls = nullsFirst
	case defaultNulls:
	}
	return o
}

// Ordinal is a 1-based output-column index for ORDER BY or GROUP BY, not a bind.
func Ordinal(index int) Expr {
	if index < 1 {
		return invalidExpr("ordinal", "index must be positive")
	}
	return LiteralInt(int64(index))
}

func (w *renderer) orders(terms []Order) {
	if w.err != nil {
		return
	}
	for i, term := range terms {
		if i != 0 {
			w.text(", ")
		}
		w.order(term)
		if w.stopped("order", i+1) {
			return
		}
	}
}

func (w *renderer) order(term Order) {
	w.expr(term.expr)
	if w.err != nil {
		return
	}
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
	case reversedUsing:
		w.fail(ErrInvalid, "ORDER BY", "a USING term cannot be reversed")
		return
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
