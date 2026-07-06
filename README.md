<p align="center"><img src="https://raw.githubusercontent.com/go-ruby-sequel/brand/main/social/go-ruby-sequel-sequel.png" alt="go-ruby-sequel/sequel" width="720"></p>

# sequel — go-ruby-sequel

[![Docs](https://img.shields.io/badge/docs-mkdocs--material-DC2626)](https://go-ruby-sequel.github.io/docs/)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

**A pure-Go (no cgo) reimplementation of the deterministic SQL-generation core of
Ruby's [Sequel](https://sequel.jeremyevans.net/) toolkit** (the `sequel` gem) —
the chainable `Dataset` query builder, the expression DSL, the schema DSL, and
the per-dialect literalization/identifier-quoting that turn a chain of
`DB[:table].where(…).order(…)` calls into a SQL string. It emits the exact bytes
the gem emits, across the **default**, **sqlite** and **postgres** dialects —
**without any Ruby runtime and without a database**.

It is the SQL-toolkit backend for
[go-embedded-ruby](https://github.com/go-embedded-ruby/ruby), but is a
**standalone, reusable** module — a sibling of
[go-ruby-regexp](https://github.com/go-ruby-regexp/regexp) (the Onigmo engine)
and [go-ruby-erb](https://github.com/go-ruby-erb/erb) (the ERB compiler).

> **What it is — and isn't.** Turning a dataset/schema DSL chain into a SQL
> string is fully deterministic and needs **no interpreter and no database**, so
> it lives here as pure Go. Actually *running* that SQL against a server is the
> host's job: a `Database` carries an injectable `Executor` seam
> (`Execute(sql) → rows`) the host wires to a driver such as
> [go-ruby-sqlite3](https://github.com/go-ruby-sqlite3/sqlite3) or
> [go-ruby-pg](https://github.com/go-ruby-pg/pg). The SQL *text* is what this
> library generates and tests; execution is the seam. (A `Model` layer is
> **deferred** — see below.)

## Features

Faithful port of Sequel's SQL generation, validated byte-for-byte against the
`sequel` gem's `mock` adapter on every supported platform:

- **Dataset query builder** — immutable, chainable datasets: `Select`, `Where`
  (hash / comparison / list / sub-select / raw literal), `Exclude`, `Order` /
  `Reverse`, `Limit` / `LimitOffset` / `Offset`, `Distinct`, `Group` / `Having`,
  `From` (multi-source, aliased, sub-select), and `InsertSQL` / `UpdateSQL` /
  `DeleteSQL` / `SelectSQL`.
- **Joins** — `Join` / `InnerJoin` / `LeftJoin` / `RightJoin` / `FullJoin` /
  `CrossJoin`, with hash `{joinCol ⇒ srcCol}` conditions (auto-qualified),
  `USING (…)` lists, and arbitrary `Expr` `ON` conditions.
- **Compound queries** — `Union` / `UnionAll` / `Intersect` / `Except`, wrapped
  as `SELECT * FROM (a OP b) AS t1` exactly like the gem.
- **Expression DSL** — identifiers (`Ident`, `Qualify`), literals (`Lit`),
  functions (`Function`), comparisons (`Eq`/`Neq`/`Gt`/`Lt`/`Gte`/`Lte`),
  `Like`, `In` / `InDataset`, `And` / `Or` / `Not`, arithmetic (`Arith`), and
  aliasing (`As`).
- **Per-dialect literalization** — identifier quoting (unquoted / `` ` `` /
  `"…"`, with quote-doubling), string escaping (single-quote doubling), booleans
  (`IS TRUE`/`IS FALSE` vs `'t'`/`'f'`), blobs (hex string / `X'..'` / `\ooo`
  octal), floats (Ruby `Float#to_s`), dates, and timestamps.
- **Schema DSL** — `CreateTable` (typed columns, `primary_key`, `foreign_key`,
  defaults, not-null/unique, indexes), `AlterTable` (add/drop/rename column, set
  default/type, add/drop index), `DropTable`, and a reversible `Migration`
  up/down pair — with per-dialect type mapping (Postgres `IDENTITY`, SQLite
  `NOT NULL PRIMARY KEY` + paren-wrapped defaults, `String → text`, …).
- **Adapter seam** — `Executor` / `ExecutorFunc`, wired by the host; SQL runs
  through it or, executor-less, is collected for inspection (mirrors the gem's
  mock `#sqls`).

CGO-free, dependency-free, **100% test coverage**, `gofmt` + `go vet` clean, and
green across the six 64-bit Go targets (amd64, arm64, riscv64, loong64, ppc64le,
s390x).

## Install

```sh
go get github.com/go-ruby-sequel/sequel
```

## Usage

```go
package main

import (
	"fmt"

	"github.com/go-ruby-sequel/sequel"
)

func main() {
	db := sequel.Mock("postgres") // or Connect("postgres", executor)

	ds := db.T("items").
		Where(sequel.H("category", "books")).
		Where(sequel.Gt("price", 10)).
		Order(sequel.Desc("price")).
		Limit(5)

	fmt.Println(ds.SelectSQL())
	// SELECT * FROM "items" WHERE (("category" = 'books') AND ("price" > 10))
	//   ORDER BY "price" DESC LIMIT 5

	fmt.Println(db.T("items").InsertSQL("name", "Go", "price", 42))
	// INSERT INTO "items" ("name", "price") VALUES ('Go', 42)

	db.CreateTable("users", func(t *sequel.TableBuilder) {
		t.PrimaryKey("id")
		t.String("name", sequel.NotNull())
		t.Integer("age")
		t.Index([]string{"name"}, sequel.UniqueIndex())
	})
	for _, s := range db.SQLs() {
		fmt.Println(s)
	}
	// CREATE TABLE "users" ("id" integer GENERATED BY DEFAULT AS IDENTITY
	//   PRIMARY KEY, "name" text NOT NULL, "age" integer)
	// CREATE UNIQUE INDEX "users_name_index" ON "users" ("name")
}
```

## Dialects

`Mock` / `Connect` take a dialect name; unknown names degrade to the base SQL:

| name(s)                    | quoting  | booleans           | blobs        |
| -------------------------- | -------- | ------------------ | ------------ |
| `default`                  | none     | `IS TRUE`/`IS FALSE` | `'bytes'`  |
| `sqlite`                   | `` `x` `` | `'t'` / `'f'`     | `X'..'`      |
| `postgres` / `postgresql` / `pg` | `"x"` | `IS TRUE`/`IS FALSE` | `'\ooo'` |

## Adapter seam & Model

Execution goes through the `Executor` the host wires in:

```go
type Executor interface {
	Execute(sql string) ([]map[string]any, error)
}
db := sequel.Connect("sqlite", myDriver) // rows, _ := db.All(db.T("items"))
```

A full `Model` (active-record) layer is **deferred**: it is thin glue over this
dataset/schema core plus the executor seam, and belongs with the host that owns
object instantiation. This module delivers the deterministic SQL generation it
would sit on.

## Tests & coverage

The suite pairs deterministic, ruby-free golden vectors — which alone hold
coverage at **100%**, so the qemu cross-arch and Windows lanes pass the gate —
with a **differential oracle** against the real `sequel` gem: each dataset and
schema statement is built here *and* in Ruby (via the gem's `mock` adapter, so no
database is touched) and the emitted SQL is compared **byte-for-byte** across the
default, sqlite and postgres dialects. The oracle skips itself where `ruby` or
the gem is absent.

```sh
COVERPKG=$(go list ./... | paste -sd, -)
go test -race -coverpkg="$COVERPKG" -coverprofile=cover.out ./...
go tool cover -func=cover.out | tail -1   # 100.0%
```

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-ruby-sequel/sequel authors.

## WebAssembly

Being pure Go (CGO=0), this library also compiles to **WebAssembly** — both
`GOOS=js GOARCH=wasm` (browser / Node.js) and `GOOS=wasip1 GOARCH=wasm` (WASI).
CI builds both targets on every push, alongside the six 64-bit native/qemu arches.

```sh
GOOS=js     GOARCH=wasm go build ./...   # browser / Node
GOOS=wasip1 GOARCH=wasm go build ./...   # WASI (wasmtime, wasmer, wasmedge, …)
```
