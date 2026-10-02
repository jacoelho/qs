// Package qs builds PostgreSQL queries without reflection, execution, or scanning.
//
// Ordinary values become bind parameters. Identifiers are quoted. SQL-suffixed
// methods and UnsafeSQL accept trusted application-authored SQL, never request
// text. A single renderer numbers parameters through nested statements.
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
