// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

// Executor is the host seam through which generated SQL actually runs. A
// [Database] holds one; the host (go-embedded-ruby) wires it to a driver such
// as go-ruby-sqlite3 or go-ruby-pg. This library never runs SQL itself — it
// only generates the text and hands it to Execute.
//
// Execute runs a statement and returns its result rows. Each row is a map from
// column name to value in the small [Value] model. A statement with no result
// set (INSERT/UPDATE/DDL) returns a nil or empty slice and a nil error.
type Executor interface {
	Execute(sql string) ([]map[string]Value, error)
}

// ExecutorFunc adapts a plain function to the Executor interface.
type ExecutorFunc func(sql string) ([]map[string]Value, error)

// Execute calls the underlying function.
func (f ExecutorFunc) Execute(sql string) ([]map[string]Value, error) { return f(sql) }

// Database is the entry point — the Go equivalent of a Sequel::Database. It
// carries the SQL dialect (which drives identifier quoting and value
// literalization) and the host [Executor] seam. Construct one with [Connect] or
// [Mock], then index it with [Database.T] (the Go form of DB[:table]) to obtain
// datasets.
type Database struct {
	dialect  Dialect
	executor Executor
	// schemaLog collects the DDL emitted by CreateTable/AlterTable/DropTable
	// when no executor is wired, so tests and callers can inspect the SQL. It
	// mirrors Sequel's mock adapter #sqls.
	schemaLog []string
}

// Connect builds a Database for a named dialect ("default", "sqlite",
// "postgres") wired to the given executor. A nil executor is allowed: the
// database can still generate SQL, but [Dataset] execution and DDL execution
// will collect SQL into the log instead of running it.
func Connect(dialectName string, exec Executor) *Database {
	return &Database{dialect: DialectByName(dialectName), executor: exec}
}

// Mock builds an executor-less Database for a dialect, mirroring
// `Sequel.mock(host: ...)`. It generates SQL and logs DDL but runs nothing.
func Mock(dialectName string) *Database {
	return &Database{dialect: DialectByName(dialectName)}
}

// Dialect returns the database's SQL dialect.
func (db *Database) Dialect() Dialect { return db.dialect }

// T returns a dataset over the named table — the Go form of DB[:table].
func (db *Database) T(table string) *Dataset {
	return &Dataset{db: db, sources: []source{{name: table}}}
}

// From returns a dataset over one or more FROM sources — DB.from(:a, :b).
func (db *Database) From(tables ...Value) *Dataset {
	ds := &Dataset{db: db}
	for _, t := range tables {
		ds.sources = append(ds.sources, sourceOf(t))
	}
	return ds
}

// Dataset returns an empty dataset bound to this database (no FROM), a base for
// compound queries or fully-literal SQL.
func (db *Database) Dataset() *Dataset { return &Dataset{db: db} }

// Run executes a raw SQL statement through the wired executor.
func (db *Database) Run(sql string) ([]map[string]Value, error) {
	if db.executor == nil {
		db.schemaLog = append(db.schemaLog, sql)
		return nil, nil
	}
	return db.executor.Execute(sql)
}

// SQLs returns and clears the collected DDL/statement log (mirrors the mock
// adapter's #sqls followed by a clear), so successive assertions see only the
// statements from the most recent operation.
func (db *Database) SQLs() []string {
	out := db.schemaLog
	db.schemaLog = nil
	return out
}

// All runs a dataset's SELECT through the executor and returns its rows.
func (db *Database) All(d *Dataset) ([]map[string]Value, error) {
	return db.Run(d.SelectSQL())
}
