package qs

// Array constructs a SQL array from expressions. Cast empty arrays explicitly.
func Array(values ...Expr) Expr                     { return listExpr("ARRAY", values) }
func ArrayFrom(query Rowset) Expr                   { return Fragment(UnsafeSQL("ARRAY"), Scalar(query)) }
func ArrayContains(array, values Expr) Condition    { return AsCondition(binary(array, "@>", values)) }
func ArrayContainedBy(array, values Expr) Condition { return AsCondition(binary(array, "<@", values)) }
func ArrayOverlaps(left, right Expr) Condition      { return AsCondition(binary(left, "&&", right)) }
func ArrayAppend(array, value Expr) Expr            { return builtin("array_append", array, value) }
func ArrayPrepend(value, array Expr) Expr           { return builtin("array_prepend", value, array) }
func ArrayCat(left, right Expr) Expr                { return builtin("array_cat", left, right) }
func ArrayLength(array, dimension Expr) Expr        { return builtin("array_length", array, dimension) }
func ArrayLower(array, dimension Expr) Expr         { return builtin("array_lower", array, dimension) }
func ArrayUpper(array, dimension Expr) Expr         { return builtin("array_upper", array, dimension) }
func ArrayPosition(array, value Expr) Expr          { return builtin("array_position", array, value) }
func ArrayPositions(array, value Expr) Expr         { return builtin("array_positions", array, value) }
func ArrayRemove(array, value Expr) Expr            { return builtin("array_remove", array, value) }
func ArrayReplace(array, old, new Expr) Expr        { return builtin("array_replace", array, old, new) }
func Cardinality(array Expr) Expr                   { return builtin("cardinality", array) }
func Unnest(arrays ...Expr) Expr                    { return builtin("unnest", arrays...) }
func StringToArray(value, delimiter Expr) Expr      { return builtin("string_to_array", value, delimiter) }
func ArrayToString(value, delimiter Expr) Expr      { return builtin("array_to_string", value, delimiter) }

func JSONGet(document, key Expr) Expr             { return binary(document, "->", key) }
func JSONGetText(document, key Expr) Expr         { return binary(document, "->>", key) }
func JSONGetPath(document, path Expr) Expr        { return binary(document, "#>", path) }
func JSONGetPathText(document, path Expr) Expr    { return binary(document, "#>>", path) }
func JSONContains(document, value Expr) Condition { return AsCondition(binary(document, "@>", value)) }
func JSONContainedBy(document, value Expr) Condition {
	return AsCondition(binary(document, "<@", value))
}
func JSONHas(document, key Expr) Condition          { return AsCondition(binary(document, "?", key)) }
func JSONHasAny(document, keys Expr) Condition      { return AsCondition(binary(document, "?|", keys)) }
func JSONHasAll(document, keys Expr) Condition      { return AsCondition(binary(document, "?&", keys)) }
func JSONDelete(document, key Expr) Expr            { return binary(document, "-", key) }
func JSONDeletePath(document, path Expr) Expr       { return binary(document, "#-", path) }
func JSONPathExists(document, path Expr) Condition  { return AsCondition(binary(document, "@?", path)) }
func JSONPathMatches(document, path Expr) Condition { return AsCondition(binary(document, "@@", path)) }
func ToJSON(value Expr) Expr                        { return builtin("to_json", value) }
func ToJSONB(value Expr) Expr                       { return builtin("to_jsonb", value) }
func RowToJSON(value Expr) Expr                     { return builtin("row_to_json", value) }
func JSONBuildArray(values ...Expr) Expr            { return builtin("json_build_array", values...) }
func JSONBBuildArray(values ...Expr) Expr           { return builtin("jsonb_build_array", values...) }
func JSONBuildObject(pairs ...Expr) Expr {
	if len(pairs)%2 != 0 {
		return invalidExpr("JSON object", "requires key/value pairs")
	}
	return builtin("json_build_object", pairs...)
}
func JSONBBuildObject(pairs ...Expr) Expr {
	if len(pairs)%2 != 0 {
		return invalidExpr("JSON object", "requires key/value pairs")
	}
	return builtin("jsonb_build_object", pairs...)
}
func JSONBSet(document, path, value Expr, createMissing bool) Expr {
	return builtin("jsonb_set", document, path, value, Param(createMissing))
}
func JSONBInsert(document, path, value Expr, after bool) Expr {
	return builtin("jsonb_insert", document, path, value, Param(after))
}
func JSONBStripNulls(document Expr) Expr    { return builtin("jsonb_strip_nulls", document) }
func JSONBArrayElements(document Expr) Expr { return builtin("jsonb_array_elements", document) }
func JSONBArrayElementsText(document Expr) Expr {
	return builtin("jsonb_array_elements_text", document)
}
func JSONBEach(document Expr) Expr          { return builtin("jsonb_each", document) }
func JSONBEachText(document Expr) Expr      { return builtin("jsonb_each_text", document) }
func JSONBObjectKeys(document Expr) Expr    { return builtin("jsonb_object_keys", document) }
func JSONBTypeOf(document Expr) Expr        { return builtin("jsonb_typeof", document) }
func JSONBToRecord(document Expr) Expr      { return builtin("jsonb_to_record", document) }
func JSONBToRecordset(document Expr) Expr   { return builtin("jsonb_to_recordset", document) }
func JSONBPathQuery(arguments ...Expr) Expr { return builtin("jsonb_path_query", arguments...) }
func JSONBPathQueryArray(arguments ...Expr) Expr {
	return builtin("jsonb_path_query_array", arguments...)
}
func JSONBPathQueryFirst(arguments ...Expr) Expr {
	return builtin("jsonb_path_query_first", arguments...)
}

func RangeContains(r, value Expr) Condition    { return AsCondition(binary(r, "@>", value)) }
func RangeContainedBy(r, value Expr) Condition { return AsCondition(binary(r, "<@", value)) }
func RangeOverlaps(left, right Expr) Condition { return AsCondition(binary(left, "&&", right)) }
func RangeAdjacent(left, right Expr) Condition { return AsCondition(binary(left, "-|-", right)) }
func RangeBefore(left, right Expr) Condition   { return AsCondition(binary(left, "<<", right)) }
func RangeAfter(left, right Expr) Condition    { return AsCondition(binary(left, ">>", right)) }
func RangeDoesNotExtendRight(left, right Expr) Condition {
	return AsCondition(binary(left, "&<", right))
}
func RangeDoesNotExtendLeft(left, right Expr) Condition {
	return AsCondition(binary(left, "&>", right))
}
func RangeUnion(left, right Expr) Expr         { return binary(left, "+", right) }
func RangeIntersection(left, right Expr) Expr  { return binary(left, "*", right) }
func RangeDifference(left, right Expr) Expr    { return binary(left, "-", right) }
func RangeLower(value Expr) Expr               { return builtin("lower", value) }
func RangeUpper(value Expr) Expr               { return builtin("upper", value) }
func RangeEmpty(value Expr) Condition          { return AsCondition(builtin("isempty", value)) }
func RangeLowerInclusive(value Expr) Condition { return AsCondition(builtin("lower_inc", value)) }
func RangeUpperInclusive(value Expr) Condition { return AsCondition(builtin("upper_inc", value)) }
func RangeLowerInfinite(value Expr) Condition  { return AsCondition(builtin("lower_inf", value)) }
func RangeUpperInfinite(value Expr) Condition  { return AsCondition(builtin("upper_inf", value)) }

// RangeAgg aggregates ranges into a multirange (PostgreSQL 14+).
func RangeAgg(value Expr) Expr { return versionedCall(PostgreSQL14, "range_agg", value) }

// RangeIntersectAgg intersects range inputs as an aggregate (PostgreSQL 14+).
func RangeIntersectAgg(value Expr) Expr {
	return versionedCall(PostgreSQL14, "range_intersect_agg", value)
}

// Int4Range uses PostgreSQL constructor overloads for int4 bounds and inclusivity.
func Int4Range(arguments ...Expr) Expr { return builtin("int4range", arguments...) }

// Int8Range uses PostgreSQL constructor overloads for int8 bounds and inclusivity.
func Int8Range(arguments ...Expr) Expr { return builtin("int8range", arguments...) }

// NumRange uses PostgreSQL constructor overloads for numeric bounds and inclusivity.
func NumRange(arguments ...Expr) Expr { return builtin("numrange", arguments...) }

// DateRange uses PostgreSQL constructor overloads for date bounds and inclusivity.
func DateRange(arguments ...Expr) Expr { return builtin("daterange", arguments...) }

// TSRange uses PostgreSQL constructor overloads for timestamp bounds and inclusivity.
func TSRange(arguments ...Expr) Expr { return builtin("tsrange", arguments...) }

// TSTZRange uses PostgreSQL constructor overloads for timestamptz bounds and inclusivity.
func TSTZRange(arguments ...Expr) Expr { return builtin("tstzrange", arguments...) }

func ToTSVector(arguments ...Expr) Expr         { return builtin("to_tsvector", arguments...) }
func ToTSQuery(arguments ...Expr) Expr          { return builtin("to_tsquery", arguments...) }
func PlainToTSQuery(arguments ...Expr) Expr     { return builtin("plainto_tsquery", arguments...) }
func PhraseToTSQuery(arguments ...Expr) Expr    { return builtin("phraseto_tsquery", arguments...) }
func WebsearchToTSQuery(arguments ...Expr) Expr { return builtin("websearch_to_tsquery", arguments...) }
func TSMatches(vector, query Expr) Condition    { return AsCondition(binary(vector, "@@", query)) }
func TSAnd(left, right Expr) Expr               { return binary(left, "&&", right) }
func TSOr(left, right Expr) Expr                { return binary(left, "||", right) }
func TSNot(query Expr) Expr                     { return prefix("!!", query) }
func TSPhrase(left, right Expr) Expr            { return binary(left, "<->", right) }
func TSRank(arguments ...Expr) Expr             { return builtin("ts_rank", arguments...) }
func TSRankCD(arguments ...Expr) Expr           { return builtin("ts_rank_cd", arguments...) }
func TSHeadline(arguments ...Expr) Expr         { return builtin("ts_headline", arguments...) }
func SetWeight(vector, weight Expr) Expr        { return builtin("setweight", vector, weight) }
