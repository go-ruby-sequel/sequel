// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"os/exec"
	"strings"
	"testing"
)

// The oracle re-derives the expected SQL strings from the real `sequel` gem
// (using its `mock` adapter, so no database is touched) and asserts this
// library emits the same bytes. It skips itself where ruby or the gem is
// absent — the qemu cross-arch lanes and the Windows lane — so the
// deterministic golden vectors in sequel_test.go alone drive the 100% gate
// there. On the ubuntu/macos lanes ruby+sequel are installed and the oracle
// runs, pinning the golden strings to the gem's actual output.

// rubySequel locates a `ruby` that can `require "sequel"`, once. It skips the
// test when either is missing.
func rubySequel(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby not on PATH; skipping sequel-gem oracle")
	}
	check := exec.Command(bin, "-e", `require "sequel"`)
	if out, err := check.CombinedOutput(); err != nil {
		t.Skipf("sequel gem not installed (%v): %s", err, out)
	}
	return bin
}

// gemSQL runs a Ruby snippet against a mock Sequel database for the given host
// and returns the trimmed SQL the snippet prints. `body` receives a local `db`
// (the mock database) and must `print` exactly the SQL string under test.
func gemSQL(t *testing.T, bin, host, body string) string {
	t.Helper()
	hostArg := "nil"
	if host != "" {
		hostArg = "'" + host + "'"
	}
	script := `require "sequel"
require "sequel/extensions/migration"
$stdout.binmode
host = ` + hostArg + `
db = host ? Sequel.mock(host: host) : Sequel.mock
` + body
	out, err := exec.Command(bin, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("ruby error: %v\nscript:\n%s\noutput:\n%s", err, script, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// oracleCase pairs a Go SQL-builder closure with the Ruby snippet that yields
// the same SQL from the gem.
type oracleCase struct {
	name string
	host string // "", "postgres", "sqlite"
	got  func(db *Database) string
	ruby string // Ruby body; must print the SQL
}

func TestOracleDataset(t *testing.T) {
	bin := rubySequel(t)
	cases := []oracleCase{
		{"select", "", func(db *Database) string { return db.T("items").SelectSQL() },
			`print db[:items].select_sql`},
		{"where-hash", "", func(db *Database) string { return db.T("items").Where(H("id", 1, "name", "x")).SelectSQL() },
			`print db[:items].where(id: 1, name: "x").select_sql`},
		{"where-nil", "", func(db *Database) string { return db.T("items").Where(H("id", nil)).SelectSQL() },
			`print db[:items].where(id: nil).select_sql`},
		{"where-gt", "", func(db *Database) string { return db.T("items").Where(Gt("price", 100)).SelectSQL() },
			`print db[:items].where{price > 100}.select_sql`},
		{"where-in", "", func(db *Database) string { return db.T("items").Where(H("id", []Value{1, 2, 3})).SelectSQL() },
			`print db[:items].where(id: [1,2,3]).select_sql`},
		{"where-like", "", func(db *Database) string { return db.T("items").Where(Like("name", "A%")).SelectSQL() },
			`print db[:items].where(Sequel.like(:name, 'A%')).select_sql`},
		{"where-or", "", func(db *Database) string { return db.T("items").Where(Or(H("id", 1), H("id", 2))).SelectSQL() },
			`print db[:items].where(Sequel.|({id: 1}, {id: 2})).select_sql`},
		{"exclude", "", func(db *Database) string { return db.T("items").Exclude(H("id", 1)).SelectSQL() },
			`print db[:items].exclude(id: 1).select_sql`},
		{"where-subq", "", func(db *Database) string {
			return db.T("items").Where(H("id", db.T("o").Select("id"))).SelectSQL()
		}, `print db[:items].where(id: db[:o].select(:id)).select_sql`},
		{"order", "", func(db *Database) string { return db.T("items").Order("name").SelectSQL() },
			`print db[:items].order(:name).select_sql`},
		{"order-desc", "", func(db *Database) string { return db.T("items").Order(Desc("name")).SelectSQL() },
			`print db[:items].order(Sequel.desc(:name)).select_sql`},
		{"order-multi", "", func(db *Database) string { return db.T("items").Order("a", Desc("b")).SelectSQL() },
			`print db[:items].order(:a, Sequel.desc(:b)).select_sql`},
		{"reverse", "", func(db *Database) string { return db.T("items").Order("a").Reverse().SelectSQL() },
			`print db[:items].order(:a).reverse.select_sql`},
		{"limit", "", func(db *Database) string { return db.T("items").Limit(10).SelectSQL() },
			`print db[:items].limit(10).select_sql`},
		{"limit-offset", "", func(db *Database) string { return db.T("items").LimitOffset(10, 5).SelectSQL() },
			`print db[:items].limit(10, 5).select_sql`},
		{"offset", "", func(db *Database) string { return db.T("items").Offset(5).SelectSQL() },
			`print db[:items].offset(5).select_sql`},
		{"select-cols", "", func(db *Database) string { return db.T("items").Select("a", "b").SelectSQL() },
			`print db[:items].select(:a, :b).select_sql`},
		{"select-alias", "", func(db *Database) string { return db.T("items").Select(As("a", "b")).SelectSQL() },
			`print db[:items].select(Sequel.as(:a, :b)).select_sql`},
		{"distinct", "", func(db *Database) string { return db.T("items").Distinct().SelectSQL() },
			`print db[:items].distinct.select_sql`},
		{"group", "", func(db *Database) string { return db.T("items").Group("cat").SelectSQL() },
			`print db[:items].group(:cat).select_sql`},
		{"having", "", func(db *Database) string {
			return db.T("items").Group("cat").Having(Gt(Function("count", Lit("*")), 1)).SelectSQL()
		}, `print db[:items].group(:cat).having{count.function.* > 1}.select_sql`},
		{"join", "", func(db *Database) string { return db.T("items").Join("orders", JoinOn("item_id", "id")).SelectSQL() },
			`print db[:items].join(:orders, item_id: :id).select_sql`},
		{"left-join", "", func(db *Database) string {
			return db.T("items").LeftJoin("orders", JoinOn("item_id", "id")).SelectSQL()
		},
			`print db[:items].left_join(:orders, item_id: :id).select_sql`},
		{"cross-join", "", func(db *Database) string { return db.T("items").CrossJoin("orders").SelectSQL() },
			`print db[:items].cross_join(:orders).select_sql`},
		{"join-using", "", func(db *Database) string { return db.T("items").Join("o", Using("id")).SelectSQL() },
			`print db[:items].join(:o, [:id]).select_sql`},
		{"union", "", func(db *Database) string { return db.T("items").Union(db.T("other")).SQL() },
			`print db[:items].union(db[:other]).sql`},
		{"intersect", "", func(db *Database) string { return db.T("items").Intersect(db.T("o")).SQL() },
			`print db[:items].intersect(db[:o]).sql`},
		{"except", "", func(db *Database) string { return db.T("items").Except(db.T("o")).SQL() },
			`print db[:items].except(db[:o]).sql`},
		{"qualified", "", func(db *Database) string { return db.T("items").Where(H(Qualify("t", "c"), 1)).SelectSQL() },
			`print db[:items].where(Sequel[:t][:c] => 1).select_sql`},
		{"function", "", func(db *Database) string { return db.T("items").Where(H(Function("lower", "name"), "x")).SelectSQL() },
			`print db[:items].where(Sequel.function(:lower, :name) => 'x').select_sql`},
		{"arith-alias", "", func(db *Database) string {
			return db.T("items").Select(Arith("*", "price", "qty").As("total")).SelectSQL()
		},
			`print db[:items].select{(price * qty).as(total)}.select_sql`},
		{"bool-true", "", func(db *Database) string { return db.T("items").Where(H("active", true)).SelectSQL() },
			`print db[:items].where(active: true).select_sql`},
		{"str-escape", "", func(db *Database) string { return db.T("items").Where(H("name", "O'Brien")).SelectSQL() },
			`print db[:items].where(name: "O'Brien").select_sql`},
		{"float", "", func(db *Database) string { return db.T("items").InsertSQL("x", 1.5) },
			`print db[:items].insert_sql(x: 1.5)`},
		{"date", "", func(db *Database) string { return db.T("items").Where(H("d", NewDate(2026, 7, 2))).SelectSQL() },
			`print db[:items].where(d: Date.new(2026,7,2)).select_sql`},
		{"insert", "", func(db *Database) string { return db.T("items").InsertSQL("name", "x", "age", 3) },
			`print db[:items].insert_sql(name: "x", age: 3)`},
		{"update", "", func(db *Database) string { return db.T("items").UpdateSQL("name", "y") },
			`print db[:items].update_sql(name: "y")`},
		{"delete", "", func(db *Database) string { return db.T("items").Where(H("id", 1)).DeleteSQL() },
			`print db[:items].where(id: 1).delete_sql`},
		{"from-multi", "", func(db *Database) string { return db.From("a", "b").SelectSQL() },
			`print db.from(:a, :b).select_sql`},
		{"from-alias", "", func(db *Database) string { return db.From(As("items", "i")).SelectSQL() },
			`print db.from(Sequel.as(:items, :i)).select_sql`},

		// --- postgres dialect ---
		{"pg-select", "postgres", func(db *Database) string { return db.T("items").SelectSQL() },
			`print db[:items].select_sql`},
		{"pg-cols-where", "postgres", func(db *Database) string { return db.T("items").Select("a", "b").Where(H("id", 1)).SelectSQL() },
			`print db[:items].select(:a, :b).where(id: 1).select_sql`},
		{"pg-qualified", "postgres", func(db *Database) string { return db.T("items").Where(H(Qualify("t", "c"), 1)).SelectSQL() },
			`print db[:items].where(Sequel[:t][:c] => 1).select_sql`},
		{"pg-insert", "postgres", func(db *Database) string { return db.T("items").InsertSQL("name", "x") },
			`print db[:items].insert_sql(name: "x")`},
		{"pg-bool", "postgres", func(db *Database) string { return db.T("items").Where(H("active", true)).SelectSQL() },
			`print db[:items].where(active: true).select_sql`},
		{"pg-blob", "postgres", func(db *Database) string { return db.T("items").InsertSQL("x", Blob([]byte{0x00, 0xFF})) },
			"print db[:items].insert_sql(x: Sequel.blob(\"\\x00\\xFF\".b))"},

		// --- sqlite dialect ---
		{"sq-select", "sqlite", func(db *Database) string { return db.T("items").SelectSQL() },
			`print db[:items].select_sql`},
		{"sq-bool", "sqlite", func(db *Database) string { return db.T("items").Where(H("active", false)).SelectSQL() },
			`print db[:items].where(active: false).select_sql`},
		{"sq-blob", "sqlite", func(db *Database) string { return db.T("items").InsertSQL("x", Blob([]byte{0x00, 0xFF})) },
			"print db[:items].insert_sql(x: Sequel.blob(\"\\x00\\xFF\".b))"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := gemSQL(t, bin, c.host, c.ruby)
			got := c.got(Mock(c.host))
			if got != want {
				t.Errorf("SQL mismatch:\n go  =%q\n gem =%q", got, want)
			}
		})
	}
}

func TestOracleSchema(t *testing.T) {
	bin := rubySequel(t)
	cases := []oracleCase{
		{"create-types", "", func(db *Database) string {
			db.CreateTable("t", func(t *TableBuilder) {
				t.PrimaryKey("id")
				t.String("name")
				t.String("code", Size(10))
				t.Integer("age")
				t.Bignum("big")
				t.Float("score")
				t.Numeric("price", Precision(10, 2))
				t.Bool("active")
				t.Date("d")
			})
			return strings.Join(db.SQLs(), "\n")
		}, `db.create_table(:t){ primary_key :id; String :name; String :code, size: 10; Integer :age; Bignum :big; Float :score; Numeric :price, size: [10,2]; TrueClass :active; Date :d }
print db.sqls.join("\n")`},
		{"create-constraints", "", func(db *Database) string {
			db.CreateTable("u", func(t *TableBuilder) {
				t.PrimaryKey("id")
				t.String("name", DefaultVal("x"), NotNull(), Unique())
				t.ForeignKey("org_id", "orgs", OnDelete("cascade"))
				t.Index([]string{"name"})
				t.Index([]string{"a", "b"}, UniqueIndex())
			})
			return strings.Join(db.SQLs(), "\n")
		}, `db.create_table(:u){ primary_key :id; String :name, null: false, default: 'x', unique: true; foreign_key :org_id, :orgs, on_delete: :cascade; index :name; index [:a, :b], unique: true }
print db.sqls.join("\n")`},
		{"create-pg", "postgres", func(db *Database) string {
			db.CreateTable("t", func(t *TableBuilder) {
				t.PrimaryKey("id")
				t.String("name")
				t.String("code", Size(10))
			})
			return strings.Join(db.SQLs(), "\n")
		}, `db.create_table(:t){ primary_key :id; String :name; String :code, size: 10 }
print db.sqls.join("\n")`},
		{"create-sqlite", "sqlite", func(db *Database) string {
			db.CreateTable("t", func(t *TableBuilder) {
				t.PrimaryKey("id")
				t.String("name", DefaultVal("x"))
			})
			return strings.Join(db.SQLs(), "\n")
		}, `db.create_table(:t){ primary_key :id; String :name, default: 'x' }
print db.sqls.join("\n")`},
		{"alter", "", func(db *Database) string {
			db.AlterTable("t", func(a *AlterBuilder) {
				a.AddColumn(Column{Name: "bio", Type: TypeString})
				a.DropColumn("age")
				a.RenameColumn("name", "fullname")
				a.AddIndex([]string{"bio"})
			})
			return strings.Join(db.SQLs(), "\n")
		}, `db.alter_table(:t){ add_column :bio, String; drop_column :age; rename_column :name, :fullname; add_index :bio }
print db.sqls.join("\n")`},
		{"drop", "", func(db *Database) string {
			db.DropTable("t")
			return strings.Join(db.SQLs(), "\n")
		}, `db.drop_table(:t)
print db.sqls.join("\n")`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := gemSQL(t, bin, c.host, c.ruby)
			got := c.got(Mock(c.host))
			if got != want {
				t.Errorf("schema SQL mismatch:\n go  =%q\n gem =%q", got, want)
			}
		})
	}
}
