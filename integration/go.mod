module github.com/jacoelho/qs/integration

go 1.27.0

require (
	github.com/jackc/pgx/v5 v5.11.0
	github.com/jacoelho/qs v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/jacoelho/qs => ..
