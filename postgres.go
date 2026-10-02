package qs

// Array constructs a SQL array from expressions. Cast empty arrays explicitly.
func Array(values ...Expr) Expr { return listExpr("ARRAY", values) }

// ArrayFrom constructs an array from a single-column subquery.
func ArrayFrom(query Rowset) Expr { return Fragment(UnsafeSQL("ARRAY"), Scalar(query)) }

// ArrayContains tests whether an array contains every element of another array.
func ArrayContains(array, values Expr) Condition { return AsCondition(binary(array, "@>", values)) }

// ArrayContainedBy tests whether an array is contained by another array.
func ArrayContainedBy(array, values Expr) Condition { return AsCondition(binary(array, "<@", values)) }

// ArrayOverlaps tests whether two arrays share any elements.
func ArrayOverlaps(left, right Expr) Condition { return AsCondition(binary(left, "&&", right)) }

// ArrayAppend appends a value to an array.
func ArrayAppend(array, value Expr) Expr { return builtin("array_append", array, value) }

// ArrayPrepend prepends a value to an array.
func ArrayPrepend(value, array Expr) Expr { return builtin("array_prepend", value, array) }

// ArrayCat concatenates two arrays.
func ArrayCat(left, right Expr) Expr { return builtin("array_cat", left, right) }

// ArrayLength returns the length of an array dimension.
func ArrayLength(array, dimension Expr) Expr { return builtin("array_length", array, dimension) }

// ArrayLower returns the lower bound of an array dimension.
func ArrayLower(array, dimension Expr) Expr { return builtin("array_lower", array, dimension) }

// ArrayUpper returns the upper bound of an array dimension.
func ArrayUpper(array, dimension Expr) Expr { return builtin("array_upper", array, dimension) }

// ArrayPosition returns the first position of a value in an array.
func ArrayPosition(array, value Expr) Expr { return builtin("array_position", array, value) }

// ArrayPositions returns all positions of a value in an array.
func ArrayPositions(array, value Expr) Expr { return builtin("array_positions", array, value) }

// ArrayRemove removes all occurrences of a value from an array.
func ArrayRemove(array, value Expr) Expr { return builtin("array_remove", array, value) }

// ArrayReplace replaces all occurrences of one value in an array.
func ArrayReplace(array, old, replacement Expr) Expr {
	return builtin("array_replace", array, old, replacement)
}

// Cardinality returns the total number of elements in an array.
func Cardinality(array Expr) Expr { return builtin("cardinality", array) }

// Unnest expands one or more arrays into rows.
func Unnest(arrays ...Expr) Expr { return builtin("unnest", arrays...) }

// StringToArray splits a string into an array using delimiter.
func StringToArray(value, delimiter Expr) Expr { return builtin("string_to_array", value, delimiter) }

// ArrayToString joins array elements using delimiter.
func ArrayToString(value, delimiter Expr) Expr { return builtin("array_to_string", value, delimiter) }

// JSONGet extracts a JSON object field or array element with the -> operator.
func JSONGet(document, key Expr) Expr { return binary(document, "->", key) }

// JSONGetText extracts a JSON field or element as text with the ->> operator.
func JSONGetText(document, key Expr) Expr { return binary(document, "->>", key) }

// JSONGetPath extracts a JSON value at a path with the #> operator.
func JSONGetPath(document, path Expr) Expr { return binary(document, "#>", path) }

// JSONGetPathText extracts a JSON value at a path as text with the #>> operator.
func JSONGetPathText(document, path Expr) Expr { return binary(document, "#>>", path) }

// JSONContains tests whether a JSON value contains another JSON value.
func JSONContains(document, value Expr) Condition { return AsCondition(binary(document, "@>", value)) }

// JSONContainedBy tests whether a JSON value is contained by another value.
func JSONContainedBy(document, value Expr) Condition {
	return AsCondition(binary(document, "<@", value))
}

// JSONHas tests whether a JSON object contains a key or an array contains a string.
func JSONHas(document, key Expr) Condition { return AsCondition(binary(document, "?", key)) }

// JSONHasAny tests whether a JSON object contains any key from keys.
func JSONHasAny(document, keys Expr) Condition { return AsCondition(binary(document, "?|", keys)) }

// JSONHasAll tests whether a JSON object contains every key from keys.
func JSONHasAll(document, keys Expr) Condition { return AsCondition(binary(document, "?&", keys)) }

// JSONDelete removes a JSON object field or array element with the - operator.
func JSONDelete(document, key Expr) Expr { return binary(document, "-", key) }

// JSONDeletePath removes a JSON value at a path with the #- operator.
func JSONDeletePath(document, path Expr) Expr { return binary(document, "#-", path) }

// JSONPathExists tests whether a JSON path returns any item.
func JSONPathExists(document, path Expr) Condition { return AsCondition(binary(document, "@?", path)) }

// JSONPathMatches tests whether a JSON path predicate matches a document.
func JSONPathMatches(document, path Expr) Condition { return AsCondition(binary(document, "@@", path)) }

// ToJSON converts a value to json.
func ToJSON(value Expr) Expr { return builtin("to_json", value) }

// ToJSONB converts a value to jsonb.
func ToJSONB(value Expr) Expr { return builtin("to_jsonb", value) }

// RowToJSON converts a composite value to json.
func RowToJSON(value Expr) Expr { return builtin("row_to_json", value) }

// JSONBuildArray constructs a JSON array from values.
func JSONBuildArray(values ...Expr) Expr { return builtin("json_build_array", values...) }

// JSONBBuildArray constructs a JSONB array from values.
func JSONBBuildArray(values ...Expr) Expr { return builtin("jsonb_build_array", values...) }

// JSONBuildObject constructs a JSON object from alternating key and value expressions.
func JSONBuildObject(pairs ...Expr) Expr {
	if len(pairs)%2 != 0 {
		return invalidExpr("JSON object", "requires key/value pairs")
	}
	return builtin("json_build_object", pairs...)
}

// JSONBBuildObject constructs a JSONB object from alternating key and value expressions.
func JSONBBuildObject(pairs ...Expr) Expr {
	if len(pairs)%2 != 0 {
		return invalidExpr("JSON object", "requires key/value pairs")
	}
	return builtin("jsonb_build_object", pairs...)
}

// JSONBSet replaces or adds a JSONB value at path, optionally creating missing keys.
func JSONBSet(document, path, value Expr, createMissing bool) Expr {
	return builtin("jsonb_set", document, path, value, Param(createMissing))
}

// JSONBInsert inserts a JSONB value at path, optionally after the target.
func JSONBInsert(document, path, value Expr, after bool) Expr {
	return builtin("jsonb_insert", document, path, value, Param(after))
}

// JSONBStripNulls removes null-valued object fields from a JSONB value.
func JSONBStripNulls(document Expr) Expr { return builtin("jsonb_strip_nulls", document) }

// JSONBArrayElements expands a JSONB array into JSONB values.
func JSONBArrayElements(document Expr) Expr { return builtin("jsonb_array_elements", document) }

// JSONBArrayElementsText expands a JSONB array into text values.
func JSONBArrayElementsText(document Expr) Expr {
	return builtin("jsonb_array_elements_text", document)
}

// JSONBEach expands a JSONB object into key/value JSONB pairs.
func JSONBEach(document Expr) Expr { return builtin("jsonb_each", document) }

// JSONBEachText expands a JSONB object into key/value text pairs.
func JSONBEachText(document Expr) Expr { return builtin("jsonb_each_text", document) }

// JSONBObjectKeys expands a JSONB object into its keys.
func JSONBObjectKeys(document Expr) Expr { return builtin("jsonb_object_keys", document) }

// JSONBTypeOf returns the JSONB value's top-level type name.
func JSONBTypeOf(document Expr) Expr { return builtin("jsonb_typeof", document) }

// JSONBToRecord expands a JSONB object into a caller-specified record shape.
func JSONBToRecord(document Expr) Expr { return builtin("jsonb_to_record", document) }

// JSONBToRecordset expands a JSONB array into caller-specified record rows.
func JSONBToRecordset(document Expr) Expr { return builtin("jsonb_to_recordset", document) }

// JSONBPathQuery returns JSONB values produced by a JSON path query.
func JSONBPathQuery(arguments ...Expr) Expr { return builtin("jsonb_path_query", arguments...) }

// JSONBPathQueryArray returns a JSONB array produced by a JSON path query.
func JSONBPathQueryArray(arguments ...Expr) Expr {
	return builtin("jsonb_path_query_array", arguments...)
}

// JSONBPathQueryFirst returns the first JSONB value produced by a JSON path query.
func JSONBPathQueryFirst(arguments ...Expr) Expr {
	return builtin("jsonb_path_query_first", arguments...)
}

// RangeContains tests whether a range contains a value or another range.
func RangeContains(r, value Expr) Condition { return AsCondition(binary(r, "@>", value)) }

// RangeContainedBy tests whether a range is contained by another range.
func RangeContainedBy(r, value Expr) Condition { return AsCondition(binary(r, "<@", value)) }

// RangeOverlaps tests whether two ranges overlap.
func RangeOverlaps(left, right Expr) Condition { return AsCondition(binary(left, "&&", right)) }

// RangeAdjacent tests whether two ranges are adjacent.
func RangeAdjacent(left, right Expr) Condition { return AsCondition(binary(left, "-|-", right)) }

// RangeBefore tests whether the left range ends before the right range starts.
func RangeBefore(left, right Expr) Condition { return AsCondition(binary(left, "<<", right)) }

// RangeAfter tests whether the left range starts after the right range ends.
func RangeAfter(left, right Expr) Condition { return AsCondition(binary(left, ">>", right)) }

// RangeDoesNotExtendRight tests whether the left range does not extend right of the right range.
func RangeDoesNotExtendRight(left, right Expr) Condition {
	return AsCondition(binary(left, "&<", right))
}

// RangeDoesNotExtendLeft tests whether the left range does not extend left of the right range.
func RangeDoesNotExtendLeft(left, right Expr) Condition {
	return AsCondition(binary(left, "&>", right))
}

// RangeUnion returns the union of two ranges.
func RangeUnion(left, right Expr) Expr { return binary(left, "+", right) }

// RangeIntersection returns the intersection of two ranges.
func RangeIntersection(left, right Expr) Expr { return binary(left, "*", right) }

// RangeDifference returns the difference between two ranges.
func RangeDifference(left, right Expr) Expr { return binary(left, "-", right) }

// RangeLower returns the lower bound of a range.
func RangeLower(value Expr) Expr { return builtin("lower", value) }

// RangeUpper returns the upper bound of a range.
func RangeUpper(value Expr) Expr { return builtin("upper", value) }

// RangeEmpty tests whether a range is empty.
func RangeEmpty(value Expr) Condition { return AsCondition(builtin("isempty", value)) }

// RangeLowerInclusive tests whether a range includes its lower bound.
func RangeLowerInclusive(value Expr) Condition { return AsCondition(builtin("lower_inc", value)) }

// RangeUpperInclusive tests whether a range includes its upper bound.
func RangeUpperInclusive(value Expr) Condition { return AsCondition(builtin("upper_inc", value)) }

// RangeLowerInfinite tests whether a range has no lower bound.
func RangeLowerInfinite(value Expr) Condition { return AsCondition(builtin("lower_inf", value)) }

// RangeUpperInfinite tests whether a range has no upper bound.
func RangeUpperInfinite(value Expr) Condition { return AsCondition(builtin("upper_inf", value)) }

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

// ToTSVector constructs a tsvector from PostgreSQL's text-search arguments.
func ToTSVector(arguments ...Expr) Expr { return builtin("to_tsvector", arguments...) }

// ToTSQuery parses text-search query syntax into a tsquery.
func ToTSQuery(arguments ...Expr) Expr { return builtin("to_tsquery", arguments...) }

// PlainToTSQuery parses plain text into a tsquery.
func PlainToTSQuery(arguments ...Expr) Expr { return builtin("plainto_tsquery", arguments...) }

// PhraseToTSQuery parses plain text into a phrase tsquery.
func PhraseToTSQuery(arguments ...Expr) Expr { return builtin("phraseto_tsquery", arguments...) }

// WebsearchToTSQuery parses web-search-style text into a tsquery.
func WebsearchToTSQuery(arguments ...Expr) Expr { return builtin("websearch_to_tsquery", arguments...) }

// TSMatches tests whether a tsvector matches a tsquery.
func TSMatches(vector, query Expr) Condition { return AsCondition(binary(vector, "@@", query)) }

// TSAnd combines two tsquery expressions with AND.
func TSAnd(left, right Expr) Expr { return binary(left, "&&", right) }

// TSOr combines two tsquery expressions with OR.
func TSOr(left, right Expr) Expr { return binary(left, "||", right) }

// TSNot negates a tsquery expression.
func TSNot(query Expr) Expr { return prefix("!!", query) }

// TSPhrase combines two tsquery expressions with the phrase operator.
func TSPhrase(left, right Expr) Expr { return binary(left, "<->", right) }

// TSRank computes the relevance rank of a tsvector and query.
func TSRank(arguments ...Expr) Expr { return builtin("ts_rank", arguments...) }

// TSRankCD computes cover-density relevance rank for a tsvector and query.
func TSRankCD(arguments ...Expr) Expr { return builtin("ts_rank_cd", arguments...) }

// TSHeadline generates highlighted text for a text-search query.
func TSHeadline(arguments ...Expr) Expr { return builtin("ts_headline", arguments...) }

// SetWeight assigns a weight label to a tsvector.
func SetWeight(vector, weight Expr) Expr { return builtin("setweight", vector, weight) }
