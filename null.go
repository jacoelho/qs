package qs

// Null represents a nullable SQL input. The zero value is SQL NULL. It has no
// database/sql or pgx dependency and does not implement scanning or Valuer.
type Null[T any] struct {
	// Value is the Go value when Valid is true.
	Value T
	// Valid reports whether Value is a non-NULL value. When false, the Null
	// represents SQL NULL.
	Valid bool
}

// NonNull returns a Null containing a non-NULL value.
func NonNull[T any](value T) Null[T] { return Null[T]{Value: value, Valid: true} }

// NullOf returns a Null representing SQL NULL.
func NullOf[T any]() Null[T] { return Null[T]{} }

// Optional represents whether an input was supplied, not whether it is NULL.
// Optional[Null[T]] distinguishes an absent patch from setting SQL NULL.
type Optional[T any] struct {
	// Value is the supplied value when Present is true.
	Value T
	// Present reports whether the input was supplied.
	Present bool
}

// Some returns an Optional containing a supplied value.
func Some[T any](value T) Optional[T] { return Optional[T]{Value: value, Present: true} }

// None returns an Optional representing an absent input.
func None[T any]() Optional[T]     { return Optional[T]{} }
func (Optional[T]) optionalValue() {}
