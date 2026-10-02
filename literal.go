package qs

import "strings"

// LiteralNumeric preserves a numeric token exactly, without float64 rounding.
// It accepts a sign, decimal point/exponent, and PostgreSQL 16+ integer bases
// and digit separators. Use Param for application values.
func LiteralNumeric(value string) Expr {
	if !validNumericLiteral(value) {
		return invalidExpr("numeric literal", "requires a numeric token with valid digits and separators")
	}
	e := Expr{kind: exprLiteral, text: value}
	i := 0
	if value[0] == '+' || value[0] == '-' {
		i++
	}
	if strings.IndexByte(value, '_') >= 0 || len(value)-i >= 2 && value[i] == '0' && numericBase(value[i+1]) != 0 {
		return versionExpression(PostgreSQL16, "numeric bases and separators", e)
	}
	return e
}

func validNumericLiteral(value string) bool {
	i := 0
	if value != "" && (value[0] == '+' || value[0] == '-') {
		i++
	}
	if len(value)-i >= 2 && value[i] == '0' {
		if base := numericBase(value[i+1]); base != 0 {
			end, digits := numericDigits(value, i+2, base, true)
			return digits > 0 && end == len(value)
		}
	}
	i, digits := numericDigits(value, i, 10, false)
	if i < len(value) && value[i] == '.' {
		i++
		var fraction int
		i, fraction = numericDigits(value, i, 10, false)
		digits += fraction
	}
	if digits == 0 {
		return false
	}
	if i < len(value) && (value[i] == 'e' || value[i] == 'E') {
		i++
		if i < len(value) && (value[i] == '+' || value[i] == '-') {
			i++
		}
		var exponent int
		i, exponent = numericDigits(value, i, 10, false)
		if exponent == 0 {
			return false
		}
	}
	return i == len(value)
}

func numericBase(prefix byte) byte {
	switch prefix {
	case 'b', 'B':
		return 2
	case 'o', 'O':
		return 8
	case 'x', 'X':
		return 16
	}
	return 0
}

func numericDigit(digit, base byte) bool {
	if digit >= '0' && digit <= '9' {
		return digit-'0' < base
	}
	return base == 16 && (digit >= 'a' && digit <= 'f' || digit >= 'A' && digit <= 'F')
}

func numericDigits(value string, start int, base byte, leadingSeparator bool) (int, int) {
	i, count := start, 0
	if leadingSeparator && i < len(value) && value[i] == '_' {
		i++
	}
	for i < len(value) && numericDigit(value[i], base) {
		i++
		count++
		if i+1 < len(value) && value[i] == '_' && numericDigit(value[i+1], base) {
			i++
		}
	}
	return i, count
}

// LiteralBit emits a PostgreSQL bit-string literal, including the empty string.
func LiteralBit(bits string) Expr {
	for i := range len(bits) {
		if bits[i] != '0' && bits[i] != '1' {
			return invalidExpr("bit literal", "requires binary digits")
		}
	}
	return Expr{kind: exprLiteral, text: "B'" + bits + "'"}
}

// LiteralHex emits a PostgreSQL hexadecimal bit-string literal.
func LiteralHex(digits string) Expr {
	for i := range len(digits) {
		c := digits[i]
		if !hexDigit(c) {
			return invalidExpr("hex literal", "requires hexadecimal digits")
		}
	}
	return Expr{kind: exprLiteral, text: "X'" + digits + "'"}
}

func hexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
