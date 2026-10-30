// Package qs builds PostgreSQL queries without reflection, execution, or scanning.
//
// Ordinary values become bind parameters. Identifiers are quoted. SQL-suffixed
// methods and UnsafeSQL accept trusted application-authored SQL, never request
// text. A single renderer numbers parameters through nested statements.
// Comparison operands, including In lists and SeekAfter cursors, reject untyped
// nil because a comparison with SQL NULL is never true; Param, Value and
// ParamNull bind it. Trusted conditions are parenthesized when qs combines them.
//
// Destination writes accept WriteValue: Value binds application data, Write
// wraps an ordinary expression, and Default requests SQL DEFAULT. WriteRow and
// WriteTuple construct row writes.
// Write descriptors cannot be general expressions or application parameters.
// InsertInto returns a target. Select optional Columns or Targets before Values,
// From or DefaultValues; assignment sources select Set directly on the target.
// Each source creates an independent completed statement with source-specific
// methods. Column selectors own an immutable list shared between completions.
// Roles and required arguments prevent invalid grammar combinations and omitted
// operands. Dynamic lists, zero descriptors and live subqueries still require
// render validation; a Statement is not proof of database validity.
//
// Builders are mutable: use Clone before branching and do not mutate a builder
// while another goroutine renders it. Expressions and relations are immutable
// values. Subqueries retain references to their builders; Clone copies the
// complete statement structure, not application objects in bound parameters.
//
// ToSQL returns an owned string and argument slice. AppendSQL appends to reusable
// caller-owned storage, starting parameter numbering after the existing args.
// ToSQLWith and AppendWith select Dollar or Question placeholders per render;
// PostgreSQL operators and quoted text are never rewritten.
// Driver codecs, transactions, tenant context, RLS, and result mapping remain
// the caller's responsibility.
package qs
