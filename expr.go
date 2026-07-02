// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"math/big"
	"strconv"
	"time"
)

// Expr is a node in Sequel's expression tree — the Go equivalent of the objects
// under Sequel::SQL. Every builder helper (Ident, Lit, BinaryOp, Function, …)
// produces an Expr, and Expr values compose via And/Or/Not and the comparison
// helpers to form the tree a Dataset literalizes into SQL.
type Expr interface {
	// litSQL appends this node's SQL to b.
	litSQL(b *builder)
}

// ---- Identifiers --------------------------------------------------------

// Identifier is an unqualified column or table name — Sequel[:col]. It is
// quoted per the dialect when quoting is enabled.
type Identifier struct{ Value string }

// Ident builds an Identifier. It is the Go form of Sequel[:name].
func Ident(name string) Identifier { return Identifier{Value: name} }

func (i Identifier) litSQL(b *builder) { b.WriteString(b.dialect.quoteIdentifier(i.Value)) }

// Col returns a QualifiedIdentifier for this table's column — the Go form of
// Sequel[:table][:col].
func (i Identifier) Col(col string) QualifiedIdentifier {
	return QualifiedIdentifier{Table: i.Value, Column: col}
}

// As aliases this identifier — Sequel[:x].as(:y).
func (i Identifier) As(alias string) AliasedExpr { return AliasedExpr{Expr: i, Alias: alias} }

// QualifiedIdentifier is a table-qualified column — Sequel[:t][:c] -> t.c.
type QualifiedIdentifier struct {
	Table  string
	Column string
}

// Qualify builds a QualifiedIdentifier (table.column).
func Qualify(table, column string) QualifiedIdentifier {
	return QualifiedIdentifier{Table: table, Column: column}
}

func (q QualifiedIdentifier) litSQL(b *builder) {
	b.WriteString(b.dialect.quoteIdentifier(q.Table))
	b.WriteByte('.')
	b.WriteString(b.dialect.quoteIdentifier(q.Column))
}

// As aliases this qualified identifier.
func (q QualifiedIdentifier) As(alias string) AliasedExpr { return AliasedExpr{Expr: q, Alias: alias} }

// ---- Literals -----------------------------------------------------------

// LiteralString is raw, already-formed SQL — Sequel.lit("a = 1"). It is emitted
// verbatim.
type LiteralString struct{ Value string }

// Lit builds a LiteralString, emitted verbatim into the SQL.
func Lit(sql string) LiteralString { return LiteralString{Value: sql} }

func (l LiteralString) litSQL(b *builder) { b.WriteString(l.Value) }

// literalValue wraps a plain Go value so it can appear inside an Expr tree.
type literalValue struct{ v Value }

func (l literalValue) litSQL(b *builder) { b.litValue(l.v) }

// Expr lifts an arbitrary value into an Expr node. A hash-like condition is
// passed as a []HashPair (see BoolHash); a bare Go value becomes a literal.
func exprOf(v Value) Expr {
	if e, ok := v.(Expr); ok {
		return e
	}
	if d, ok := v.(*Dataset); ok {
		return subquery{d}
	}
	return literalValue{v}
}

// ---- Aliasing -----------------------------------------------------------

// AliasedExpr is `expr AS alias` — Sequel.as(expr, :alias).
type AliasedExpr struct {
	Expr  Expr
	Alias string
}

// As builds an AliasedExpr (expr AS alias). The expr argument may be a bare
// value (column name via Ident, or a literal) or any Expr.
func As(expr Value, alias string) AliasedExpr {
	return AliasedExpr{Expr: coerceColumn(expr), Alias: alias}
}

func (a AliasedExpr) litSQL(b *builder) {
	a.Expr.litSQL(b)
	b.WriteString(" AS ")
	b.WriteString(b.dialect.quoteIdentifier(a.Alias))
}

// coerceColumn turns a value used in a column position into an Expr: a string
// becomes an Identifier, everything else defers to exprOf.
func coerceColumn(v Value) Expr {
	switch x := v.(type) {
	case Expr:
		return x
	case string:
		return Identifier{Value: x}
	case *Dataset:
		return subquery{x}
	default:
		return literalValue{v}
	}
}

// ---- Ordering -----------------------------------------------------------

// OrderedExpr is an ORDER BY term with a direction — Sequel.asc/desc(expr).
type OrderedExpr struct {
	Expr       Expr
	Descending bool
}

// Asc marks a column/expression ascending in ORDER BY.
func Asc(v Value) OrderedExpr { return OrderedExpr{Expr: coerceColumn(v)} }

// Desc marks a column/expression descending in ORDER BY.
func Desc(v Value) OrderedExpr { return OrderedExpr{Expr: coerceColumn(v), Descending: true} }

func (o OrderedExpr) litSQL(b *builder) {
	o.Expr.litSQL(b)
	if o.Descending {
		b.WriteString(" DESC")
	} else {
		b.WriteString(" ASC")
	}
}

// reversed flips the direction of an ORDER BY term (Dataset#reverse). A bare
// column defaults to ascending, so its reverse is descending.
func reversed(e Expr) Expr {
	if o, ok := e.(OrderedExpr); ok {
		return OrderedExpr{Expr: o.Expr, Descending: !o.Descending}
	}
	return OrderedExpr{Expr: e, Descending: true}
}

// ---- Functions ----------------------------------------------------------

// FunctionCall is a SQL function invocation — Sequel.function(:name, args...).
type FunctionCall struct {
	Name string
	Args []Expr
}

// Function builds a function call. Each argument is coerced: strings become
// identifiers (column references), other values become literals.
func Function(name string, args ...Value) FunctionCall {
	fc := FunctionCall{Name: name}
	for _, a := range args {
		fc.Args = append(fc.Args, coerceColumn(a))
	}
	return fc
}

func (f FunctionCall) litSQL(b *builder) {
	b.WriteString(f.Name)
	b.WriteByte('(')
	for i, a := range f.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		a.litSQL(b)
	}
	b.WriteByte(')')
}

// As aliases a function call.
func (f FunctionCall) As(alias string) AliasedExpr { return AliasedExpr{Expr: f, Alias: alias} }

// ---- Boolean / comparison operators -------------------------------------

// booleanOp is an infix comparison/boolean operator node.
type booleanOp struct {
	op    string
	left  Expr
	right Expr
}

func (o booleanOp) litSQL(b *builder) {
	b.WriteByte('(')
	o.left.litSQL(b)
	// A boolean right-hand side under the Default dialect uses IS TRUE/IS FALSE
	// with no separate operator, mirroring Sequel's base literalization.
	if lv, ok := o.right.(literalValue); ok {
		if bv, isBool := lv.v.(bool); isBool && b.dialect.boolIsInfix() && (o.op == "=" || o.op == "!=") {
			b.WriteByte(' ')
			if o.op == "!=" {
				b.WriteString("IS NOT ")
				b.WriteString(boolWord(bv))
			} else {
				b.WriteString(b.dialect.literalBoolIS(bv))
			}
			b.WriteByte(')')
			return
		}
		if lv.v == nil && (o.op == "=" || o.op == "!=") {
			b.WriteByte(' ')
			if o.op == "!=" {
				b.WriteString("IS NOT NULL")
			} else {
				b.WriteString("IS NULL")
			}
			b.WriteByte(')')
			return
		}
	}
	b.WriteByte(' ')
	b.WriteString(o.op)
	b.WriteByte(' ')
	o.right.litSQL(b)
	b.WriteByte(')')
}

func boolWord(v bool) string {
	if v {
		return "TRUE"
	}
	return "FALSE"
}

// Cmp builds a comparison node for the given operator (=, !=, >, <, >=, <=).
// left is coerced as a column, right as a literal value.
func Cmp(op string, left, right Value) Expr {
	return booleanOp{op: op, left: coerceColumn(left), right: exprOf(right)}
}

// Gt/Lt/Gte/Lte/Eq/Neq are convenience comparison builders.
func Gt(l, r Value) Expr  { return Cmp(">", l, r) }
func Lt(l, r Value) Expr  { return Cmp("<", l, r) }
func Gte(l, r Value) Expr { return Cmp(">=", l, r) }
func Lte(l, r Value) Expr { return Cmp("<=", l, r) }
func Eq(l, r Value) Expr  { return Cmp("=", l, r) }
func Neq(l, r Value) Expr { return Cmp("!=", l, r) }

// ---- Arithmetic ---------------------------------------------------------

// arithOp is an infix arithmetic operator (+, -, *, /).
type arithOp struct {
	op    string
	left  Expr
	right Expr
}

func (o arithOp) litSQL(b *builder) {
	b.WriteByte('(')
	o.left.litSQL(b)
	b.WriteByte(' ')
	b.WriteString(o.op)
	b.WriteByte(' ')
	o.right.litSQL(b)
	b.WriteByte(')')
}

// Arith builds an arithmetic node (op in +, -, *, /). The concrete type is
// returned so .As can alias it directly.
func Arith(op string, left, right Value) arithOp {
	return arithOp{op: op, left: coerceColumn(left), right: coerceColumn(right)}
}

// As aliases an arithmetic expression.
func (o arithOp) As(alias string) AliasedExpr { return AliasedExpr{Expr: o, Alias: alias} }

// ---- LIKE / IN ----------------------------------------------------------

// likeOp is a LIKE comparison — Sequel.like(col, pattern). It always appends the
// backslash ESCAPE clause Sequel emits.
type likeOp struct {
	left    Expr
	pattern Expr
	negate  bool
}

// Like builds a LIKE expression (col LIKE pattern ESCAPE '\').
func Like(col, pattern Value) Expr {
	return likeOp{left: coerceColumn(col), pattern: exprOf(pattern)}
}

func (o likeOp) litSQL(b *builder) {
	b.WriteByte('(')
	o.left.litSQL(b)
	if o.negate {
		b.WriteString(" NOT LIKE ")
	} else {
		b.WriteString(" LIKE ")
	}
	o.pattern.litSQL(b)
	b.WriteString(` ESCAPE '\'`)
	b.WriteByte(')')
}

// inOp is a col IN (values) / col IN (sub-select) comparison.
type inOp struct {
	left   Expr
	values []Value
	sub    *Dataset
	negate bool
}

// In builds a col IN (...) expression from an explicit value list.
func In(col Value, values ...Value) Expr {
	return inOp{left: coerceColumn(col), values: values}
}

// InDataset builds a col IN (sub-select) expression.
func InDataset(col Value, sub *Dataset) Expr {
	return inOp{left: coerceColumn(col), sub: sub}
}

func (o inOp) litSQL(b *builder) {
	b.WriteByte('(')
	o.left.litSQL(b)
	if o.negate {
		b.WriteString(" NOT IN ")
	} else {
		b.WriteString(" IN ")
	}
	b.WriteByte('(')
	if o.sub != nil {
		o.sub.appendSelect(b)
	} else {
		for i, v := range o.values {
			if i > 0 {
				b.WriteString(", ")
			}
			b.litValue(v)
		}
	}
	b.WriteByte(')')
	b.WriteByte(')')
}

// ---- Logical composition ------------------------------------------------

// logicalOp is AND / OR over a list of operands.
type logicalOp struct {
	op       string // "AND" or "OR"
	operands []Expr
}

func (o logicalOp) litSQL(b *builder) {
	b.WriteByte('(')
	for i, e := range o.operands {
		if i > 0 {
			b.WriteByte(' ')
			b.WriteString(o.op)
			b.WriteByte(' ')
		}
		e.litSQL(b)
	}
	b.WriteByte(')')
}

// And composes conditions with AND.
func And(conds ...Value) Expr { return logical("AND", conds) }

// Or composes conditions with OR.
func Or(conds ...Value) Expr { return logical("OR", conds) }

func logical(op string, conds []Value) Expr {
	ops := make([]Expr, 0, len(conds))
	for _, c := range conds {
		ops = append(ops, condOf(c))
	}
	return logicalOp{op: op, operands: ops}
}

// notOp negates a condition — Sequel.~ / Dataset#exclude.
type notOp struct{ inner Expr }

func (o notOp) litSQL(b *builder) {
	switch e := o.inner.(type) {
	case booleanOp:
		neg := e
		neg.op = negateCmp(e.op)
		neg.litSQL(b)
	case likeOp:
		neg := e
		neg.negate = !e.negate
		neg.litSQL(b)
	case inOp:
		neg := e
		neg.negate = !e.negate
		neg.litSQL(b)
	case logicalOp:
		flipped := logicalOp{op: flipLogical(e.op)}
		for _, sub := range e.operands {
			flipped.operands = append(flipped.operands, notOp{sub})
		}
		flipped.litSQL(b)
	default:
		b.WriteString("NOT ")
		o.inner.litSQL(b)
	}
}

// Not negates a condition (Sequel.~ / Dataset#exclude).
func Not(cond Value) Expr { return notOp{condOf(cond)} }

func negateCmp(op string) string {
	switch op {
	case "=":
		return "!="
	case "!=":
		return "="
	case ">":
		return "<="
	case "<":
		return ">="
	case ">=":
		return "<"
	case "<=":
		return ">"
	default:
		return op
	}
}

func flipLogical(op string) string {
	if op == "AND" {
		return "OR"
	}
	return "AND"
}

// ---- Sub-selects --------------------------------------------------------

// subquery wraps a Dataset used as a scalar/list expression: (SELECT ...).
type subquery struct{ ds *Dataset }

func (s subquery) litSQL(b *builder) {
	b.WriteByte('(')
	s.ds.appendSelect(b)
	b.WriteByte(')')
}

// ---- Hash conditions ----------------------------------------------------

// HashPair is one key/value of a hash condition — {key => value}. Key is the
// column (a string name or an Expr), Value is the literal compared with =
// (or IS NULL / IN for nil and lists).
type HashPair struct {
	Key   Value
	Value Value
}

// H builds an ordered hash condition from alternating key/value pairs, matching
// Ruby's insertion-ordered Hash. It is the Go form of `where(a: 1, b: 2)`:
//
//	H("a", 1, "b", 2)  ->  ((a = 1) AND (b = 2))
//
// A nil value becomes IS NULL; a []Value value becomes IN (...); a *Dataset
// becomes IN (sub-select).
func H(kv ...Value) Expr {
	if len(kv)%2 != 0 {
		panic("sequel.H: odd number of arguments")
	}
	pairs := make([]HashPair, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		pairs = append(pairs, HashPair{Key: kv[i], Value: kv[i+1]})
	}
	return hashCond(pairs)
}

// hashCond turns an ordered set of pairs into an AND of comparisons.
func hashCond(pairs []HashPair) Expr {
	ops := make([]Expr, 0, len(pairs))
	for _, p := range pairs {
		ops = append(ops, pairToExpr(p))
	}
	if len(ops) == 1 {
		return ops[0]
	}
	return logicalOp{op: "AND", operands: ops}
}

func pairToExpr(p HashPair) Expr {
	left := coerceColumn(p.Key)
	switch v := p.Value.(type) {
	case nil:
		return booleanOp{op: "=", left: left, right: literalValue{nil}}
	case []Value:
		return inOp{left: left, values: v}
	case *Dataset:
		return inOp{left: left, sub: v}
	default:
		return booleanOp{op: "=", left: left, right: exprOf(p.Value)}
	}
}

// parenExpr wraps an inner Expr in parentheses. It is used for a raw literal
// filter, which Sequel parenthesizes as a boolean condition (e.g.
// `where(Sequel.lit("a = 1"))` -> `WHERE (a = 1)`).
type parenExpr struct{ inner Expr }

func (p parenExpr) litSQL(b *builder) {
	b.WriteByte('(')
	p.inner.litSQL(b)
	b.WriteByte(')')
}

// condOf turns a value used in a WHERE/HAVING position into a boolean Expr:
// a []HashPair is a hash condition; a raw LiteralString is parenthesized as a
// boolean filter (Sequel.lit semantics); any other Expr is used as-is.
func condOf(v Value) Expr {
	switch x := v.(type) {
	case parenExpr:
		return x
	case LiteralString:
		return parenExpr{x}
	case Expr:
		return x
	case []HashPair:
		return hashCond(x)
	default:
		return literalValue{v}
	}
}

// ---- The builder value serializer --------------------------------------

// litValue appends the SQL literal for a plain Go value, dispatching on its
// dynamic type. Expr values recurse; unknown types fall back to their %v form
// as a quoted string, matching Sequel treating unknown objects via to_s.
func (b *builder) litValue(v Value) {
	switch x := v.(type) {
	case nil:
		b.WriteString("NULL")
	case bool:
		// A bare boolean value (not in a comparison) literalizes to the
		// dialect's boolean word; under Default that is TRUE / FALSE.
		if b.dialect == Default {
			b.WriteString(boolWord(x))
		} else {
			b.WriteString(b.dialect.literalBool(x))
		}
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case *big.Int:
		b.WriteString(literalBig(x))
	case float64:
		b.WriteString(literalFloat(x))
	case string:
		b.WriteString(b.dialect.literalString(x))
	case Blob:
		b.WriteString(b.dialect.literalBlob([]byte(x)))
	case []byte:
		b.WriteString(b.dialect.literalBlob(x))
	case time.Time:
		b.WriteString(literalTime(x))
	case Date:
		b.WriteString(literalDate(x))
	case []Value:
		b.WriteByte('(')
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			b.litValue(e)
		}
		b.WriteByte(')')
	case Expr:
		x.litSQL(b)
	case *Dataset:
		b.WriteByte('(')
		x.appendSelect(b)
		b.WriteByte(')')
	default:
		b.WriteString(b.dialect.literalString(defaultString(v)))
	}
}

// defaultString renders an unknown value like Ruby's to_s for the fallback path.
func defaultString(v Value) string {
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return ""
}
