// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import "strings"

// builder accumulates SQL text for one statement in the context of a dialect.
type builder struct {
	strings.Builder
	dialect Dialect
}

// newBuilder makes a builder for a dialect.
func newBuilder(d Dialect) *builder { return &builder{dialect: d} }

// source is one FROM / JOIN table reference.
type source struct {
	// name is a bare table name; expr, when non-nil, is an aliased or
	// sub-select source and takes precedence.
	name string
	expr Expr
}

func (s source) litSQL(b *builder) {
	if s.expr != nil {
		s.expr.litSQL(b)
		return
	}
	b.WriteString(b.dialect.quoteIdentifier(s.name))
}

// joinType selects the SQL keyword for a JOIN clause.
type joinType int

const (
	innerJoin joinType = iota
	leftJoin
	rightJoin
	fullJoin
	crossJoin
)

func (j joinType) keyword() string {
	switch j {
	case leftJoin:
		return "LEFT JOIN"
	case rightJoin:
		return "RIGHT JOIN"
	case fullJoin:
		return "FULL JOIN"
	case crossJoin:
		return "CROSS JOIN"
	default:
		return "INNER JOIN"
	}
}

// joinClause is one JOIN in a dataset.
type joinClause struct {
	typ   joinType
	table source
	// on is the ON condition (nil for a CROSS JOIN or a USING join).
	on Expr
	// using lists USING columns (empty unless a USING join).
	using []string
	// implicitHash records that this join was built from a hash of
	// {joinCol => sourceCol}; the ON is qualified against the joined table and
	// the last source. Stored resolved in `on` already; kept for clarity.
}

func (jc joinClause) litSQL(b *builder, lastSource string) {
	b.WriteByte(' ')
	b.WriteString(jc.typ.keyword())
	b.WriteByte(' ')
	jc.table.litSQL(b)
	if len(jc.using) > 0 {
		b.WriteString(" USING (")
		for i, c := range jc.using {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(b.dialect.quoteIdentifier(c))
		}
		b.WriteByte(')')
		return
	}
	if jc.on != nil {
		b.WriteString(" ON ")
		jc.on.litSQL(b)
	}
	_ = lastSource
}

// Dataset is an immutable, chainable query builder. Every filtering/ordering
// method returns a new Dataset that shares nothing mutable with the receiver, so
// datasets are safe to store and branch — matching Sequel's frozen datasets.
type Dataset struct {
	db      *Database
	sources []source

	columns  []Expr // SELECT list; empty means SELECT *
	wheres   []Expr
	havings  []Expr
	groups   []Expr
	orders   []Expr
	joins    []joinClause
	distinct bool

	limit  *limitClause
	offset Expr

	// compounds are UNION/INTERSECT/EXCEPT chained onto this dataset.
	compounds []compound
}

type limitClause struct {
	limit  Expr
	offset Expr
}

type compoundType int

const (
	compoundUnion compoundType = iota
	compoundIntersect
	compoundExcept
)

func (c compoundType) keyword() string {
	switch c {
	case compoundIntersect:
		return "INTERSECT"
	case compoundExcept:
		return "EXCEPT"
	default:
		return "UNION"
	}
}

type compound struct {
	typ compoundType
	ds  *Dataset
	all bool
}

// clone returns a shallow copy of the dataset with independent slices so the
// caller can append without mutating the original.
func (d *Dataset) clone() *Dataset {
	nd := *d
	nd.sources = append([]source(nil), d.sources...)
	nd.columns = append([]Expr(nil), d.columns...)
	nd.wheres = append([]Expr(nil), d.wheres...)
	nd.havings = append([]Expr(nil), d.havings...)
	nd.groups = append([]Expr(nil), d.groups...)
	nd.orders = append([]Expr(nil), d.orders...)
	nd.joins = append([]joinClause(nil), d.joins...)
	nd.compounds = append([]compound(nil), d.compounds...)
	return &nd
}

// lastSourceName returns the name of the most recent FROM/JOIN source, used to
// qualify a hash join condition ({joinCol => srcCol}).
func (d *Dataset) lastSourceName() string {
	if len(d.joins) > 0 {
		return d.joins[len(d.joins)-1].table.name
	}
	if len(d.sources) > 0 {
		return d.sources[len(d.sources)-1].name
	}
	return ""
}

// ---- Chainable builders -------------------------------------------------

// From replaces the dataset's FROM sources.
func (d *Dataset) From(tables ...Value) *Dataset {
	nd := d.clone()
	nd.sources = nil
	for _, t := range tables {
		nd.sources = append(nd.sources, sourceOf(t))
	}
	return nd
}

// sourceOf coerces a table argument to a source: a string/Identifier becomes a
// bare name, an AliasedExpr/sub-select becomes an expr source.
func sourceOf(t Value) source {
	switch x := t.(type) {
	case string:
		return source{name: x}
	case Identifier:
		return source{name: x.Value}
	case AliasedExpr:
		return source{expr: x}
	case *Dataset:
		return source{expr: subquery{x}}
	case Expr:
		return source{expr: x}
	default:
		return source{name: ""}
	}
}

// Select sets the SELECT column list. Columns are coerced: strings become
// identifiers, other Exprs are used directly.
func (d *Dataset) Select(cols ...Value) *Dataset {
	nd := d.clone()
	nd.columns = nil
	for _, c := range cols {
		nd.columns = append(nd.columns, coerceColumn(c))
	}
	return nd
}

// Where adds a filter, AND-combined with any existing filters.
func (d *Dataset) Where(cond Value) *Dataset {
	nd := d.clone()
	nd.wheres = append(nd.wheres, condOf(cond))
	return nd
}

// Exclude adds a negated filter (Sequel's Dataset#exclude).
func (d *Dataset) Exclude(cond Value) *Dataset {
	nd := d.clone()
	nd.wheres = append(nd.wheres, notOp{condOf(cond)})
	return nd
}

// Order sets the ORDER BY terms, replacing any existing ones.
func (d *Dataset) Order(terms ...Value) *Dataset {
	nd := d.clone()
	nd.orders = nil
	for _, t := range terms {
		nd.orders = append(nd.orders, orderTermOf(t))
	}
	return nd
}

// orderTermOf coerces an ORDER BY term: a bare column keeps its default
// (ascending, no keyword); an OrderedExpr carries its direction.
func orderTermOf(t Value) Expr {
	switch x := t.(type) {
	case OrderedExpr:
		return orderNoAscKeyword{x}
	case Expr:
		return x
	case string:
		return Identifier{Value: x}
	default:
		return literalValue{t}
	}
}

// orderNoAscKeyword renders an ascending OrderedExpr without the redundant "ASC"
// keyword (Sequel omits ASC, keeps DESC), and DESC when descending.
type orderNoAscKeyword struct{ o OrderedExpr }

func (o orderNoAscKeyword) litSQL(b *builder) {
	o.o.Expr.litSQL(b)
	if o.o.Descending {
		b.WriteString(" DESC")
	}
}

// Reverse flips every ORDER BY term's direction (Sequel's Dataset#reverse).
func (d *Dataset) Reverse() *Dataset {
	nd := d.clone()
	rev := make([]Expr, 0, len(nd.orders))
	for _, o := range nd.orders {
		rev = append(rev, reverseTerm(o))
	}
	nd.orders = rev
	return nd
}

func reverseTerm(e Expr) Expr {
	if x, ok := e.(orderNoAscKeyword); ok {
		return orderNoAscKeyword{OrderedExpr{Expr: x.o.Expr, Descending: !x.o.Descending}}
	}
	// A bare (unordered) column defaults to ascending, so its reverse is DESC.
	return orderNoAscKeyword{OrderedExpr{Expr: e, Descending: true}}
}

// Limit sets a LIMIT (and, when offset is non-nil, an OFFSET).
func (d *Dataset) Limit(limit int) *Dataset {
	nd := d.clone()
	nd.limit = &limitClause{limit: literalValue{limit}}
	return nd
}

// LimitOffset sets both LIMIT and OFFSET.
func (d *Dataset) LimitOffset(limit, offset int) *Dataset {
	nd := d.clone()
	nd.limit = &limitClause{limit: literalValue{limit}, offset: literalValue{offset}}
	return nd
}

// Offset sets an OFFSET with no LIMIT.
func (d *Dataset) Offset(offset int) *Dataset {
	nd := d.clone()
	nd.offset = literalValue{offset}
	return nd
}

// Distinct marks the SELECT as DISTINCT.
func (d *Dataset) Distinct() *Dataset {
	nd := d.clone()
	nd.distinct = true
	return nd
}

// Group sets the GROUP BY columns.
func (d *Dataset) Group(cols ...Value) *Dataset {
	nd := d.clone()
	nd.groups = nil
	for _, c := range cols {
		nd.groups = append(nd.groups, coerceColumn(c))
	}
	return nd
}

// Having adds a HAVING condition, AND-combined with any existing ones.
func (d *Dataset) Having(cond Value) *Dataset {
	nd := d.clone()
	nd.havings = append(nd.havings, condOf(cond))
	return nd
}

// ---- Joins --------------------------------------------------------------

// Join / InnerJoin add an INNER JOIN. cond may be a hash of
// {joinColumn => sourceColumn} (via H or JoinHash), a []string USING list, or
// any boolean Expr used verbatim as the ON condition.
func (d *Dataset) Join(table Value, cond Value) *Dataset { return d.joinWith(innerJoin, table, cond) }

// InnerJoin is an explicit alias for Join.
func (d *Dataset) InnerJoin(table Value, cond Value) *Dataset {
	return d.joinWith(innerJoin, table, cond)
}

// LeftJoin adds a LEFT JOIN.
func (d *Dataset) LeftJoin(table Value, cond Value) *Dataset {
	return d.joinWith(leftJoin, table, cond)
}

// RightJoin adds a RIGHT JOIN.
func (d *Dataset) RightJoin(table Value, cond Value) *Dataset {
	return d.joinWith(rightJoin, table, cond)
}

// FullJoin adds a FULL JOIN.
func (d *Dataset) FullJoin(table Value, cond Value) *Dataset {
	return d.joinWith(fullJoin, table, cond)
}

// CrossJoin adds a CROSS JOIN (no ON condition).
func (d *Dataset) CrossJoin(table Value) *Dataset {
	nd := d.clone()
	nd.joins = append(nd.joins, joinClause{typ: crossJoin, table: sourceOf(table)})
	return nd
}

// JoinUsing represents a USING(...) join condition.
type JoinUsing []string

// Using builds a JoinUsing condition for a join.
func Using(cols ...string) JoinUsing { return JoinUsing(cols) }

func (d *Dataset) joinWith(typ joinType, table Value, cond Value) *Dataset {
	nd := d.clone()
	src := sourceOf(table)
	jc := joinClause{typ: typ, table: src}
	switch c := cond.(type) {
	case JoinUsing:
		jc.using = []string(c)
	case []string:
		jc.using = c
	case joinHash:
		jc.on = c.resolve(src.name, nd.lastSourceName())
	case Expr:
		jc.on = c
	default:
		jc.on = condOf(cond)
	}
	nd.joins = append(nd.joins, jc)
	return nd
}

// joinHash is a {joinColumn => sourceColumn} join condition. Each key is
// qualified against the joined table, each value against the previous source —
// matching Sequel's `join(:t, col: :other_col)` semantics.
type joinHash struct{ pairs []HashPair }

// JoinOn builds a join condition from alternating joinColumn/sourceColumn names.
func JoinOn(kv ...string) joinHash {
	if len(kv)%2 != 0 {
		panic("sequel.JoinOn: odd number of arguments")
	}
	jh := joinHash{}
	for i := 0; i < len(kv); i += 2 {
		jh.pairs = append(jh.pairs, HashPair{Key: kv[i], Value: kv[i+1]})
	}
	return jh
}

func (jh joinHash) resolve(joinTable, srcTable string) Expr {
	ops := make([]Expr, 0, len(jh.pairs))
	for _, p := range jh.pairs {
		left := QualifiedIdentifier{Table: joinTable, Column: p.Key.(string)}
		right := QualifiedIdentifier{Table: srcTable, Column: p.Value.(string)}
		ops = append(ops, booleanOp{op: "=", left: left, right: right})
	}
	if len(ops) == 1 {
		return ops[0]
	}
	return logicalOp{op: "AND", operands: ops}
}

// ---- Compound queries ---------------------------------------------------

// Union wraps this and the other dataset in a UNION sub-select.
func (d *Dataset) Union(other *Dataset) *Dataset { return d.compound(compoundUnion, other, false) }

// Intersect wraps this and the other dataset in an INTERSECT sub-select.
func (d *Dataset) Intersect(other *Dataset) *Dataset {
	return d.compound(compoundIntersect, other, false)
}

// Except wraps this and the other dataset in an EXCEPT sub-select.
func (d *Dataset) Except(other *Dataset) *Dataset { return d.compound(compoundExcept, other, false) }

// UnionAll is UNION ALL.
func (d *Dataset) UnionAll(other *Dataset) *Dataset { return d.compound(compoundUnion, other, true) }

func (d *Dataset) compound(typ compoundType, other *Dataset, all bool) *Dataset {
	// Sequel wraps the compound in a fresh dataset over `SELECT * FROM (a OP b)
	// AS t1`, so build a wrapper whose single compound describes the operation.
	inner := d.clone()
	inner.compounds = append(inner.compounds, compound{typ: typ, ds: other, all: all})
	wrapper := &Dataset{db: d.db}
	wrapper.sources = []source{{expr: compoundSource{inner: inner}}}
	return wrapper
}

// compoundSource renders "(a OP b) AS t1" as a FROM source.
type compoundSource struct{ inner *Dataset }

func (c compoundSource) litSQL(b *builder) {
	b.WriteByte('(')
	c.inner.appendSelect(b)
	b.WriteString(") AS ")
	b.WriteString(b.dialect.quoteIdentifier("t1"))
}

// ---- SQL generation -----------------------------------------------------

// SelectSQL returns the SELECT statement for this dataset.
func (d *Dataset) SelectSQL() string {
	b := newBuilder(d.dialect())
	d.appendSelect(b)
	return b.String()
}

// SQL is an alias for SelectSQL (Sequel's Dataset#sql).
func (d *Dataset) SQL() string { return d.SelectSQL() }

func (d *Dataset) dialect() Dialect {
	if d.db != nil {
		return d.db.dialect
	}
	return Default
}

// appendSelect writes the full SELECT (including compound tails) into b.
func (d *Dataset) appendSelect(b *builder) {
	b.WriteString("SELECT ")
	if d.distinct {
		b.WriteString("DISTINCT ")
	}
	if len(d.columns) == 0 {
		b.WriteByte('*')
	} else {
		for i, c := range d.columns {
			if i > 0 {
				b.WriteString(", ")
			}
			c.litSQL(b)
		}
	}
	if len(d.sources) > 0 {
		b.WriteString(" FROM ")
		for i, s := range d.sources {
			if i > 0 {
				b.WriteString(", ")
			}
			s.litSQL(b)
		}
	}
	last := d.lastFromName()
	for _, j := range d.joins {
		j.litSQL(b, last)
	}
	d.appendWhere(b)
	if len(d.groups) > 0 {
		b.WriteString(" GROUP BY ")
		for i, g := range d.groups {
			if i > 0 {
				b.WriteString(", ")
			}
			g.litSQL(b)
		}
	}
	if len(d.havings) > 0 {
		b.WriteString(" HAVING ")
		combineAnd(d.havings).litSQL(b)
	}
	for _, c := range d.compounds {
		b.WriteByte(' ')
		b.WriteString(c.typ.keyword())
		if c.all {
			b.WriteString(" ALL")
		}
		b.WriteByte(' ')
		c.ds.appendSelect(b)
	}
	d.appendOrder(b)
	d.appendLimitOffset(b)
}

func (d *Dataset) lastFromName() string {
	if len(d.sources) > 0 {
		return d.sources[len(d.sources)-1].name
	}
	return ""
}

func (d *Dataset) appendWhere(b *builder) {
	if len(d.wheres) == 0 {
		return
	}
	b.WriteString(" WHERE ")
	combineAnd(d.wheres).litSQL(b)
}

func (d *Dataset) appendOrder(b *builder) {
	if len(d.orders) == 0 {
		return
	}
	b.WriteString(" ORDER BY ")
	for i, o := range d.orders {
		if i > 0 {
			b.WriteString(", ")
		}
		o.litSQL(b)
	}
}

func (d *Dataset) appendLimitOffset(b *builder) {
	if d.limit != nil {
		b.WriteString(" LIMIT ")
		d.limit.limit.litSQL(b)
		if d.limit.offset != nil {
			b.WriteString(" OFFSET ")
			d.limit.offset.litSQL(b)
		}
	} else if d.offset != nil {
		b.WriteString(" OFFSET ")
		d.offset.litSQL(b)
	}
}

// combineAnd wraps a list of conditions in an implicit AND. A single condition
// is returned as-is (Sequel does not add an extra paren layer for one filter).
func combineAnd(conds []Expr) Expr {
	if len(conds) == 1 {
		return conds[0]
	}
	return logicalOp{op: "AND", operands: conds}
}

// ---- INSERT / UPDATE / DELETE ------------------------------------------

// InsertSQL returns an INSERT statement from alternating column/value pairs,
// preserving their order (Sequel iterates the hash in insertion order).
func (d *Dataset) InsertSQL(kv ...Value) string {
	if len(kv)%2 != 0 {
		panic("sequel.InsertSQL: odd number of arguments")
	}
	b := newBuilder(d.dialect())
	b.WriteString("INSERT INTO ")
	d.writeInsertTarget(b)
	if len(kv) == 0 {
		b.WriteString(" DEFAULT VALUES")
		return b.String()
	}
	b.WriteString(" (")
	for i := 0; i < len(kv); i += 2 {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(b.dialect.quoteIdentifier(kv[i].(string)))
	}
	b.WriteString(") VALUES (")
	for i := 1; i < len(kv); i += 2 {
		if i > 1 {
			b.WriteString(", ")
		}
		b.litValue(kv[i])
	}
	b.WriteByte(')')
	return b.String()
}

// UpdateSQL returns an UPDATE statement from alternating column/value pairs,
// applying the dataset's WHERE clause.
func (d *Dataset) UpdateSQL(kv ...Value) string {
	if len(kv)%2 != 0 {
		panic("sequel.UpdateSQL: odd number of arguments")
	}
	b := newBuilder(d.dialect())
	b.WriteString("UPDATE ")
	d.writeInsertTarget(b)
	b.WriteString(" SET ")
	for i := 0; i < len(kv); i += 2 {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(b.dialect.quoteIdentifier(kv[i].(string)))
		b.WriteString(" = ")
		b.litValue(kv[i+1])
	}
	d.appendWhere(b)
	return b.String()
}

// DeleteSQL returns a DELETE statement, applying the dataset's WHERE clause.
func (d *Dataset) DeleteSQL() string {
	b := newBuilder(d.dialect())
	b.WriteString("DELETE FROM ")
	d.writeInsertTarget(b)
	d.appendWhere(b)
	return b.String()
}

// writeInsertTarget writes the single table an INSERT/UPDATE/DELETE targets.
func (d *Dataset) writeInsertTarget(b *builder) {
	if len(d.sources) > 0 {
		d.sources[0].litSQL(b)
	}
}
