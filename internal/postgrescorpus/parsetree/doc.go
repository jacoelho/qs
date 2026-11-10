// Package parsetree builds qs statements from PostgreSQL parse trees.
//
// The input is libpg_query JSON for one query, as frozen in the corpus
// fixtures. The package uses only exported qs constructors and never raw SQL,
// so a tree that builds is evidence the public API can express the query.
package parsetree
