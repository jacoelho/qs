package qs

import "strconv"

const numericTypeName = "numeric"

// DataType is PostgreSQL type syntax, not a driver codec. Use TypeNamed for a
// domain or extension type, never interpolate request strings into type syntax.
type DataType struct {
	name    string
	invalid string
	parts   []string
	params  []int
	arrays  int
}

var (
	// TypeBool is PostgreSQL's boolean type.
	TypeBool = DataType{name: "boolean"}
	// TypeInt2 is PostgreSQL's smallint type.
	TypeInt2 = DataType{name: "smallint"}
	// TypeInt4 is PostgreSQL's integer type.
	TypeInt4 = DataType{name: "integer"}
	// TypeInt8 is PostgreSQL's bigint type.
	TypeInt8 = DataType{name: "bigint"}
	// TypeFloat4 is PostgreSQL's real type.
	TypeFloat4 = DataType{name: "real"}
	// TypeFloat8 is PostgreSQL's double precision type.
	TypeFloat8 = DataType{name: "double precision"}
	// TypeText is PostgreSQL's text type.
	TypeText = DataType{name: "text"}
	// TypeBytea is PostgreSQL's binary byte-string type.
	TypeBytea = DataType{name: "bytea"}
	// TypeUUID is PostgreSQL's universally unique identifier type.
	TypeUUID = DataType{name: "uuid"}
	// TypeJSON is PostgreSQL's json type.
	TypeJSON = DataType{name: "json"}
	// TypeJSONB is PostgreSQL's binary JSON type.
	TypeJSONB = DataType{name: "jsonb"}
	// TypeJSONPath is PostgreSQL's jsonpath type.
	TypeJSONPath = DataType{name: "jsonpath"}
	// TypeDate is PostgreSQL's date type.
	TypeDate = DataType{name: "date"}
	// TypeTime is PostgreSQL's time without time zone type.
	TypeTime = DataType{name: "time"}
	// TypeTimeTZ is PostgreSQL's time with time zone type.
	TypeTimeTZ = DataType{name: "time with time zone"}
	// TypeTimestamp is PostgreSQL's timestamp without time zone type.
	TypeTimestamp = DataType{name: "timestamp"}
	// TypeTimestampTZ is PostgreSQL's timestamp with time zone type.
	TypeTimestampTZ = DataType{name: "timestamp with time zone"}
	// TypeInterval is PostgreSQL's interval type.
	TypeInterval = DataType{name: "interval"}
	// TypeNumeric is PostgreSQL's numeric type without precision or scale.
	TypeNumeric = DataType{name: numericTypeName}
	// TypeTSVector is PostgreSQL's tsvector type.
	TypeTSVector = DataType{name: "tsvector"}
	// TypeTSQuery is PostgreSQL's tsquery type.
	TypeTSQuery = DataType{name: "tsquery"}
	// TypeInet is PostgreSQL's inet type.
	TypeInet = DataType{name: "inet"}
	// TypeCIDR is PostgreSQL's cidr type.
	TypeCIDR = DataType{name: "cidr"}
	// TypeXML is PostgreSQL's xml type.
	TypeXML = DataType{name: "xml"}
)

// TypeVarchar returns a varchar type with the given positive length.
func TypeVarchar(length int) DataType {
	if length < 1 {
		return DataType{invalid: "varchar length must be positive"}
	}
	return DataType{name: "varchar", params: []int{length}}
}

// TypeDecimal describes a numeric precision and scale. PostgreSQL 15 added negative
// scales; the renderer validates those against the configured server version.
func TypeDecimal(precision, scale int) DataType {
	if precision < 1 || precision > 1000 || scale < -1000 || scale > 1000 {
		return DataType{invalid: "numeric precision or scale out of range"}
	}
	return DataType{name: numericTypeName, params: []int{precision, scale}}
}

// TypeNamed returns a possibly schema-qualified type name.
func TypeNamed(parts ...string) DataType { return DataType{parts: cloneSlice(parts)} }

// TypeArray returns an array type whose element type is element.
func TypeArray(element DataType) DataType { element.arrays++; return element }

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
