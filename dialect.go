// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// Dialect names a SQL dialect. It selects identifier quoting and value
// literalization so the emitted SQL is byte-faithful to what the matching
// Sequel adapter produces.
type Dialect int

const (
	// Default is Sequel's base dialect (the abstract Database). Identifiers are
	// unquoted; booleans literalize to IS TRUE / IS FALSE; blobs to a
	// hex-escaped C string.
	Default Dialect = iota
	// SQLite mirrors Sequel's sqlite adapter: backtick-quoted identifiers,
	// booleans as 't'/'f', blobs as X'..'.
	SQLite
	// Postgres mirrors Sequel's postgres adapter: double-quote-quoted
	// identifiers, booleans as true/false, blobs as '\\ooo' octal escapes.
	Postgres
)

// String returns the dialect's canonical name.
func (d Dialect) String() string {
	switch d {
	case SQLite:
		return "sqlite"
	case Postgres:
		return "postgres"
	default:
		return "default"
	}
}

// DialectByName maps a Sequel adapter/host name to a Dialect. Unknown names map
// to Default, matching how Sequel's mock adapter degrades to the base SQL.
func DialectByName(name string) Dialect {
	switch strings.ToLower(name) {
	case "sqlite":
		return SQLite
	case "postgres", "postgresql", "pg":
		return Postgres
	default:
		return Default
	}
}

// quotesIdentifiers reports whether the dialect quotes identifiers by default.
func (d Dialect) quotesIdentifiers() bool { return d != Default }

// quoteIdentifier quotes a single identifier component per the dialect,
// doubling the closing quote inside. The Default dialect leaves it bare.
func (d Dialect) quoteIdentifier(s string) string {
	switch d {
	case SQLite:
		return "`" + strings.ReplaceAll(s, "`", "``") + "`"
	case Postgres:
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	default:
		return s
	}
}

// literalString quotes and escapes a string literal. Sequel's base
// literalization doubles single quotes and does not escape backslashes; the
// sqlite and postgres dialects share that behaviour for plain strings.
func (d Dialect) literalString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// literalBool literalizes a boolean per the dialect.
func (d Dialect) literalBool(b bool) string {
	switch d {
	case SQLite:
		if b {
			return "'t'"
		}
		return "'f'"
	case Postgres:
		if b {
			return "true"
		}
		return "false"
	default:
		if b {
			return "IS TRUE"
		}
		return "IS FALSE"
	}
}

// boolIsInfix reports whether `col = <bool>` renders with an IS-style infix
// (`col IS TRUE`). The Default and Postgres dialects do; SQLite compares against
// the 't'/'f' string literal instead.
func (d Dialect) boolIsInfix() bool { return d == Default || d == Postgres }

// literalBoolIS returns the IS-clause tail for a boolean (" IS TRUE"), used by
// the infix path so no stray separator space is emitted.
func (d Dialect) literalBoolIS(b bool) string {
	if b {
		return "IS TRUE"
	}
	return "IS FALSE"
}

// literalBlob literalizes a binary string per the dialect.
func (d Dialect) literalBlob(b []byte) string {
	switch d {
	case SQLite:
		return "X'" + hexLower(b) + "'"
	case Postgres:
		var sb strings.Builder
		sb.WriteByte('\'')
		for _, c := range b {
			if c >= 0x20 && c < 0x7f && c != '\'' && c != '\\' {
				sb.WriteByte(c)
			} else {
				fmt.Fprintf(&sb, `\%03o`, c)
			}
		}
		sb.WriteByte('\'')
		return sb.String()
	default:
		// Base Sequel renders a blob as a plain quoted string of its bytes,
		// doubling single quotes (bytes are emitted verbatim).
		var sb strings.Builder
		sb.WriteByte('\'')
		for _, c := range b {
			if c == '\'' {
				sb.WriteString("''")
			} else {
				sb.WriteByte(c)
			}
		}
		sb.WriteByte('\'')
		return sb.String()
	}
}

func hexLower(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}

// literalFloat formats a float64 the way Ruby's Float#to_s does, which is what
// Sequel emits. Ruby always shows a decimal point ("1.0", not "1") and uses a
// signed two-plus-digit exponent ("1.0e+20", "1.0e-07").
func literalFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant, exp := s[:i], s[i+1:]
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
		sign := "+"
		if exp[0] == '+' || exp[0] == '-' {
			if exp[0] == '-' {
				sign = "-"
			}
			exp = exp[1:]
		}
		if len(exp) < 2 {
			exp = strings.Repeat("0", 2-len(exp)) + exp
		}
		return mant + "e" + sign + exp
	}
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// literalTime formats a time.Time the way Sequel's base literalizer does:
// 'YYYY-MM-DD HH:MM:SS.ffffff'.
func literalTime(t time.Time) string {
	return "'" + t.Format("2006-01-02 15:04:05.000000") + "'"
}

// literalDate formats a Date as 'YYYY-MM-DD'.
func literalDate(d Date) string {
	return "'" + fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) + "'"
}

// literalBig formats an arbitrary-precision integer.
func literalBig(b *big.Int) string { return b.String() }
