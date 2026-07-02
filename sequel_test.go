// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"math/big"
	"strings"
	"testing"
	"time"
)

// goldenCase is one deterministic SQL-generation vector: build a value with
// `fn`, and assert its emitted string equals `want`. These vectors alone drive
// the suite to 100% coverage and hold with no Ruby present; the oracle in
// oracle_test.go re-derives the same `want` strings from the `sequel` gem.
type goldenCase struct {
	name string
	got  func() string
	want string
}

// selDefault/selPg/selSqlite are datasets over :items in each dialect.
func selDefault() *Dataset { return Mock("default").T("items") }
func selPg() *Dataset      { return Mock("postgres").T("items") }
func selSqlite() *Dataset  { return Mock("sqlite").T("items") }

func TestDatasetGolden(t *testing.T) {
	ds := selDefault()
	pg := selPg()
	sq := selSqlite()
	bi, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	cases := []goldenCase{
		// --- default dialect SELECT ---
		{"select-star", func() string { return ds.SelectSQL() }, "SELECT * FROM items"},
		{"sql-alias", func() string { return ds.SQL() }, "SELECT * FROM items"},
		{"where-hash", func() string { return ds.Where(H("id", 1, "name", "x")).SelectSQL() },
			"SELECT * FROM items WHERE ((id = 1) AND (name = 'x'))"},
		{"where-nil", func() string { return ds.Where(H("id", nil)).SelectSQL() },
			"SELECT * FROM items WHERE (id IS NULL)"},
		{"where-single-hash", func() string { return ds.Where(H("id", 1)).SelectSQL() },
			"SELECT * FROM items WHERE (id = 1)"},
		{"where-gt", func() string { return ds.Where(Gt("price", 100)).SelectSQL() },
			"SELECT * FROM items WHERE (price > 100)"},
		{"where-lt", func() string { return ds.Where(Lt("price", 100)).SelectSQL() },
			"SELECT * FROM items WHERE (price < 100)"},
		{"where-gte", func() string { return ds.Where(Gte("price", 100)).SelectSQL() },
			"SELECT * FROM items WHERE (price >= 100)"},
		{"where-lte", func() string { return ds.Where(Lte("price", 100)).SelectSQL() },
			"SELECT * FROM items WHERE (price <= 100)"},
		{"where-eq", func() string { return ds.Where(Eq("a", "b")).SelectSQL() },
			"SELECT * FROM items WHERE (a = 'b')"},
		{"where-neq", func() string { return ds.Where(Neq("a", 1)).SelectSQL() },
			"SELECT * FROM items WHERE (a != 1)"},
		{"where-in-list", func() string { return ds.Where(H("id", []Value{1, 2, 3})).SelectSQL() },
			"SELECT * FROM items WHERE (id IN (1, 2, 3))"},
		{"where-in-explicit", func() string { return ds.Where(In("id", 1, 2)).SelectSQL() },
			"SELECT * FROM items WHERE (id IN (1, 2))"},
		{"where-in-subq", func() string { return ds.Where(InDataset("id", ds.Select("id"))).SelectSQL() },
			"SELECT * FROM items WHERE (id IN (SELECT id FROM items))"},
		{"where-in-hash-subq", func() string { return ds.Where(H("id", selDefault().Select("id"))).SelectSQL() },
			"SELECT * FROM items WHERE (id IN (SELECT id FROM items))"},
		{"where-and", func() string { return ds.Where(And(Gt("a", 1), Lt("b", 2))).SelectSQL() },
			"SELECT * FROM items WHERE ((a > 1) AND (b < 2))"},
		{"where-or", func() string { return ds.Where(Or(H("id", 1), H("id", 2))).SelectSQL() },
			"SELECT * FROM items WHERE ((id = 1) OR (id = 2))"},
		{"where-chained", func() string { return ds.Where(Gt("a", 1)).Where(Lt("b", 2)).SelectSQL() },
			"SELECT * FROM items WHERE ((a > 1) AND (b < 2))"},
		{"exclude", func() string { return ds.Exclude(H("id", 1)).SelectSQL() },
			"SELECT * FROM items WHERE (id != 1)"},
		{"exclude-gt", func() string { return ds.Exclude(Gt("a", 1)).SelectSQL() },
			"SELECT * FROM items WHERE (a <= 1)"},
		{"not-fn", func() string { return ds.Where(Not(H("id", 1))).SelectSQL() },
			"SELECT * FROM items WHERE (id != 1)"},
		{"not-like", func() string { return ds.Where(Not(Like("a", "x%"))).SelectSQL() },
			`SELECT * FROM items WHERE (a NOT LIKE 'x%' ESCAPE '\')`},
		{"not-in", func() string { return ds.Where(Not(In("id", 1, 2))).SelectSQL() },
			"SELECT * FROM items WHERE (id NOT IN (1, 2))"},
		{"not-and", func() string { return ds.Where(Not(And(H("a", 1), H("b", 2)))).SelectSQL() },
			"SELECT * FROM items WHERE ((a != 1) OR (b != 2))"},
		{"not-lit", func() string { return ds.Where(Not(Lit("a = 1"))).SelectSQL() },
			"SELECT * FROM items WHERE NOT (a = 1)"},
		{"like", func() string { return ds.Where(Like("name", "A%")).SelectSQL() },
			`SELECT * FROM items WHERE (name LIKE 'A%' ESCAPE '\')`},
		{"lit-filter", func() string { return ds.Where(Lit("a = 1")).SelectSQL() },
			"SELECT * FROM items WHERE (a = 1)"},
		{"lit-filter-two", func() string { return ds.Where(Lit("a = 1")).Where(Lit("b = 2")).SelectSQL() },
			"SELECT * FROM items WHERE ((a = 1) AND (b = 2))"},
		{"order", func() string { return ds.Order("name").SelectSQL() }, "SELECT * FROM items ORDER BY name"},
		{"order-desc", func() string { return ds.Order(Desc("name")).SelectSQL() },
			"SELECT * FROM items ORDER BY name DESC"},
		{"order-asc-explicit", func() string { return ds.Order(Asc("name")).SelectSQL() },
			"SELECT * FROM items ORDER BY name"},
		{"order-multi", func() string { return ds.Order("a", Desc("b")).SelectSQL() },
			"SELECT * FROM items ORDER BY a, b DESC"},
		{"reverse", func() string { return ds.Order("a").Reverse().SelectSQL() },
			"SELECT * FROM items ORDER BY a DESC"},
		{"reverse-desc", func() string { return ds.Order(Desc("a")).Reverse().SelectSQL() },
			"SELECT * FROM items ORDER BY a"},
		{"limit", func() string { return ds.Limit(10).SelectSQL() }, "SELECT * FROM items LIMIT 10"},
		{"limit-offset", func() string { return ds.LimitOffset(10, 5).SelectSQL() },
			"SELECT * FROM items LIMIT 10 OFFSET 5"},
		{"offset-only", func() string { return ds.Offset(5).SelectSQL() }, "SELECT * FROM items OFFSET 5"},
		{"select-cols", func() string { return ds.Select("a", "b").SelectSQL() }, "SELECT a, b FROM items"},
		{"select-alias", func() string { return ds.Select(As("a", "b")).SelectSQL() }, "SELECT a AS b FROM items"},
		{"select-fn-alias", func() string { return ds.Select(Function("count", Lit("*")).As("n")).SelectSQL() },
			"SELECT count(*) AS n FROM items"},
		{"select-arith-alias", func() string { return ds.Select(Arith("*", "price", "qty").As("total")).SelectSQL() },
			"SELECT (price * qty) AS total FROM items"},
		{"distinct", func() string { return ds.Distinct().SelectSQL() }, "SELECT DISTINCT * FROM items"},
		{"group", func() string { return ds.Group("cat").SelectSQL() }, "SELECT * FROM items GROUP BY cat"},
		{"group-multi", func() string { return ds.Group("a", "b").SelectSQL() },
			"SELECT * FROM items GROUP BY a, b"},
		{"having", func() string {
			return ds.Group("cat").Having(Gt(Function("count", Lit("*")), 1)).SelectSQL()
		}, "SELECT * FROM items GROUP BY cat HAVING (count(*) > 1)"},
		{"having-two", func() string {
			return ds.Group("cat").Having(Gt("a", 1)).Having(Lt("b", 2)).SelectSQL()
		}, "SELECT * FROM items GROUP BY cat HAVING ((a > 1) AND (b < 2))"},
		// --- joins ---
		{"join", func() string { return ds.Join("orders", JoinOn("item_id", "id")).SelectSQL() },
			"SELECT * FROM items INNER JOIN orders ON (orders.item_id = items.id)"},
		{"inner-join", func() string { return ds.InnerJoin("orders", JoinOn("item_id", "id")).SelectSQL() },
			"SELECT * FROM items INNER JOIN orders ON (orders.item_id = items.id)"},
		{"left-join", func() string { return ds.LeftJoin("orders", JoinOn("item_id", "id")).SelectSQL() },
			"SELECT * FROM items LEFT JOIN orders ON (orders.item_id = items.id)"},
		{"right-join", func() string { return ds.RightJoin("orders", JoinOn("item_id", "id")).SelectSQL() },
			"SELECT * FROM items RIGHT JOIN orders ON (orders.item_id = items.id)"},
		{"full-join", func() string { return ds.FullJoin("orders", JoinOn("item_id", "id")).SelectSQL() },
			"SELECT * FROM items FULL JOIN orders ON (orders.item_id = items.id)"},
		{"cross-join", func() string { return ds.CrossJoin("orders").SelectSQL() },
			"SELECT * FROM items CROSS JOIN orders"},
		{"join-using", func() string { return ds.Join("o", Using("id")).SelectSQL() },
			"SELECT * FROM items INNER JOIN o USING (id)"},
		{"join-using-slice", func() string { return ds.Join("o", []string{"id", "k"}).SelectSQL() },
			"SELECT * FROM items INNER JOIN o USING (id, k)"},
		{"join-expr-cond", func() string { return ds.Join("o", Eq(Qualify("o", "x"), Qualify("items", "y"))).SelectSQL() },
			"SELECT * FROM items INNER JOIN o ON (o.x = items.y)"},
		{"join-multi-hash", func() string { return ds.Join("o", JoinOn("a", "x", "b", "y")).SelectSQL() },
			"SELECT * FROM items INNER JOIN o ON ((o.a = items.x) AND (o.b = items.y))"},
		{"join-two", func() string {
			return ds.Join("o", JoinOn("i", "id")).Join("p", JoinOn("j", "id")).SelectSQL()
		}, "SELECT * FROM items INNER JOIN o ON (o.i = items.id) INNER JOIN p ON (p.j = o.id)"},
		// --- compound ---
		{"union", func() string { return ds.Union(selDefault().From("other")).SQL() },
			"SELECT * FROM (SELECT * FROM items UNION SELECT * FROM other) AS t1"},
		{"union-all", func() string { return ds.UnionAll(selDefault().From("other")).SQL() },
			"SELECT * FROM (SELECT * FROM items UNION ALL SELECT * FROM other) AS t1"},
		{"intersect", func() string { return ds.Intersect(selDefault().From("o")).SQL() },
			"SELECT * FROM (SELECT * FROM items INTERSECT SELECT * FROM o) AS t1"},
		{"except", func() string { return ds.Except(selDefault().From("o")).SQL() },
			"SELECT * FROM (SELECT * FROM items EXCEPT SELECT * FROM o) AS t1"},
		// --- from ---
		{"from-multi", func() string { return Mock("default").From("a", "b").SelectSQL() },
			"SELECT * FROM a, b"},
		{"from-alias", func() string { return Mock("default").From(As("items", "i")).SelectSQL() },
			"SELECT * FROM items AS i"},
		{"from-ident", func() string { return Mock("default").From(Ident("t")).SelectSQL() },
			"SELECT * FROM t"},
		{"from-subq", func() string { return Mock("default").From(selDefault()).SelectSQL() },
			"SELECT * FROM (SELECT * FROM items)"},
		{"from-expr", func() string { return Mock("default").From(Function("f")).SelectSQL() },
			"SELECT * FROM f()"},
		{"from-replace", func() string { return ds.From("other").SelectSQL() }, "SELECT * FROM other"},
		{"dataset-nofrom", func() string { return Mock("default").Dataset().Select(Lit("1")).SelectSQL() },
			"SELECT 1"},
		// --- qualified / expr ---
		{"qualified", func() string { return ds.Where(H(Qualify("t", "c"), 1)).SelectSQL() },
			"SELECT * FROM items WHERE (t.c = 1)"},
		{"ident-col", func() string { return ds.Where(H(Ident("t").Col("c"), 1)).SelectSQL() },
			"SELECT * FROM items WHERE (t.c = 1)"},
		{"ident-as", func() string { return ds.Select(Ident("c").As("d")).SelectSQL() },
			"SELECT c AS d FROM items"},
		{"qualified-as", func() string { return ds.Select(Qualify("t", "c").As("d")).SelectSQL() },
			"SELECT t.c AS d FROM items"},
		{"fn-multi-arg", func() string { return ds.Select(Function("coalesce", "a", 0)).SelectSQL() },
			"SELECT coalesce(a, 0) FROM items"},
		{"neq-nil", func() string { return ds.Where(Neq("id", nil)).SelectSQL() },
			"SELECT * FROM items WHERE (id IS NOT NULL)"},
		{"bool-true", func() string { return ds.Where(H("active", true)).SelectSQL() },
			"SELECT * FROM items WHERE (active IS TRUE)"},
		{"bool-false", func() string { return ds.Where(H("active", false)).SelectSQL() },
			"SELECT * FROM items WHERE (active IS FALSE)"},
		{"bool-neq", func() string { return ds.Where(Neq("active", true)).SelectSQL() },
			"SELECT * FROM items WHERE (active IS NOT TRUE)"},
		// --- values / literals ---
		{"val-int64", func() string { return ds.Where(H("n", int64(9))).SelectSQL() },
			"SELECT * FROM items WHERE (n = 9)"},
		{"val-bignum", func() string { return ds.Where(H("n", bi)).SelectSQL() },
			"SELECT * FROM items WHERE (n = 123456789012345678901234567890)"},
		{"val-float", func() string { return ds.Where(H("n", 1.5)).SelectSQL() },
			"SELECT * FROM items WHERE (n = 1.5)"},
		{"val-float-int", func() string { return ds.Where(H("n", 1.0)).SelectSQL() },
			"SELECT * FROM items WHERE (n = 1.0)"},
		{"val-float-exp", func() string { return ds.Where(H("n", 1e20)).SelectSQL() },
			"SELECT * FROM items WHERE (n = 1.0e+20)"},
		{"val-float-negexp", func() string { return ds.Where(H("n", 1e-7)).SelectSQL() },
			"SELECT * FROM items WHERE (n = 1.0e-07)"},
		{"val-str-quote", func() string { return ds.Where(H("s", "O'Brien")).SelectSQL() },
			"SELECT * FROM items WHERE (s = 'O''Brien')"},
		{"val-str-backslash", func() string { return ds.Where(H("s", `a\b`)).SelectSQL() },
			`SELECT * FROM items WHERE (s = 'a\b')`},
		{"val-date", func() string { return ds.Where(H("d", NewDate(2026, 7, 2))).SelectSQL() },
			"SELECT * FROM items WHERE (d = '2026-07-02')"},
		{"val-time", func() string {
			return ds.Where(H("t", time.Date(2026, 7, 2, 10, 30, 0, 0, time.UTC))).SelectSQL()
		}, "SELECT * FROM items WHERE (t = '2026-07-02 10:30:00.000000')"},
		{"val-bare-bool", func() string { return ds.Select(Function("f", true)).SelectSQL() },
			"SELECT f(TRUE) FROM items"},
		// --- INSERT/UPDATE/DELETE ---
		{"insert", func() string { return ds.InsertSQL("name", "x", "age", 3) },
			"INSERT INTO items (name, age) VALUES ('x', 3)"},
		{"insert-default", func() string { return ds.InsertSQL() }, "INSERT INTO items DEFAULT VALUES"},
		{"update", func() string { return ds.UpdateSQL("name", "y") }, "UPDATE items SET name = 'y'"},
		{"update-where", func() string { return ds.Where(H("id", 1)).UpdateSQL("a", 1, "b", 2) },
			"UPDATE items SET a = 1, b = 2 WHERE (id = 1)"},
		{"delete", func() string { return ds.Where(H("id", 1)).DeleteSQL() },
			"DELETE FROM items WHERE (id = 1)"},
		{"delete-all", func() string { return ds.DeleteSQL() }, "DELETE FROM items"},
		{"blob-default", func() string { return ds.InsertSQL("d", Blob([]byte{'a', '\'', 'b'})) },
			"INSERT INTO items (d) VALUES ('a''b')"},
		{"bytes-default", func() string { return ds.InsertSQL("d", []byte("ab")) },
			"INSERT INTO items (d) VALUES ('ab')"},
		// --- postgres dialect ---
		{"pg-select", func() string { return pg.SelectSQL() }, `SELECT * FROM "items"`},
		{"pg-cols-where", func() string { return pg.Select("a", "b").Where(H("id", 1)).SelectSQL() },
			`SELECT "a", "b" FROM "items" WHERE ("id" = 1)`},
		{"pg-qualified", func() string { return pg.Where(H(Qualify("t", "c"), 1)).SelectSQL() },
			`SELECT * FROM "items" WHERE ("t"."c" = 1)`},
		{"pg-insert", func() string { return pg.InsertSQL("name", "x") },
			`INSERT INTO "items" ("name") VALUES ('x')`},
		{"pg-str-esc", func() string { return pg.Where(H("name", "O'Brien")).SelectSQL() },
			`SELECT * FROM "items" WHERE ("name" = 'O''Brien')`},
		{"pg-bool", func() string { return pg.Where(H("active", true)).SelectSQL() },
			`SELECT * FROM "items" WHERE ("active" IS TRUE)`},
		{"pg-bare-bool", func() string { return pg.Select(Function("f", true)).SelectSQL() },
			`SELECT f(true) FROM "items"`},
		{"pg-blob", func() string { return pg.InsertSQL("x", Blob([]byte{0x00, 0xFF, '\''})) },
			`INSERT INTO "items" ("x") VALUES ('\000\377\047')`},
		{"pg-quote-doubling", func() string { return Mock("postgres").T(`a"b`).SelectSQL() },
			`SELECT * FROM "a""b"`},
		// --- sqlite dialect ---
		{"sq-select", func() string { return sq.SelectSQL() }, "SELECT * FROM `items`"},
		{"sq-bool-false", func() string { return sq.Where(H("active", false)).SelectSQL() },
			"SELECT * FROM `items` WHERE (`active` = 'f')"},
		{"sq-bool-true", func() string { return sq.Where(H("active", true)).SelectSQL() },
			"SELECT * FROM `items` WHERE (`active` = 't')"},
		{"sq-blob", func() string { return sq.InsertSQL("x", Blob([]byte{0x00, 0xFF})) },
			"INSERT INTO `items` (`x`) VALUES (X'00ff')"},
		{"sq-quote-doubling", func() string { return Mock("sqlite").T("a`b").SelectSQL() },
			"SELECT * FROM `a``b`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.got(); got != c.want {
				t.Errorf("\n got=%q\nwant=%q", got, c.want)
			}
		})
	}
}

func TestSchemaGolden(t *testing.T) {
	cases := []goldenCase{
		{"create-types-default", func() string {
			db := Mock("default")
			db.CreateTable("t", func(t *TableBuilder) {
				t.PrimaryKey("id")
				t.String("name")
				t.String("code", Size(10))
				t.String("bio", Text())
				t.Integer("age")
				t.Bignum("big")
				t.Float("score")
				t.Numeric("price", Precision(10, 2))
				t.Numeric("plain")
				t.Bool("active")
				t.Date("d")
				t.DateTime("dt")
				t.Time("tm")
				t.Raw("raw", "blob")
			})
			return strings.Join(db.SQLs(), " | ")
		}, "CREATE TABLE t (id integer PRIMARY KEY AUTOINCREMENT, name varchar(255), code varchar(10), bio text, age integer, big bigint, score double precision, price numeric(10, 2), plain numeric, active boolean, d date, dt timestamp, tm timestamp, raw blob)"},
		{"create-constraints", func() string {
			db := Mock("default")
			db.CreateTable("u", func(t *TableBuilder) {
				t.PrimaryKey("id")
				t.String("name", DefaultVal("x"), NotNull(), Unique())
				t.Integer("b", DefaultVal(5))
				t.ForeignKey("org_id", "orgs", OnDelete("cascade"))
				t.ForeignKey("plain_fk", "z")
				t.Column(Column{Name: "manual", Type: TypeInteger})
				t.Index([]string{"name"})
				t.Index([]string{"a", "b"}, UniqueIndex())
				t.Index([]string{"c"}, IndexName("myidx"))
			})
			return strings.Join(db.SQLs(), " | ")
		}, "CREATE TABLE u (id integer PRIMARY KEY AUTOINCREMENT, name varchar(255) DEFAULT 'x' NOT NULL UNIQUE, b integer DEFAULT 5, org_id integer REFERENCES orgs ON DELETE CASCADE, plain_fk integer REFERENCES z, manual integer) | CREATE INDEX u_name_index ON u (name) | CREATE UNIQUE INDEX u_a_b_index ON u (a, b) | CREATE INDEX myidx ON u (c)"},
		{"create-pg", func() string {
			db := Mock("postgres")
			db.CreateTable("t", func(t *TableBuilder) {
				t.PrimaryKey("id")
				t.String("name")
				t.String("code", Size(10))
			})
			return strings.Join(db.SQLs(), " | ")
		}, `CREATE TABLE "t" ("id" integer GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, "name" text, "code" varchar(10))`},
		{"create-sqlite", func() string {
			db := Mock("sqlite")
			db.CreateTable("t", func(t *TableBuilder) {
				t.PrimaryKey("id")
				t.String("name", DefaultVal("x"))
			})
			return strings.Join(db.SQLs(), " | ")
		}, "CREATE TABLE `t` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `name` varchar(255) DEFAULT ('x'))"},
		{"alter", func() string {
			db := Mock("default")
			db.AlterTable("t", func(a *AlterBuilder) {
				a.AddColumn(Column{Name: "c", Type: TypeString, NotNull: true})
				a.DropColumn("d")
				a.RenameColumn("e", "f")
				a.SetColumnDefault("g", 9)
				a.SetColumnType("h", TypeInteger)
				a.AddIndex([]string{"i"}, UniqueIndex())
				a.DropIndex([]string{"j"})
			})
			return strings.Join(db.SQLs(), " | ")
		}, "ALTER TABLE t ADD COLUMN c varchar(255) NOT NULL | ALTER TABLE t DROP COLUMN d | ALTER TABLE t RENAME COLUMN e TO f | ALTER TABLE t ALTER COLUMN g SET DEFAULT 9 | ALTER TABLE t ALTER COLUMN h TYPE integer | CREATE UNIQUE INDEX t_i_index ON t (i) | DROP INDEX t_j_index"},
		{"drop", func() string {
			db := Mock("default")
			db.DropTable("a", "b")
			return strings.Join(db.SQLs(), " | ")
		}, "DROP TABLE a | DROP TABLE b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.got(); got != c.want {
				t.Errorf("\n got=%q\nwant=%q", got, c.want)
			}
		})
	}
}

func TestMigration(t *testing.T) {
	m := Migration{
		Up:   func(d *Database) { d.CreateTable("t", func(tb *TableBuilder) { tb.PrimaryKey("id") }) },
		Down: func(d *Database) { d.DropTable("t") },
	}
	db := Mock("default")
	m.Apply(db, "up")
	if got := strings.Join(db.SQLs(), ""); got != "CREATE TABLE t (id integer PRIMARY KEY AUTOINCREMENT)" {
		t.Fatalf("up: %q", got)
	}
	m.Apply(db, "down")
	if got := strings.Join(db.SQLs(), ""); got != "DROP TABLE t" {
		t.Fatalf("down: %q", got)
	}
	// unknown direction and nil handlers are no-ops.
	m.Apply(db, "sideways")
	empty := Migration{}
	empty.Apply(db, "up")
	empty.Apply(db, "down")
	if got := db.SQLs(); len(got) != 0 {
		t.Fatalf("expected no SQL, got %v", got)
	}
}
