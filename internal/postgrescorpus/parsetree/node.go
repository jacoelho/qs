package parsetree

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Node tags and enum values the builder tests in several places.
const (
	tagConstant = "A_Const"
	tagFuncCall = "FuncCall"
	tagRow      = "RowExpr"
	tagSelect   = "SelectStmt"
	tagStar     = "A_Star"
	tagString   = "String"

	joinInner             = "JOIN_INNER"
	overridingSystemValue = "OVERRIDING_SYSTEM_VALUE"
	overridingUserValue   = "OVERRIDING_USER_VALUE"
	sublinkAll            = "ALL_SUBLINK"
)

// node is one parse-tree object. libpg_query JSON wraps a generic Node field
// in a single-key object named after its type; a field of a fixed struct type
// is inlined without that tag.
type node struct {
	fields map[string]any
	// faults receives structural errors found while reading fields.
	faults *faults
	tag    string
}

// faults keeps the first structural error of one tree.
type faults struct{ err error }

func (f *faults) reportf(format string, args ...any) {
	// An absent node has no sink and nothing to misread.
	if f != nil && f.err == nil {
		f.err = fmt.Errorf(format, args...)
	}
}

func decode(tree []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(tree))
	// Parameter numbers and frame options must stay exact integers.
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode parse tree: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode parse tree: trailing data")
	}
	return root, nil
}

// tagged interprets value as a generic Node wrapper.
func tagged(value any, sink *faults, where string) node {
	object, ok := value.(map[string]any)
	if !ok {
		sink.reportf("%s: node is not an object", where)
		return node{}
	}
	// PostgreSQL prints an absent list member as an empty object.
	if len(object) == 0 {
		return node{fields: object, faults: sink}
	}
	if len(object) != 1 {
		sink.reportf("%s: node wrapper has %d keys", where, len(object))
		return node{}
	}
	for tag, inner := range object {
		fields, ok := inner.(map[string]any)
		if !ok {
			sink.reportf("%s: %s body is not an object", where, tag)
			return node{}
		}
		return node{tag: tag, fields: fields, faults: sink}
	}
	return node{}
}

func (n node) present() bool { return n.fields != nil }

func (n node) has(name string) bool {
	_, ok := n.fields[name]
	return ok
}

// node returns a generic Node field, or an absent node.
func (n node) node(name string) node {
	value, ok := n.fields[name]
	if !ok {
		return node{}
	}
	return tagged(value, n.faults, name)
}

// record returns a field of fixed struct type, which carries no tag.
func (n node) record(name string) node {
	value, ok := n.fields[name]
	if !ok {
		return node{}
	}
	fields, ok := value.(map[string]any)
	if !ok {
		n.faults.reportf("%s.%s: not an object", n.tag, name)
		return node{}
	}
	return node{fields: fields, faults: n.faults}
}

// nodes returns a list of generic Nodes.
func (n node) nodes(name string) []node {
	value, ok := n.fields[name]
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		n.faults.reportf("%s.%s: not a list", n.tag, name)
		return nil
	}
	result := make([]node, len(items))
	for index, item := range items {
		result[index] = tagged(item, n.faults, name)
	}
	return result
}

// text returns a string or enum field; an absent field is empty.
func (n node) text(name string) string {
	value, ok := n.fields[name]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		n.faults.reportf("%s.%s: not a string", n.tag, name)
	}
	return text
}

// flag returns a boolean field; protobuf JSON omits false.
func (n node) flag(name string) bool {
	value, ok := n.fields[name]
	if !ok {
		return false
	}
	flag, ok := value.(bool)
	if !ok {
		n.faults.reportf("%s.%s: not a boolean", n.tag, name)
	}
	return flag
}

// integer returns an integer field; protobuf JSON omits zero.
func (n node) integer(name string) int64 {
	value, ok := n.fields[name]
	if !ok {
		return 0
	}
	number, ok := value.(json.Number)
	if !ok {
		n.faults.reportf("%s.%s: not a number", n.tag, name)
		return 0
	}
	integer, err := number.Int64()
	if err != nil {
		n.faults.reportf("%s.%s: %v", n.tag, name, err)
	}
	return integer
}

// strings returns the values of a list of String nodes.
func (n node) strings(name string) []string {
	items := n.nodes(name)
	if len(items) == 0 {
		return nil
	}
	result := make([]string, len(items))
	for index, item := range items {
		if item.tag != tagString {
			n.faults.reportf("%s.%s[%d]: %s is not a String", n.tag, name, index, item.tag)
			return nil
		}
		result[index] = item.text("sval")
	}
	return result
}

// members unwraps a List node, as used for IN lists and VALUES rows.
func (n node) members() []node {
	if !n.present() {
		return nil
	}
	if n.tag != "List" {
		n.faults.reportf("%s is not a List", n.tag)
		return nil
	}
	return n.nodes("items")
}
