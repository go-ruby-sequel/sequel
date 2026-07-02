// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

// Package sequel is a pure-Go (no cgo) reimplementation of the deterministic
// SQL-generation core of Ruby's Sequel toolkit (the `sequel` gem): the Dataset
// query builder, the expression DSL, the schema DSL, and per-dialect
// literalization/identifier-quoting. It emits the exact SQL strings the gem
// emits, byte-for-byte, across the default, sqlite and postgres dialects.
//
// What it is — and isn't. Turning a chain of `DB[:t].where(...).order(...)`
// calls into a SQL string is fully deterministic and needs no database and no
// Ruby runtime, so it lives here as pure Go. Actually running that SQL against
// a server is the host's job: a [Database] carries an injectable [Executor]
// seam (`Execute(sql) -> rows`) the host wires to a driver such as
// go-ruby-sqlite3 or go-ruby-pg. The SQL text is what this library generates
// and tests; execution is the seam.
package sequel

import (
	"math/big"
	"time"
)

// Value is a Ruby value in the small model this library literalizes. It mirrors
// the value model the host (go-embedded-ruby) maps to and from its own objects.
// The supported dynamic types are:
//
//	nil               -> NULL
//	bool              -> the dialect's boolean literal
//	int, int64        -> integer literal
//	*big.Int          -> integer literal
//	float64           -> float literal
//	string            -> a quoted, escaped string literal
//	[]byte, Blob      -> the dialect's blob literal
//	time.Time         -> a quoted timestamp literal
//	Date              -> a quoted date literal
//	Expr (Expression) -> literalized as SQL (idents, functions, sub-selects…)
//	*Dataset          -> a parenthesised sub-select
//	[]Value           -> a parenthesised, comma-joined list (IN (...))
type Value = any

// Blob is a binary string literal — Ruby's Sequel.blob. Each dialect renders it
// differently (default: hex-escaped C string; sqlite: X'..'; postgres: \\ooo).
type Blob []byte

// Date is a calendar date with no time-of-day — Ruby's Date. It literalizes to
// 'YYYY-MM-DD'. (A time.Time literalizes to a full timestamp.)
type Date struct {
	Year  int
	Month int
	Day   int
}

// NewDate builds a Date.
func NewDate(year, month, day int) Date { return Date{Year: year, Month: month, Day: day} }

// bigIntType is used by the literalizer to recognise arbitrary-precision ints.
var _ = (*big.Int)(nil)
var _ = time.Time{}
