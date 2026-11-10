package parsetree

import (
	qs "github.com/jacoelho/qs"
)

// keywordTypes are the pg_catalog names the grammar substitutes for SQL
// type keywords. Each qs descriptor renders the keyword, which parses back
// to the same qualified name.
var keywordTypes = map[string]qs.DataType{
	"bool":        qs.TypeBool,
	"int2":        qs.TypeInt2,
	"int4":        qs.TypeInt4,
	"int8":        qs.TypeInt8,
	"float4":      qs.TypeFloat4,
	"float8":      qs.TypeFloat8,
	"numeric":     qs.TypeNumeric,
	"json":        qs.TypeJSON,
	"time":        qs.TypeTime,
	"timetz":      qs.TypeTimeTZ,
	"timestamp":   qs.TypeTimestamp,
	"timestamptz": qs.TypeTimestampTZ,
	"interval":    qs.TypeInterval,
}

func (b *builder) dataType(n node) qs.DataType {
	names := n.strings("names")
	if len(names) == 0 {
		b.gapf("empty type name")
		return qs.DataType{}
	}
	if n.flag("setof") || n.flag("pct_type") {
		b.gapf("setof/%%TYPE type name")
		return qs.DataType{}
	}
	if len(names) > 3 {
		b.gapf("type name has too many qualification parts")
		return qs.DataType{}
	}
	modifiers, ok := b.typeModifiers(n.nodes("typmods"))
	if !ok {
		return qs.DataType{}
	}
	result := qs.TypeNamed(names...)
	catalog := len(names) == 2 && names[0] == "pg_catalog"
	keyword, isKeyword := keywordTypes[names[len(names)-1]]
	switch {
	case catalog && names[1] == "varchar" && len(modifiers) == 1:
		result = qs.TypeVarchar(modifiers[0])
	case catalog && names[1] == "numeric" && len(modifiers) == 2:
		result = qs.TypeDecimal(modifiers[0], modifiers[1])
	case catalog && isKeyword && len(modifiers) == 0:
		result = keyword
	case len(modifiers) != 0:
		result = result.Modifiers(modifiers...)
	}
	for range n.nodes("arrayBounds") {
		result = qs.TypeArray(result)
	}
	return result
}

// typeModifiers accepts only integer constants, the one form qs models.
func (b *builder) typeModifiers(items []node) ([]int, bool) {
	modifiers := make([]int, len(items))
	for index, item := range items {
		if item.tag != tagConstant || !item.has("ival") {
			b.gapf("type modifier is not an integer constant")
			return nil, false
		}
		modifiers[index] = int(item.record("ival").integer("ival"))
	}
	return modifiers, true
}
