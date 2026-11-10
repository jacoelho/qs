// Package pgtype is a test fixture. Its types share their printed names with
// driver types that projection refuses to read through a pointer, and must not
// be mistaken for them.
package pgtype

// DriverBytes prints as pgtype.DriverBytes, as the pgx type does.
type DriverBytes []byte

// CompositeFields prints as pgtype.CompositeFields, as the pgx type does.
type CompositeFields []any
