package qs

import "testing"

// Star and CountAll return shared nodes. A modifier must copy the call it
// changes, or every later count(*) would carry it.
func TestCountAllIsSharedAndNeverWritten(t *testing.T) {
	var sink Expr
	if n := testing.AllocsPerRun(100, func() { sink = Star() }); n != 0 {
		t.Errorf("Star() allocates %v times", n)
	}
	if n := testing.AllocsPerRun(100, func() { sink = CountAll() }); n != 0 {
		t.Errorf("CountAll() allocates %v times", n)
	}
	_ = sink

	filtered := CountAll().Filter(Col("a").IsNull())
	windowed := CountAll().Over(Window())
	checkSQL(t, Select(filtered, windowed, CountAll()).From("t"),
		`SELECT count(*) FILTER (WHERE ("a" IS NULL)), count(*) OVER (), count(*) FROM "t"`)
}
