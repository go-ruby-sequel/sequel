// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

// This file exercises the seams, dialect helpers and error branches the
// golden vectors in sequel_test.go do not reach, so the deterministic,
// ruby-free suite alone holds the package at 100% coverage.
package sequel

import (
	"errors"
	"testing"
)

// --- Executor seam / Database plumbing -----------------------------------

func TestExecutorSeam(t *testing.T) {
	var ran []string
	exec := ExecutorFunc(func(sql string) ([]map[string]Value, error) {
		ran = append(ran, sql)
		if sql == `SELECT * FROM "boom"` {
			return nil, errors.New("boom")
		}
		return []map[string]Value{{"id": 1}}, nil
	})
	db := Connect("postgres", exec)
	if db.Dialect() != Postgres {
		t.Fatalf("dialect = %v", db.Dialect())
	}
	// Run raw.
	rows, err := db.Run("SELECT 1")
	if err != nil || len(rows) != 1 {
		t.Fatalf("run: rows=%v err=%v", rows, err)
	}
	// All routes the SELECT through the executor.
	if _, err := db.All(db.T("items")); err != nil {
		t.Fatal(err)
	}
	// Error propagation.
	if _, err := db.All(db.T("boom")); err == nil {
		t.Fatal("expected error")
	}
	// Execute directly on the func adapter.
	if _, err := exec.Execute("SELECT 1"); err != nil {
		t.Fatal(err)
	}
	// DDL through a wired executor runs (not logged).
	db.CreateTable("t", func(tb *TableBuilder) { tb.PrimaryKey("id") })
	if got := db.SQLs(); len(got) != 0 {
		t.Fatalf("wired DDL should not log, got %v", got)
	}
	if len(ran) == 0 {
		t.Fatal("executor never ran")
	}
}

func TestRunWithoutExecutorLogs(t *testing.T) {
	db := Mock("default")
	if rows, err := db.Run("SELECT 1"); rows != nil || err != nil {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if got := db.SQLs(); len(got) != 1 || got[0] != "SELECT 1" {
		t.Fatalf("log = %v", got)
	}
}

// --- Dialect helpers -----------------------------------------------------

func TestDialectByNameAndString(t *testing.T) {
	cases := []struct {
		name string
		want Dialect
		str  string
	}{
		{"sqlite", SQLite, "sqlite"},
		{"postgres", Postgres, "postgres"},
		{"postgresql", Postgres, "postgres"},
		{"pg", Postgres, "postgres"},
		{"default", Default, "default"},
		{"unknown", Default, "default"},
		{"", Default, "default"},
	}
	for _, c := range cases {
		d := DialectByName(c.name)
		if d != c.want {
			t.Errorf("DialectByName(%q) = %v", c.name, d)
		}
		if d.String() != c.str {
			t.Errorf("%q.String() = %q", c.name, d.String())
		}
	}
}

// --- Expression edge branches --------------------------------------------

func TestNegateCmpAllOperators(t *testing.T) {
	ds := Mock("default").T("t")
	cases := []struct{ in, want string }{
		{ds.Where(Not(Eq("a", 1))).SelectSQL(), "SELECT * FROM t WHERE (a != 1)"},
		{ds.Where(Not(Neq("a", 1))).SelectSQL(), "SELECT * FROM t WHERE (a = 1)"},
		{ds.Where(Not(Gt("a", 1))).SelectSQL(), "SELECT * FROM t WHERE (a <= 1)"},
		{ds.Where(Not(Lt("a", 1))).SelectSQL(), "SELECT * FROM t WHERE (a >= 1)"},
		{ds.Where(Not(Gte("a", 1))).SelectSQL(), "SELECT * FROM t WHERE (a < 1)"},
		{ds.Where(Not(Lte("a", 1))).SelectSQL(), "SELECT * FROM t WHERE (a > 1)"},
		// A LIKE operator is not a comparison; negateCmp returns it unchanged when
		// reached via a booleanOp with a non-comparison op (defensive default).
	}
	for i, c := range cases {
		if c.in != c.want {
			t.Errorf("case %d: got=%q want=%q", i, c.in, c.want)
		}
	}
	// negateCmp default branch: an unknown operator is returned unchanged.
	if got := negateCmp("LIKE"); got != "LIKE" {
		t.Errorf("negateCmp default = %q", got)
	}
	// flipLogical OR->AND branch.
	if got := flipLogical("OR"); got != "AND" {
		t.Errorf("flipLogical(OR) = %q", got)
	}
}

func TestOrExclude(t *testing.T) {
	// Not over an OR flips to AND of negations.
	ds := Mock("default").T("t")
	got := ds.Where(Not(Or(H("a", 1), H("b", 2)))).SelectSQL()
	want := "SELECT * FROM t WHERE ((a != 1) AND (b != 2))"
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestOrderTermLiteralValue(t *testing.T) {
	// A non-Expr, non-string order term becomes a literal value; reversing it
	// wraps it descending.
	ds := Mock("default").T("t")
	got := ds.Order(5).SelectSQL()
	if got != "SELECT * FROM t ORDER BY 5" {
		t.Errorf("order literal = %q", got)
	}
	got = ds.Order(Function("f")).Reverse().SelectSQL()
	if got != "SELECT * FROM t ORDER BY f() DESC" {
		t.Errorf("reverse bare expr = %q", got)
	}
}

func TestExprOfDataset(t *testing.T) {
	// exprOf on a *Dataset yields a scalar sub-select.
	sub := Mock("default").T("t").Select("id")
	got := Mock("default").T("u").Select(Function("max", sub)).SelectSQL()
	if got != "SELECT max((SELECT id FROM t)) FROM u" {
		t.Errorf("scalar subq = %q", got)
	}
}

func TestBoolWord(t *testing.T) {
	if boolWord(true) != "TRUE" || boolWord(false) != "FALSE" {
		t.Fatal("boolWord")
	}
}

func TestLiteralValueFallback(t *testing.T) {
	// An unknown type with a String() method literalizes via to_s-style fallback.
	got := Mock("default").T("t").Where(Eq("a", stringer{"hi"})).SelectSQL()
	if got != "SELECT * FROM t WHERE (a = 'hi')" {
		t.Errorf("stringer fallback = %q", got)
	}
	// An unknown type without String() literalizes to an empty quoted string.
	got = Mock("default").T("t").Where(Eq("a", noStringer{})).SelectSQL()
	if got != "SELECT * FROM t WHERE (a = '')" {
		t.Errorf("empty fallback = %q", got)
	}
}

type stringer struct{ s string }

func (s stringer) String() string { return s.s }

type noStringer struct{}

func TestLiteralValueList(t *testing.T) {
	// A []Value used directly as a value literalizes to a parenthesised list.
	got := Mock("default").T("t").Where(Eq(Function("row"), []Value{1, 2})).SelectSQL()
	if got != "SELECT * FROM t WHERE (row() = (1, 2))" {
		t.Errorf("value list = %q", got)
	}
}

// --- Panics on odd argument counts ---------------------------------------

func TestPanics(t *testing.T) {
	assertPanic(t, "H", func() { H("a") })
	assertPanic(t, "JoinOn", func() { JoinOn("a") })
	assertPanic(t, "InsertSQL", func() { Mock("default").T("t").InsertSQL("a") })
	assertPanic(t, "UpdateSQL", func() { Mock("default").T("t").UpdateSQL("a") })
}

func assertPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic", name)
		}
	}()
	fn()
}

// --- Schema edge branches -------------------------------------------------

func TestDropIndexCustomName(t *testing.T) {
	db := Mock("default")
	db.AlterTable("t", func(a *AlterBuilder) {
		a.DropIndex([]string{"x"}, IndexName("named_idx"))
	})
	if got := db.SQLs(); len(got) != 1 || got[0] != "DROP INDEX named_idx" {
		t.Fatalf("drop index = %v", got)
	}
}

func TestConnectNilExecutorLogsDDL(t *testing.T) {
	// Connect with a nil executor behaves like Mock for DDL logging.
	db := Connect("sqlite", nil)
	db.DropTable("t")
	if got := db.SQLs(); len(got) != 1 || got[0] != "DROP TABLE `t`" {
		t.Fatalf("connect-nil DDL = %v", got)
	}
}

// --- Remaining coverage seams --------------------------------------------

func TestDatasetDialectNilDB(t *testing.T) {
	// A dataset with no database defaults to the Default dialect.
	d := &Dataset{sources: []source{{name: "t"}}}
	if got := d.SelectSQL(); got != "SELECT * FROM t" {
		t.Fatalf("nil-db dataset = %q", got)
	}
}

func TestLastSourceNameEmpty(t *testing.T) {
	// A cross join onto an empty (source-less) dataset qualifies against "".
	d := &Dataset{}
	got := d.Join("o", JoinOn("a", "b")).SelectSQL()
	// No FROM, joined table qualifies its key against the joined table and the
	// (empty) previous source.
	if got != "SELECT * INNER JOIN o ON (o.a = .b)" {
		t.Fatalf("empty-source join = %q", got)
	}
}

func TestSourceOfExprAndDefault(t *testing.T) {
	// A bare Expr source (not aliased/subquery) is emitted directly.
	got := Mock("default").From(Function("generate_series", 1, 3)).SelectSQL()
	if got != "SELECT * FROM generate_series(1, 3)" {
		t.Fatalf("expr source = %q", got)
	}
	// An unrecognised source type degrades to an empty bare name (defensive).
	got = Mock("default").From(42).SelectSQL()
	if got != "SELECT * FROM " {
		t.Fatalf("unknown source = %q", got)
	}
}

func TestJoinWithDefaultCond(t *testing.T) {
	// A join condition that is neither Using/hash/Expr is coerced via condOf; a
	// []HashPair ON hash compares its columns as-is.
	got := Mock("default").T("t").Join("o", []HashPair{{Key: "x", Value: 1}}).SelectSQL()
	if got != "SELECT * FROM t INNER JOIN o ON (x = 1)" {
		t.Fatalf("hashpair join cond = %q", got)
	}
	// An Expr ON is used verbatim.
	got = Mock("default").T("t").Join("o", Lit("o.x = t.y")).SelectSQL()
	if got != "SELECT * FROM t INNER JOIN o ON o.x = t.y" {
		t.Fatalf("literal join cond = %q", got)
	}
}

func TestLiteralBoolDefaultAndSqlite(t *testing.T) {
	// literalBool Default branch (bare bool under Default is TRUE/FALSE via
	// litValue, but literalBool itself is reached for the sqlite comparison).
	got := Mock("sqlite").T("t").Where(H("a", true)).SelectSQL()
	if got != "SELECT * FROM `t` WHERE (`a` = 't')" {
		t.Fatalf("sqlite bool = %q", got)
	}
	// The Default dialect's bare-bool path.
	got = Mock("default").T("t").Select(Function("f", false)).SelectSQL()
	if got != "SELECT f(FALSE) FROM t" {
		t.Fatalf("default bare bool = %q", got)
	}
	// Postgres bare bool goes through literalBool false branch.
	got = Mock("postgres").T("t").Select(Function("f", false)).SelectSQL()
	if got != `SELECT f(false) FROM "t"` {
		t.Fatalf("postgres bare bool = %q", got)
	}
}

func TestLiteralBlobPrintableAndBytes(t *testing.T) {
	// Postgres blob with a printable, non-quote, non-backslash byte.
	got := Mock("postgres").T("t").InsertSQL("x", Blob([]byte("A\x00")))
	if got != `INSERT INTO "t" ("x") VALUES ('A\000')` {
		t.Fatalf("pg printable blob = %q", got)
	}
	// []byte value under sqlite goes through literalBlob X'..'.
	got = Mock("sqlite").T("t").InsertSQL("x", []byte{0xAB})
	if got != "INSERT INTO `t` (`x`) VALUES (X'ab')" {
		t.Fatalf("sqlite []byte = %q", got)
	}
}

func TestLiteralFloatPlainExponentPadding(t *testing.T) {
	// An exponent already two digits keeps its form; a large exponent is not
	// padded further.
	got := Mock("default").T("t").Where(H("n", 1e100)).SelectSQL()
	if got != "SELECT * FROM t WHERE (n = 1.0e+100)" {
		t.Fatalf("1e100 = %q", got)
	}
}

func TestCondOfParenAndLiteralValue(t *testing.T) {
	// Re-wrapping an already-paren literal filter is idempotent (condOf parenExpr
	// branch), reached by chaining two literal filters.
	got := Mock("default").T("t").Where(Lit("a")).Where(Lit("b")).SelectSQL()
	if got != "SELECT * FROM t WHERE ((a) AND (b))" {
		t.Fatalf("chained lit = %q", got)
	}
	// A non-Expr, non-hash, non-literal condition value falls to literalValue.
	got = Mock("default").T("t").Where(true).SelectSQL()
	if got != "SELECT * FROM t WHERE TRUE" {
		t.Fatalf("bool cond = %q", got)
	}
}

func TestExprOfExprPassthrough(t *testing.T) {
	// exprOf on an existing Expr returns it unchanged.
	got := Mock("default").T("t").Where(Eq("a", Lit("now()"))).SelectSQL()
	if got != "SELECT * FROM t WHERE (a = now())" {
		t.Fatalf("expr rhs = %q", got)
	}
	// exprOf on a *Dataset yields a scalar sub-select on the comparison RHS.
	sub := Mock("default").T("m").Select(Function("max", "id"))
	got = Mock("default").T("t").Where(Eq("a", sub)).SelectSQL()
	if got != "SELECT * FROM t WHERE (a = (SELECT max(id) FROM m))" {
		t.Fatalf("dataset rhs = %q", got)
	}
}

func TestLitValueExprDatasetNil(t *testing.T) {
	// A bare Expr as an INSERT value literalizes via its SQL.
	got := Mock("default").T("t").InsertSQL("a", Function("now"))
	if got != "INSERT INTO t (a) VALUES (now())" {
		t.Fatalf("expr value = %q", got)
	}
	// A bare *Dataset as an INSERT value literalizes to a parenthesised select.
	sub := Mock("default").T("m").Select("id")
	got = Mock("default").T("t").InsertSQL("a", sub)
	if got != "INSERT INTO t (a) VALUES ((SELECT id FROM m))" {
		t.Fatalf("dataset value = %q", got)
	}
	// A nil INSERT value literalizes to NULL.
	got = Mock("default").T("t").InsertSQL("a", nil)
	if got != "INSERT INTO t (a) VALUES (NULL)" {
		t.Fatalf("nil value = %q", got)
	}
	// An int64 value.
	got = Mock("default").T("t").InsertSQL("a", int64(7))
	if got != "INSERT INTO t (a) VALUES (7)" {
		t.Fatalf("int64 value = %q", got)
	}
}
