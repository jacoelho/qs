// Package qs builds PostgreSQL queries without reflection, execution, or scanning.
//
// Ordinary values become bind parameters. Every identifier argument is one
// name, quoted as written: Col("u", "id") is "u"."id" and Col("u.id") is the
// single identifier "u.id". SQL-suffixed methods and UnsafeSQL accept trusted
// application-authored SQL, never request text. A single renderer numbers
// parameters through nested statements.
//
// Comparisons are methods of Expr and take expressions; Param binds a value.
// A Field, from TypedCol or Typed, compares with Go values of its type and
// binds them. Comparison operands, including In lists, row fields and SeekAfter
// cursors, reject untyped nil because a comparison with SQL NULL is never true;
// Param, Value and ParamNull bind it elsewhere. Trusted conditions are
// parenthesized when qs combines them.
//
// Update and DeleteFrom return a pending statement that cannot render. Where,
// WhereCurrentOf or AllRows completes it by selecting its rows; Where still
// fails rendering when its conditions are TRUE for every row by construction.
// Case and CaseOf likewise return a value that only accepts the first branch,
// and Select requires its first column: SelectSlice takes a run-time list and
// SelectNoColumns states a zero-column SELECT. A query has one row limit,
// which Limit, LimitAll and LimitWithTies each replace.
//
// SQL/JSON forms are builders of their own: JSONObject, JSONArray,
// JSONArrayQuery, JSONObjectAggregate, JSONArrayAggregate, JSONParse,
// JSONScalar, JSONSerialize, IsJSON, JSONExists, JSONValue, JSONQuery and
// JSONTable. Each has only the clauses PostgreSQL accepts for it, so JSONValue
// has no wrapper and JSONExists no RETURNING. A JSONOperand is an expression
// or one marked with FormatJSON. A DEFAULT behavior takes a constant, function
// call or operator expression: PostgreSQL refuses a column there, and a value
// that is a column fails rendering. JSONTable is a row source that Ref or As
// turns into a Relation.
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
// operands. Dynamic lists, zero descriptors and subqueries still require render
// validation; a Statement is not proof of database validity.
//
// Builders, expressions and relations are immutable values. Every builder
// method returns a new builder and leaves its receiver unchanged, so a base
// query can be branched and shared between goroutines; a result that is
// discarded changes nothing. A subquery holds the builder value it was given.
// Bound parameter values are not copied.
//
// ToSQL returns an owned string and argument slice. AppendSQL appends to reusable
// caller-owned storage, starting parameter numbering after the existing args.
// Parameters render as $1, $2 and so on. ToSQLWith and AppendWith set the
// parameter and nesting limits for one render.
// Driver codecs, transactions, tenant context, RLS, and result mapping remain
// the caller's responsibility.
package qs
