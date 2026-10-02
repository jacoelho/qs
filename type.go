package qs

import "strconv"

const numericTypeName = "numeric"

// DataType is PostgreSQL type syntax, not a driver codec. Use NamedType for a
// domain or extension type, never interpolate request strings into type syntax.
type DataType struct {
	name    string
	invalid string
	parts   []string
	params  []int
	arrays  int
}

var (
	// Bool is PostgreSQL's boolean type.
	Bool = DataType{name: "boolean"}
	// Int2 is PostgreSQL's smallint type.
	Int2 = DataType{name: "smallint"}
	// Int4 is PostgreSQL's integer type.
	Int4 = DataType{name: "integer"}
	// Int8 is PostgreSQL's bigint type.
	Int8 = DataType{name: "bigint"}
	// Float4 is PostgreSQL's real type.
	Float4 = DataType{name: "real"}
	// Float8 is PostgreSQL's double precision type.
	Float8 = DataType{name: "double precision"}
	// Text is PostgreSQL's text type.
	Text = DataType{name: "text"}
	// Bytea is PostgreSQL's binary byte-string type.
	Bytea = DataType{name: "bytea"}
	// UUID is PostgreSQL's universally unique identifier type.
	UUID = DataType{name: "uuid"}
	// JSON is PostgreSQL's json type.
	JSON = DataType{name: "json"}
	// JSONB is PostgreSQL's binary JSON type.
	JSONB = DataType{name: "jsonb"}
	// JSONPath is PostgreSQL's jsonpath type.
	JSONPath = DataType{name: "jsonpath"}
	// Date is PostgreSQL's date type.
	Date = DataType{name: "date"}
	// Time is PostgreSQL's time without time zone type.
	Time = DataType{name: "time"}
	// TimeTZ is PostgreSQL's time with time zone type.
	TimeTZ = DataType{name: "time with time zone"}
	// Timestamp is PostgreSQL's timestamp without time zone type.
	Timestamp = DataType{name: "timestamp"}
	// TimestampTZ is PostgreSQL's timestamp with time zone type.
	TimestampTZ = DataType{name: "timestamp with time zone"}
	// Interval is PostgreSQL's interval type.
	Interval = DataType{name: "interval"}
	// Numeric is PostgreSQL's numeric type without precision or scale.
	Numeric = DataType{name: numericTypeName}
	// TSVector is PostgreSQL's tsvector type.
	TSVector = DataType{name: "tsvector"}
	// TSQuery is PostgreSQL's tsquery type.
	TSQuery = DataType{name: "tsquery"}
	// Inet is PostgreSQL's inet type.
	Inet = DataType{name: "inet"}
	// CIDR is PostgreSQL's cidr type.
	CIDR = DataType{name: "cidr"}
	// XML is PostgreSQL's xml type.
	XML = DataType{name: "xml"}
)

// Varchar returns a varchar type with the given positive length.
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
	return DataType{name: numericTypeName, params: []int{precision, scale}}
}

// NamedType returns a possibly schema-qualified type name.
func NamedType(parts ...string) DataType { return DataType{parts: cloneSlice(parts)} }

// ArrayType returns an array type whose element type is element.
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
		case numericTypeName:
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
	switch {
	case len(t.parts) != 0:
		for i, p := range t.parts {
			if i != 0 {
				w.byte('.')
			}
			w.identifierPart(p)
		}
	case t.name != "":
		w.text(t.name)
	default:
		w.fail(ErrInvalid, "type", "zero type descriptor")
		return
	}
	if len(t.params) != 0 {
		numeric := t.name == numericTypeName || len(t.parts) == 2 && t.parts[0] == "pg_catalog" && t.parts[1] == numericTypeName
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
