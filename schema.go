// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"fmt"
	"strconv"
	"strings"
)

// ColumnType names an abstract Sequel column type (the DSL's String, Integer,
// …). Each dialect maps it to a concrete SQL type via [Dialect.typeName].
type ColumnType int

const (
	// TypeString is Sequel's String: varchar(255) by default, `text` when
	// Text is set (and, under Postgres, unconditionally `text`).
	TypeString ColumnType = iota
	TypeInteger
	TypeBignum
	TypeFloat
	TypeNumeric
	TypeBool
	TypeDate
	TypeDateTime
	TypeTime
	// TypeRaw carries a verbatim SQL type in Column.RawType.
	TypeRaw
)

// Column describes one column in a CREATE TABLE / ADD COLUMN.
type Column struct {
	Name    string
	Type    ColumnType
	RawType string // used when Type == TypeRaw

	Size    int  // varchar(N) / numeric first arg; 0 = unset
	Size2   int  // numeric second arg (scale)
	HasSize bool // Size/Size2 supplied

	Text    bool // String with text:true
	NotNull bool
	Unique  bool

	HasDefault bool
	Default    Value

	PrimaryKey bool // integer PRIMARY KEY AUTOINCREMENT / IDENTITY

	// Foreign-key target (empty = not a foreign key).
	References string
	OnDelete   string // e.g. "CASCADE"; empty = none
}

// index describes a CREATE INDEX in a table or alter.
type indexDef struct {
	columns []string
	unique  bool
	name    string // explicit name; empty = derived
}

// TableBuilder accumulates the columns and indexes of a CREATE TABLE. Obtain one
// from [Database.CreateTable], chain the column helpers, then read the SQL back
// via [Database.SQLs] (or let CreateTable run it through the executor).
type TableBuilder struct {
	table   string
	columns []Column
	indexes []indexDef
}

// PrimaryKey adds an auto-incrementing integer primary key column.
func (t *TableBuilder) PrimaryKey(name string) *TableBuilder {
	t.columns = append(t.columns, Column{Name: name, Type: TypeInteger, PrimaryKey: true})
	return t
}

// Column adds a fully-specified column.
func (t *TableBuilder) Column(c Column) *TableBuilder {
	t.columns = append(t.columns, c)
	return t
}

// String adds a String column. Pass a non-nil opts to set size/text/null/etc.
func (t *TableBuilder) String(name string, opts ...ColOpt) *TableBuilder {
	return t.typed(name, TypeString, opts)
}

// Integer/Bignum/Float/Numeric/Bool/Date/DateTime/Time add typed columns.
func (t *TableBuilder) Integer(name string, opts ...ColOpt) *TableBuilder {
	return t.typed(name, TypeInteger, opts)
}
func (t *TableBuilder) Bignum(name string, opts ...ColOpt) *TableBuilder {
	return t.typed(name, TypeBignum, opts)
}
func (t *TableBuilder) Float(name string, opts ...ColOpt) *TableBuilder {
	return t.typed(name, TypeFloat, opts)
}
func (t *TableBuilder) Numeric(name string, opts ...ColOpt) *TableBuilder {
	return t.typed(name, TypeNumeric, opts)
}
func (t *TableBuilder) Bool(name string, opts ...ColOpt) *TableBuilder {
	return t.typed(name, TypeBool, opts)
}
func (t *TableBuilder) Date(name string, opts ...ColOpt) *TableBuilder {
	return t.typed(name, TypeDate, opts)
}
func (t *TableBuilder) DateTime(name string, opts ...ColOpt) *TableBuilder {
	return t.typed(name, TypeDateTime, opts)
}
func (t *TableBuilder) Time(name string, opts ...ColOpt) *TableBuilder {
	return t.typed(name, TypeTime, opts)
}

// Raw adds a column with a verbatim SQL type (Sequel's column :n, 'blob').
func (t *TableBuilder) Raw(name, sqlType string, opts ...ColOpt) *TableBuilder {
	c := Column{Name: name, Type: TypeRaw, RawType: sqlType}
	applyColOpts(&c, opts)
	t.columns = append(t.columns, c)
	return t
}

func (t *TableBuilder) typed(name string, ct ColumnType, opts []ColOpt) *TableBuilder {
	c := Column{Name: name, Type: ct}
	applyColOpts(&c, opts)
	t.columns = append(t.columns, c)
	return t
}

// ForeignKey adds an integer foreign-key column referencing another table.
func (t *TableBuilder) ForeignKey(name, table string, opts ...ColOpt) *TableBuilder {
	c := Column{Name: name, Type: TypeInteger, References: table}
	applyColOpts(&c, opts)
	t.columns = append(t.columns, c)
	return t
}

// Index adds a (possibly unique/named) index over one or more columns.
func (t *TableBuilder) Index(cols []string, opts ...IdxOpt) *TableBuilder {
	id := indexDef{columns: cols}
	for _, o := range opts {
		o(&id)
	}
	t.indexes = append(t.indexes, id)
	return t
}

// ColOpt configures a Column.
type ColOpt func(*Column)

// Size sets a single size argument (varchar(N)).
func Size(n int) ColOpt { return func(c *Column) { c.Size = n; c.HasSize = true } }

// Precision sets a numeric(p, s) precision/scale.
func Precision(p, s int) ColOpt {
	return func(c *Column) { c.Size = p; c.Size2 = s; c.HasSize = true }
}

// Text marks a String column as TEXT.
func Text() ColOpt { return func(c *Column) { c.Text = true } }

// NotNull marks a column NOT NULL.
func NotNull() ColOpt { return func(c *Column) { c.NotNull = true } }

// Unique marks a column UNIQUE.
func Unique() ColOpt { return func(c *Column) { c.Unique = true } }

// DefaultVal sets a column DEFAULT. (Named DefaultVal to avoid colliding with
// the Default dialect constant.)
func DefaultVal(v Value) ColOpt { return func(c *Column) { c.HasDefault = true; c.Default = v } }

// OnDelete sets a foreign key's ON DELETE action (e.g. "CASCADE").
func OnDelete(action string) ColOpt { return func(c *Column) { c.OnDelete = action } }

func applyColOpts(c *Column, opts []ColOpt) {
	for _, o := range opts {
		o(c)
	}
}

// IdxOpt configures an index.
type IdxOpt func(*indexDef)

// UniqueIndex marks an index UNIQUE.
func UniqueIndex() IdxOpt { return func(i *indexDef) { i.unique = true } }

// IndexName sets an explicit index name.
func IndexName(name string) IdxOpt { return func(i *indexDef) { i.name = name } }

// ---- Dialect type mapping ----------------------------------------------

// typeName maps an abstract column type to concrete SQL for the dialect.
func (d Dialect) typeName(c Column) string {
	switch c.Type {
	case TypeString:
		if c.Text {
			return "text"
		}
		if d == Postgres && !c.HasSize {
			return "text"
		}
		n := 255
		if c.HasSize {
			n = c.Size
		}
		return "varchar(" + strconv.Itoa(n) + ")"
	case TypeInteger:
		return "integer"
	case TypeBignum:
		return "bigint"
	case TypeFloat:
		return "double precision"
	case TypeNumeric:
		if c.HasSize {
			return fmt.Sprintf("numeric(%d, %d)", c.Size, c.Size2)
		}
		return "numeric"
	case TypeBool:
		return "boolean"
	case TypeDate:
		return "date"
	case TypeDateTime, TypeTime:
		return "timestamp"
	default:
		return c.RawType
	}
}

// ---- CREATE TABLE -------------------------------------------------------

// CreateTableSQL renders the CREATE TABLE statement plus any CREATE INDEX
// statements the builder accumulated, in order.
func (db *Database) CreateTableSQL(t *TableBuilder) []string {
	d := db.dialect
	var b builder
	b.dialect = d
	b.WriteString("CREATE TABLE ")
	b.WriteString(d.quoteIdentifier(t.table))
	b.WriteString(" (")
	for i, c := range t.columns {
		if i > 0 {
			b.WriteString(", ")
		}
		db.writeColumnDef(&b, c)
	}
	b.WriteByte(')')
	out := []string{b.String()}
	for _, idx := range t.indexes {
		out = append(out, db.indexSQL(t.table, idx))
	}
	return out
}

// writeColumnDef renders one column definition in the order Sequel emits its
// modifiers: NAME TYPE [pk] [DEFAULT] [NOT NULL] [UNIQUE] [REFERENCES ...].
func (db *Database) writeColumnDef(b *builder, c Column) {
	d := db.dialect
	b.WriteString(d.quoteIdentifier(c.Name))
	b.WriteByte(' ')
	b.WriteString(d.typeName(c))
	if c.PrimaryKey {
		switch d {
		case Postgres:
			b.WriteString(" GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY")
		case SQLite:
			b.WriteString(" NOT NULL PRIMARY KEY AUTOINCREMENT")
		default:
			b.WriteString(" PRIMARY KEY AUTOINCREMENT")
		}
		return
	}
	if c.HasDefault {
		b.WriteString(" DEFAULT ")
		if d == SQLite {
			b.WriteByte('(')
			b.litValue(c.Default)
			b.WriteByte(')')
		} else {
			b.litValue(c.Default)
		}
	}
	if c.NotNull {
		b.WriteString(" NOT NULL")
	}
	if c.Unique {
		b.WriteString(" UNIQUE")
	}
	if c.References != "" {
		b.WriteString(" REFERENCES ")
		b.WriteString(d.quoteIdentifier(c.References))
		if c.OnDelete != "" {
			b.WriteString(" ON DELETE ")
			b.WriteString(strings.ToUpper(c.OnDelete))
		}
	}
}

// indexSQL renders a CREATE INDEX for a table's index definition.
func (db *Database) indexSQL(table string, idx indexDef) string {
	d := db.dialect
	var b builder
	b.dialect = d
	b.WriteString("CREATE ")
	if idx.unique {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX ")
	b.WriteString(d.quoteIdentifier(indexName(table, idx)))
	b.WriteString(" ON ")
	b.WriteString(d.quoteIdentifier(table))
	b.WriteString(" (")
	for i, c := range idx.columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.quoteIdentifier(c))
	}
	b.WriteByte(')')
	return b.String()
}

// indexName derives Sequel's default index name (<table>_<cols>_index) unless an
// explicit name was set.
func indexName(table string, idx indexDef) string {
	if idx.name != "" {
		return idx.name
	}
	return table + "_" + strings.Join(idx.columns, "_") + "_index"
}

// ---- DROP TABLE ---------------------------------------------------------

// DropTableSQL renders a DROP TABLE for each named table (Sequel emits one
// statement per table).
func (db *Database) DropTableSQL(tables ...string) []string {
	out := make([]string, 0, len(tables))
	for _, t := range tables {
		out = append(out, "DROP TABLE "+db.dialect.quoteIdentifier(t))
	}
	return out
}

// ---- ALTER TABLE --------------------------------------------------------

// AlterBuilder accumulates alterations to a table.
type AlterBuilder struct {
	table string
	ops   []alterOp
}

type alterOp interface {
	sql(db *Database, table string) string
}

// AddColumn adds an ADD COLUMN operation.
func (a *AlterBuilder) AddColumn(c Column) *AlterBuilder {
	a.ops = append(a.ops, addColumnOp{c})
	return a
}

// DropColumn adds a DROP COLUMN operation.
func (a *AlterBuilder) DropColumn(name string) *AlterBuilder {
	a.ops = append(a.ops, dropColumnOp{name})
	return a
}

// RenameColumn adds a RENAME COLUMN operation.
func (a *AlterBuilder) RenameColumn(from, to string) *AlterBuilder {
	a.ops = append(a.ops, renameColumnOp{from, to})
	return a
}

// SetColumnDefault adds an ALTER COLUMN ... SET DEFAULT operation.
func (a *AlterBuilder) SetColumnDefault(name string, v Value) *AlterBuilder {
	a.ops = append(a.ops, setDefaultOp{name, v})
	return a
}

// SetColumnType adds an ALTER COLUMN ... TYPE operation.
func (a *AlterBuilder) SetColumnType(name string, ct ColumnType) *AlterBuilder {
	a.ops = append(a.ops, setTypeOp{name, ct})
	return a
}

// AddIndex adds a CREATE INDEX operation (emitted as its own statement).
func (a *AlterBuilder) AddIndex(cols []string, opts ...IdxOpt) *AlterBuilder {
	id := indexDef{columns: cols}
	for _, o := range opts {
		o(&id)
	}
	a.ops = append(a.ops, addIndexOp{id})
	return a
}

// DropIndex adds a DROP INDEX operation.
func (a *AlterBuilder) DropIndex(cols []string, opts ...IdxOpt) *AlterBuilder {
	id := indexDef{columns: cols}
	for _, o := range opts {
		o(&id)
	}
	a.ops = append(a.ops, dropIndexOp{id})
	return a
}

type addColumnOp struct{ c Column }

func (o addColumnOp) sql(db *Database, table string) string {
	var b builder
	b.dialect = db.dialect
	b.WriteString("ALTER TABLE ")
	b.WriteString(db.dialect.quoteIdentifier(table))
	b.WriteString(" ADD COLUMN ")
	db.writeColumnDef(&b, o.c)
	return b.String()
}

type dropColumnOp struct{ name string }

func (o dropColumnOp) sql(db *Database, table string) string {
	return "ALTER TABLE " + db.dialect.quoteIdentifier(table) +
		" DROP COLUMN " + db.dialect.quoteIdentifier(o.name)
}

type renameColumnOp struct{ from, to string }

func (o renameColumnOp) sql(db *Database, table string) string {
	return "ALTER TABLE " + db.dialect.quoteIdentifier(table) +
		" RENAME COLUMN " + db.dialect.quoteIdentifier(o.from) +
		" TO " + db.dialect.quoteIdentifier(o.to)
}

type setDefaultOp struct {
	name string
	v    Value
}

func (o setDefaultOp) sql(db *Database, table string) string {
	var b builder
	b.dialect = db.dialect
	b.WriteString("ALTER TABLE ")
	b.WriteString(db.dialect.quoteIdentifier(table))
	b.WriteString(" ALTER COLUMN ")
	b.WriteString(db.dialect.quoteIdentifier(o.name))
	b.WriteString(" SET DEFAULT ")
	b.litValue(o.v)
	return b.String()
}

type setTypeOp struct {
	name string
	ct   ColumnType
}

func (o setTypeOp) sql(db *Database, table string) string {
	tn := db.dialect.typeName(Column{Type: o.ct})
	return "ALTER TABLE " + db.dialect.quoteIdentifier(table) +
		" ALTER COLUMN " + db.dialect.quoteIdentifier(o.name) + " TYPE " + tn
}

type addIndexOp struct{ idx indexDef }

func (o addIndexOp) sql(db *Database, table string) string { return db.indexSQL(table, o.idx) }

type dropIndexOp struct{ idx indexDef }

func (o dropIndexOp) sql(db *Database, table string) string {
	return "DROP INDEX " + db.dialect.quoteIdentifier(indexName(table, o.idx))
}

// AlterTableSQL renders one statement per accumulated alteration, in order.
func (db *Database) AlterTableSQL(a *AlterBuilder) []string {
	out := make([]string, 0, len(a.ops))
	for _, op := range a.ops {
		out = append(out, op.sql(db, a.table))
	}
	return out
}

// ---- Database entry points ----------------------------------------------

// CreateTable builds a table via fn, generates the SQL, and either runs it
// through the executor or (executor-less) appends it to the SQL log. It returns
// the generated statements.
func (db *Database) CreateTable(table string, fn func(*TableBuilder)) []string {
	tb := &TableBuilder{table: table}
	fn(tb)
	stmts := db.CreateTableSQL(tb)
	db.emit(stmts)
	return stmts
}

// AlterTable builds and emits ALTER TABLE statements via fn.
func (db *Database) AlterTable(table string, fn func(*AlterBuilder)) []string {
	ab := &AlterBuilder{table: table}
	fn(ab)
	stmts := db.AlterTableSQL(ab)
	db.emit(stmts)
	return stmts
}

// DropTable generates and emits DROP TABLE statements.
func (db *Database) DropTable(tables ...string) []string {
	stmts := db.DropTableSQL(tables...)
	db.emit(stmts)
	return stmts
}

// emit runs each statement through the executor, or logs it when executor-less.
func (db *Database) emit(stmts []string) {
	for _, s := range stmts {
		if db.executor != nil {
			// Errors are the host's concern; schema DDL returns no rows.
			_, _ = db.executor.Execute(s)
		} else {
			db.schemaLog = append(db.schemaLog, s)
		}
	}
}

// ---- Migration ----------------------------------------------------------

// Migration is a reversible schema change — the Go equivalent of
// Sequel.migration { up {...}; down {...} }. Up and Down each receive the target
// [Database] and issue CreateTable/AlterTable/DropTable against it.
type Migration struct {
	Up   func(*Database)
	Down func(*Database)
}

// Apply runs the migration in the given direction ("up" or "down") against db.
// An unknown direction runs nothing. A nil handler for the chosen direction is a
// no-op (an irreversible migration with no Down).
func (m Migration) Apply(db *Database, direction string) {
	switch direction {
	case "up":
		if m.Up != nil {
			m.Up(db)
		}
	case "down":
		if m.Down != nil {
			m.Down(db)
		}
	}
}
