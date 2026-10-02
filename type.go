package qs

import "strconv"

// DataType is PostgreSQL type syntax, not a driver codec. Use NamedType for a
// domain or extension type, never interpolate request strings into type syntax.
type DataType struct {
	name    string
	parts   []string
	params  []int
	arrays  int
	invalid string
}

var (
	Bool        = DataType{name: "boolean"}
	Int2        = DataType{name: "smallint"}
	Int4        = DataType{name: "integer"}
	Int8        = DataType{name: "bigint"}
	Float4      = DataType{name: "real"}
	Float8      = DataType{name: "double precision"}
	Text        = DataType{name: "text"}
	Bytea       = DataType{name: "bytea"}
	UUID        = DataType{name: "uuid"}
	JSON        = DataType{name: "json"}
	JSONB       = DataType{name: "jsonb"}
	JSONPath    = DataType{name: "jsonpath"}
	Date        = DataType{name: "date"}
	Time        = DataType{name: "time"}
	TimeTZ      = DataType{name: "time with time zone"}
	Timestamp   = DataType{name: "timestamp"}
	TimestampTZ = DataType{name: "timestamp with time zone"}
	Interval    = DataType{name: "interval"}
	Numeric     = DataType{name: "numeric"}
	TSVector    = DataType{name: "tsvector"}
	TSQuery     = DataType{name: "tsquery"}
	Inet        = DataType{name: "inet"}
	CIDR        = DataType{name: "cidr"}
	XML         = DataType{name: "xml"}
)

func Varchar(length int) DataType {
	if length < 1 {
		return DataType{invalid: "varchar length must be positive"}
	}
	return DataType{name: "varchar", params: []int{length}}
}

// Decimal describes a numeric precision and scale. PostgreSQL 15 added negative
// scales; the renderer validates those against the configured server version.
func Decimal(precision, scale int) DataType {
	if precision < 1 || precision > 1000 || scale < -1000 || scale > 1000 {
		return DataType{invalid: "numeric precision or scale out of range"}
	}
	return DataType{name: "numeric", params: []int{precision, scale}}
}

func NamedType(parts ...string) DataType  { return DataType{parts: cloneSlice(parts)} }
func ArrayType(element DataType) DataType { element.arrays++; return element }

// Modifiers supplies integer type modifiers for a named type. Dedicated builtin
// constructors retain their own validation. Custom types resolve in PostgreSQL.
func (t DataType) Modifiers(values ...int) DataType {
	if t.invalid != "" {
		return t
	}
	if len(t.parts) == 0 || len(values) == 0 {
		t.invalid = "integer modifiers require a named type and at least one value"
		return t
	}
	if len(t.parts) == 2 && t.parts[0] == "pg_catalog" {
		switch t.parts[1] {
		case "varchar", "bpchar", "bit", "varbit":
			if len(values) != 1 || values[0] < 1 {
				t.invalid = "length modifier must be one positive integer"
			}
		case "numeric":
			if len(values) < 1 || len(values) > 2 || values[0] < 1 || values[0] > 1000 || len(values) == 2 && (values[1] < -1000 || values[1] > 1000) {
				t.invalid = "numeric precision or scale out of range"
			}
		case "time", "timetz", "timestamp", "timestamptz":
			if len(values) != 1 || values[0] < 0 || values[0] > 6 {
				t.invalid = "time precision must be between 0 and 6"
			}
		}
	}
	t.params = cloneSlice(values)
	return t
}

func (w *renderer) dataType(t DataType) {
	if !w.require(t.invalid == "", "type", t.invalid) {
		return
	}
	if len(t.parts) != 0 {
		for i, p := range t.parts {
			if i != 0 {
				w.byte('.')
			}
			w.identifierPart(p)
		}
	} else if t.name != "" {
		w.text(t.name)
	} else {
		w.fail(ErrInvalid, "type", "zero type descriptor")
		return
	}
	if len(t.params) != 0 {
		numeric := t.name == "numeric" || len(t.parts) == 2 && t.parts[0] == "pg_catalog" && t.parts[1] == "numeric"
		if numeric && len(t.params) == 2 && (t.params[1] < 0 || t.params[1] > t.params[0]) {
			w.feature(PostgreSQL15, "numeric scale")
		}
		w.byte('(')
		for i, n := range t.params {
			if i != 0 {
				w.text(", ")
			}
			w.sql = strconv.AppendInt(w.sql, int64(n), 10)
		}
		w.byte(')')
	}
	for range t.arrays {
		w.text("[]")
	}
}
