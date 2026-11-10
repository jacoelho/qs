package parsetree

import (
	"slices"
	"strings"
)

// removedNodes maps node types to the SQL feature qs does not model.
var removedNodes = map[string]string{
	"XmlSerialize":      "XMLSERIALIZE",
	"RangeTableFunc":    "XMLTABLE",
	"ExplainStmt":       "EXPLAIN",
	"ExecuteStmt":       "EXECUTE",
	"DeclareCursorStmt": "DECLARE CURSOR",
	"TruncateStmt":      "TRUNCATE",
}

var removedXMLOperations = map[string]string{
	"IS_XMLCONCAT":    "XMLCONCAT",
	"IS_XMLELEMENT":   "XMLELEMENT",
	"IS_XMLFOREST":    "XMLFOREST",
	"IS_XMLPARSE":     "XMLPARSE",
	"IS_XMLPI":        "XMLPI",
	"IS_XMLROOT":      "XMLROOT",
	"IS_XMLSERIALIZE": "XMLSERIALIZE",
	"IS_DOCUMENT":     "IS DOCUMENT",
}

// removedFeatures names every removed feature in the tree, sorted and
// comma-separated, or returns "" when the tree is in scope.
func removedFeatures(root any) string {
	found := make(map[string]struct{})
	collectRemoved(root, found)
	if len(found) == 0 {
		return ""
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func collectRemoved(value any, found map[string]struct{}) {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			collectRemoved(item, found)
		}
	case map[string]any:
		for key, inner := range value {
			if feature := removedFeature(key, inner); feature != "" {
				found[feature] = struct{}{}
			}
			collectRemoved(inner, found)
		}
	}
}

// removedFeature classifies one object key: a node tag, or the name of a
// field that only removed syntax populates.
func removedFeature(key string, value any) string {
	if feature, ok := removedNodes[key]; ok {
		return feature
	}
	if key == "intoClause" {
		return "SELECT INTO"
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	switch key {
	case "CreateTableAsStmt":
		if fields["objtype"] == "OBJECT_MATVIEW" {
			return "CREATE MATERIALIZED VIEW"
		}
		return "CREATE TABLE AS"
	case "XmlExpr":
		return operationFeature(removedXMLOperations, fields["op"], "XML expression")
	case tagFuncCall:
		// XMLEXISTS is the one removed form the parser stores as a call.
		if fields["funcformat"] == "COERCE_SQL_SYNTAX" && lastString(fields["funcname"]) == "xmlexists" {
			return "XMLEXISTS"
		}
	}
	return ""
}

func operationFeature(names map[string]string, operation any, fallback string) string {
	if name, ok := operation.(string); ok {
		if feature, ok := names[name]; ok {
			return feature
		}
	}
	return fallback
}

// lastString returns the final String value of a name list, or "".
func lastString(list any) string {
	items, ok := list.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	wrapper, ok := items[len(items)-1].(map[string]any)
	if !ok {
		return ""
	}
	inner, ok := wrapper[tagString].(map[string]any)
	if !ok {
		return ""
	}
	text, ok := inner["sval"].(string)
	if !ok {
		return ""
	}
	return text
}
