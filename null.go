package qx

// Null represents a nullable SQL input. The zero value is SQL NULL. It has no
// database/sql or pgx dependency and does not implement scanning or Valuer.
type Null[T any] struct {
	Value T
	Valid bool
}

func NonNull[T any](value T) Null[T] { return Null[T]{Value: value, Valid: true} }
func NullOf[T any]() Null[T]         { return Null[T]{} }

// Optional represents whether an input was supplied, not whether it is NULL.
// Optional[Null[T]] distinguishes an absent patch from setting SQL NULL.
type Optional[T any] struct {
	Value   T
	Present bool
}

func Some[T any](value T) Optional[T] { return Optional[T]{Value: value, Present: true} }
func None[T any]() Optional[T]        { return Optional[T]{} }
func (Optional[T]) optionalValue()    {}
