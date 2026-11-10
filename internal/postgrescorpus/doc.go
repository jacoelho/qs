// Package postgrescorpus measures how much of the pinned PostgreSQL
// regression corpus the public qs API can express.
//
// Each fixture record carries a frozen PostgreSQL parse tree and the SQL an
// independent parser verified for it. Package parsetree builds the statement
// from the tree; this package owns the fixture format, the manifest audit and
// the comparison. Nothing here runs outside tests and the offline tooling.
package postgrescorpus
