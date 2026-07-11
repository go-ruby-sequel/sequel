// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"errors"
	"reflect"
	"regexp"
	"testing"
)

// mockExec is a Sequel-mock-style executor for the Model behavioural tests: it
// records every SQL string, returns queued result sets in order, hands out
// auto-incrementing primary keys for inserts, and can be told to fail on a
// specific call to exercise error branches. It implements both [Executor] and
// [KeyExecutor].
type mockExec struct {
	sqls   []string
	fetch  [][]map[string]Value
	fi     int
	autoid int
	calls  int
	errAt  int  // 1-based call index to fail on; 0 = never
	always bool // fail on every call
	errVal error
}

func newMock(fetch ...[]map[string]Value) *mockExec {
	return &mockExec{fetch: fetch, errAt: 0}
}

func (m *mockExec) next() []map[string]Value {
	if m.fi < len(m.fetch) {
		r := m.fetch[m.fi]
		m.fi++
		return r
	}
	return nil
}

func (m *mockExec) Execute(sql string) ([]map[string]Value, error) {
	m.sqls = append(m.sqls, sql)
	m.calls++
	if m.always || m.errAt == m.calls {
		return nil, m.errVal
	}
	return m.next(), nil
}

func (m *mockExec) ExecuteInsert(sql string) (Value, error) {
	m.sqls = append(m.sqls, sql)
	m.calls++
	if m.always || m.errAt == m.calls {
		return nil, m.errVal
	}
	m.autoid++
	return m.autoid, nil
}

func (m *mockExec) last() string {
	if len(m.sqls) == 0 {
		return ""
	}
	return m.sqls[len(m.sqls)-1]
}

// dbWith wires a Database to a mock executor for a dialect.
func dbWith(exec Executor) *Database { return Connect("default", exec) }

func itemsModel(db *Database) *ModelClass {
	return db.Model("items").SetColumns("id", "name", "price")
}

// ---- construction / dirty tracking --------------------------------------

func TestModelNewAndDirty(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	i := m.New("name", "x", "price", 5)
	if !i.IsNew() {
		t.Fatal("New instance should be new")
	}
	if !i.Modified() {
		t.Fatal("New instance with set columns should be modified")
	}
	if got := i.ChangedColumns(); !reflect.DeepEqual(got, []string{"name", "price"}) {
		t.Fatalf("changed columns = %v", got)
	}
	if i.Get("name") != "x" {
		t.Fatalf("Get name = %v", i.Get("name"))
	}
	// Setting the same value does not dirty.
	i2 := m.Load(map[string]Value{"id": 1, "name": "x"})
	i2.Set("name", "x")
	if i2.Modified() {
		t.Fatal("setting identical value should not modify")
	}
	i2.Set("name", "y")
	if !i2.Modified() || !reflect.DeepEqual(i2.ChangedColumns(), []string{"name"}) {
		t.Fatalf("expected name dirty, got %v", i2.ChangedColumns())
	}
}

func TestModelDirtyBytes(t *testing.T) {
	m := dbWith(newMock()).Model("blobs").SetColumns("id", "data")
	i := m.Load(map[string]Value{"id": 1, "data": []byte("ab")})
	i.Set("data", []byte("ab"))
	if i.Modified() {
		t.Fatal("identical byte slice should not modify")
	}
	i.Set("data", []byte("cd"))
	if !i.Modified() {
		t.Fatal("changed byte slice should modify")
	}
}

func TestModelLoadColumnOrder(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	// Extra keys beyond declared columns are appended in sorted order.
	i := m.Load(map[string]Value{"id": 1, "name": "x", "zeta": 9, "alpha": 2})
	if got := i.order; !reflect.DeepEqual(got, []string{"id", "name", "alpha", "zeta"}) {
		t.Fatalf("order = %v", got)
	}
}

// ---- CRUD ---------------------------------------------------------------

func TestModelCreate(t *testing.T) {
	mock := newMock([]map[string]Value{{"id": 1, "name": "x", "price": 5}}) // refresh row
	m := itemsModel(dbWith(mock))
	i, err := m.Create("name", "x", "price", 5)
	if err != nil {
		t.Fatal(err)
	}
	if i.IsNew() {
		t.Fatal("created instance should not be new")
	}
	if i.PK() != 1 {
		t.Fatalf("pk = %v", i.PK())
	}
	if i.Modified() {
		t.Fatal("created instance should be clean")
	}
	wantSQL := []string{
		"INSERT INTO items (name, price) VALUES ('x', 5)",
		"SELECT * FROM items WHERE (id = 1) LIMIT 1",
	}
	if !reflect.DeepEqual(mock.sqls, wantSQL) {
		t.Fatalf("sqls = %#v", mock.sqls)
	}
}

func TestModelCreateNoKeyExecutor(t *testing.T) {
	var got []string
	exec := ExecutorFunc(func(sql string) ([]map[string]Value, error) {
		got = append(got, sql)
		return nil, nil
	})
	m := itemsModel(Connect("default", exec))
	m.refreshOnCreate = false
	i, err := m.Create("id", 7, "name", "x")
	if err != nil {
		t.Fatal(err)
	}
	if i.PK() != 7 {
		t.Fatalf("pk = %v", i.PK())
	}
	if got[0] != "INSERT INTO items (id, name) VALUES (7, 'x')" {
		t.Fatalf("insert sql = %q", got[0])
	}
}

func TestModelSaveUpdate(t *testing.T) {
	mock := newMock()
	m := itemsModel(dbWith(mock))
	i := m.Load(map[string]Value{"id": 1, "name": "x", "price": 5})
	i.Set("name", "y")
	if err := i.Save(); err != nil {
		t.Fatal(err)
	}
	if mock.last() != "UPDATE items SET name = 'y' WHERE (id = 1)" {
		t.Fatalf("update sql = %q", mock.last())
	}
	if i.Modified() {
		t.Fatal("save should clear dirty")
	}
}

func TestModelSaveNoChange(t *testing.T) {
	mock := newMock()
	m := itemsModel(dbWith(mock))
	i := m.Load(map[string]Value{"id": 1, "name": "x"})
	if err := i.Save(); err != nil {
		t.Fatal(err)
	}
	if len(mock.sqls) != 0 {
		t.Fatalf("expected no SQL, got %v", mock.sqls)
	}
}

func TestModelUpdateMethod(t *testing.T) {
	mock := newMock()
	m := itemsModel(dbWith(mock))
	i := m.Load(map[string]Value{"id": 2, "name": "x"})
	if err := i.Update("name", "z"); err != nil {
		t.Fatal(err)
	}
	if mock.last() != "UPDATE items SET name = 'z' WHERE (id = 2)" {
		t.Fatalf("update sql = %q", mock.last())
	}
}

func TestModelDeleteDestroy(t *testing.T) {
	mock := newMock()
	m := itemsModel(dbWith(mock))
	i := m.Load(map[string]Value{"id": 3})
	if err := i.Delete(); err != nil {
		t.Fatal(err)
	}
	if mock.last() != "DELETE FROM items WHERE (id = 3)" {
		t.Fatalf("delete sql = %q", mock.last())
	}
	var order []string
	m.BeforeDestroy(func(*Instance) error { order = append(order, "before"); return nil })
	m.AfterDestroy(func(*Instance) error { order = append(order, "after"); return nil })
	if err := i.Destroy(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"before", "after"}) {
		t.Fatalf("destroy hook order = %v", order)
	}
}

func TestModelRefresh(t *testing.T) {
	mock := newMock([]map[string]Value{{"id": 1, "name": "fresh", "price": 9}})
	m := itemsModel(dbWith(mock))
	i := m.Load(map[string]Value{"id": 1, "name": "stale"})
	if err := i.Refresh(); err != nil {
		t.Fatal(err)
	}
	if i.Get("name") != "fresh" || i.Get("price") != 9 {
		t.Fatalf("refresh values = %v", i.values)
	}
	if mock.last() != "SELECT * FROM items WHERE (id = 1) LIMIT 1" {
		t.Fatalf("refresh sql = %q", mock.last())
	}
}

func TestModelRefreshNoRow(t *testing.T) {
	mock := newMock() // no rows
	m := itemsModel(dbWith(mock))
	i := m.Load(map[string]Value{"id": 1, "name": "stale"})
	if err := i.Refresh(); err != nil {
		t.Fatal(err)
	}
	if i.Get("name") != "stale" {
		t.Fatal("no-row refresh should keep values")
	}
}

// ---- finders ------------------------------------------------------------

func TestModelWithPK(t *testing.T) {
	mock := newMock([]map[string]Value{{"id": 5, "name": "x"}})
	m := itemsModel(dbWith(mock))
	i, err := m.Get(5)
	if err != nil {
		t.Fatal(err)
	}
	if i == nil || i.PK() != 5 || i.IsNew() {
		t.Fatalf("with_pk = %+v", i)
	}
	if mock.last() != "SELECT * FROM items WHERE (id = 5) LIMIT 1" {
		t.Fatalf("with_pk sql = %q", mock.last())
	}
}

func TestModelWithPKMissing(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	i, err := m.WithPK(9)
	if err != nil || i != nil {
		t.Fatalf("expected nil,nil got %v,%v", i, err)
	}
}

func TestModelCompositePK(t *testing.T) {
	mock := newMock([]map[string]Value{{"a": 1, "b": 2}})
	m := dbWith(mock).Model("t").SetColumns("a", "b").SetPrimaryKey("a", "b")
	if _, err := m.WithPK([]Value{1, 2}); err != nil {
		t.Fatal(err)
	}
	if mock.last() != "SELECT * FROM t WHERE ((a = 1) AND (b = 2)) LIMIT 1" {
		t.Fatalf("composite pk sql = %q", mock.last())
	}
	i := m.Load(map[string]Value{"a": 1, "b": 2})
	if got := i.PK(); !reflect.DeepEqual(got, []Value{1, 2}) {
		t.Fatalf("composite PK() = %v", got)
	}
}

func TestModelFirstAll(t *testing.T) {
	mock := newMock(
		[]map[string]Value{{"id": 1}},
		[]map[string]Value{{"id": 1}, {"id": 2}},
	)
	m := itemsModel(dbWith(mock))
	first, err := m.First()
	if err != nil || first.PK() != 1 {
		t.Fatalf("first = %v %v", first, err)
	}
	if mock.sqls[0] != "SELECT * FROM items LIMIT 1" {
		t.Fatalf("first sql = %q", mock.sqls[0])
	}
	all, err := m.All()
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %v %v", all, err)
	}
}

func TestModelFirstEmpty(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	i, err := m.First()
	if err != nil || i != nil {
		t.Fatalf("empty first = %v %v", i, err)
	}
}

func TestModelWhereOrderLimit(t *testing.T) {
	mock := newMock([]map[string]Value{{"id": 1}})
	m := itemsModel(dbWith(mock))
	md := m.Where(H("name", "x")).Order("price").Limit(3).Exclude(H("id", 9))
	insts, err := md.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 1 {
		t.Fatalf("insts = %d", len(insts))
	}
	want := "SELECT * FROM items WHERE ((name = 'x') AND (id != 9)) ORDER BY price LIMIT 3"
	if md.SQL() != want {
		t.Fatalf("sql = %q", md.SQL())
	}
	if md.Dataset().SelectSQL() != want {
		t.Fatal("Dataset() mismatch")
	}
}

func TestModelEach(t *testing.T) {
	mock := newMock([]map[string]Value{{"id": 1}, {"id": 2}})
	m := itemsModel(dbWith(mock))
	var ids []Value
	if err := m.ModelDataset().Each(func(i *Instance) error { ids = append(ids, i.PK()); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []Value{1, 2}) {
		t.Fatalf("each ids = %v", ids)
	}
	// Each propagates fn error.
	stop := errors.New("stop")
	m2 := itemsModel(dbWith(newMock([]map[string]Value{{"id": 1}})))
	if err := m2.ModelDataset().Each(func(*Instance) error { return stop }); err != stop {
		t.Fatalf("expected each error, got %v", err)
	}
}

// ---- named dataset methods ----------------------------------------------

func TestDatasetModule(t *testing.T) {
	mock := newMock([]map[string]Value{{"id": 1}})
	m := itemsModel(dbWith(mock))
	m.DatasetModule(map[string]func(*Dataset) *Dataset{
		"cheap": func(d *Dataset) *Dataset { return d.Where(Lt("price", 10)) },
	})
	m.Def("named", func(d *Dataset) *Dataset { return d.Order("name") })
	if got := m.DatasetMethod("cheap").SQL(); got != "SELECT * FROM items WHERE (price < 10)" {
		t.Fatalf("cheap sql = %q", got)
	}
	if _, err := m.DatasetMethod("named").All(); err != nil {
		t.Fatal(err)
	}
}

// ---- hooks --------------------------------------------------------------

func TestModelHookOrder(t *testing.T) {
	mock := newMock([]map[string]Value{{"id": 1, "name": "x"}})
	m := itemsModel(dbWith(mock))
	var order []string
	m.BeforeValidation(func(*Instance) error { order = append(order, "bv"); return nil })
	m.AfterValidation(func(*Instance) error { order = append(order, "av"); return nil })
	m.BeforeSave(func(*Instance) error { order = append(order, "bs"); return nil })
	m.BeforeCreate(func(*Instance) error { order = append(order, "bc"); return nil })
	m.AfterCreate(func(*Instance) error { order = append(order, "ac"); return nil })
	m.AfterSave(func(*Instance) error { order = append(order, "as"); return nil })
	if _, err := m.Create("name", "x"); err != nil {
		t.Fatal(err)
	}
	want := []string{"bv", "av", "bs", "bc", "ac", "as"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("hook order = %v", order)
	}
}

func TestModelUpdateHooks(t *testing.T) {
	mock := newMock()
	m := itemsModel(dbWith(mock))
	var order []string
	m.BeforeUpdate(func(*Instance) error { order = append(order, "bu"); return nil })
	m.AfterUpdate(func(*Instance) error { order = append(order, "au"); return nil })
	i := m.Load(map[string]Value{"id": 1, "name": "x"})
	i.Set("name", "y")
	if err := i.Save(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"bu", "au"}) {
		t.Fatalf("update hooks = %v", order)
	}
}

func TestModelHookAbort(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name  string
		setup func(*ModelClass)
	}{
		{"before_save", func(m *ModelClass) { m.BeforeSave(func(*Instance) error { return boom }) }},
		{"before_create", func(m *ModelClass) { m.BeforeCreate(func(*Instance) error { return boom }) }},
		{"after_create", func(m *ModelClass) { m.AfterCreate(func(*Instance) error { return boom }) }},
		{"after_save", func(m *ModelClass) { m.AfterSave(func(*Instance) error { return boom }) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := itemsModel(dbWith(newMock([]map[string]Value{{"id": 1}})))
			m.refreshOnCreate = false
			tc.setup(m)
			if _, err := m.Create("name", "x"); err != boom {
				t.Fatalf("expected boom, got %v", err)
			}
		})
	}
}

func TestModelHookAbortUpdateAndDestroy(t *testing.T) {
	boom := errors.New("boom")
	m := itemsModel(dbWith(newMock()))
	m.BeforeUpdate(func(*Instance) error { return boom })
	m.BeforeDestroy(func(*Instance) error { return boom })
	i := m.Load(map[string]Value{"id": 1, "name": "x"})
	i.Set("name", "y")
	if err := i.Save(); err != boom {
		t.Fatalf("update abort = %v", err)
	}
	if err := i.Destroy(); err != boom {
		t.Fatalf("destroy abort = %v", err)
	}
}

func TestModelValidationHookAbort(t *testing.T) {
	boom := errors.New("bv-fail")
	m := itemsModel(dbWith(newMock()))
	m.BeforeValidation(func(*Instance) error { return boom })
	i := m.New("name", "x")
	if i.Valid() {
		t.Fatal("expected invalid")
	}
	m2 := itemsModel(dbWith(newMock()))
	m2.AfterValidation(func(*Instance) error { return boom })
	if m2.New("name", "x").Valid() {
		t.Fatal("expected invalid via after-validation")
	}
}

// ---- error propagation --------------------------------------------------

func TestModelExecErrors(t *testing.T) {
	boom := errors.New("exec-fail")
	newFailing := func(at int) *mockExec { mk := newMock(); mk.errAt = at; mk.errVal = boom; return mk }

	// insert error (via KeyExecutor)
	m := itemsModel(dbWith(newFailing(1)))
	if _, err := m.Create("name", "x"); err != boom {
		t.Fatalf("insert err = %v", err)
	}
	// insert error via plain executor
	fexec := ExecutorFunc(func(string) ([]map[string]Value, error) { return nil, boom })
	if _, err := itemsModel(Connect("default", fexec)).Create("name", "x"); err != boom {
		t.Fatalf("plain insert err = %v", err)
	}
	// refresh error after successful insert
	mk := newMock()
	mk.errAt = 2
	mk.errVal = boom
	if _, err := itemsModel(dbWith(mk)).Create("name", "x"); err != boom {
		t.Fatalf("refresh err = %v", err)
	}
	// update error
	mu := newFailing(1)
	iu := itemsModel(dbWith(mu)).Load(map[string]Value{"id": 1, "name": "x"})
	iu.Set("name", "y")
	if err := iu.Save(); err != boom {
		t.Fatalf("update err = %v", err)
	}
	// delete error
	md := newFailing(1)
	if err := itemsModel(dbWith(md)).Load(map[string]Value{"id": 1}).Delete(); err != boom {
		t.Fatalf("delete err = %v", err)
	}
	// finder errors
	for _, run := range []func(*ModelClass) error{
		func(m *ModelClass) error { _, e := m.WithPK(1); return e },
		func(m *ModelClass) error { _, e := m.First(); return e },
		func(m *ModelClass) error { _, e := m.All(); return e },
		func(m *ModelClass) error { return m.Load(map[string]Value{"id": 1}).Refresh() },
	} {
		if err := run(itemsModel(dbWith(newFailing(1)))); err != boom {
			t.Fatalf("finder err = %v", err)
		}
	}
}

// ---- validations --------------------------------------------------------

func TestValidatesPresence(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	m.ValidatesPresence("name")
	i := m.New("name", "")
	if i.Valid() {
		t.Fatal("blank name should be invalid")
	}
	if got := i.Errors().FullMessages(); !reflect.DeepEqual(got, []string{"name is not present"}) {
		t.Fatalf("errors = %v", got)
	}
	if i.Errors().On("name")[0] != "is not present" {
		t.Fatal("On(name) wrong")
	}
	// non-blank passes
	if !m.New("name", "x").Valid() {
		t.Fatal("non-blank should be valid")
	}
	// nil is blank
	if m.New("price", 1).Valid() {
		t.Fatal("nil name should be invalid")
	}
}

func TestValidatesPresenceBlankTypes(t *testing.T) {
	m := dbWith(newMock()).Model("t").SetColumns("a", "b")
	m.ValidatesPresence("a")
	// whitespace string is blank
	if m.New("a", "   ").Valid() {
		t.Fatal("whitespace is blank")
	}
	// empty bytes blank, non-empty ok, number ok
	if m.New("a", []byte{}).Valid() {
		t.Fatal("empty bytes blank")
	}
	if !m.New("a", []byte("x")).Valid() {
		t.Fatal("non-empty bytes present")
	}
	if !m.New("a", 0).Valid() {
		t.Fatal("number is present")
	}
}

func TestValidatesFormat(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	m.ValidatesFormat(regexp.MustCompile(`\A[a-z]+\z`), "name")
	if m.New("name", "ABC").Valid() {
		t.Fatal("uppercase should fail format")
	}
	if m.New("name", 5).Valid() {
		t.Fatal("non-string should fail format")
	}
	i := m.New("name", "ABC")
	i.Valid()
	if i.Errors().On("name")[0] != "is invalid" {
		t.Fatal("format message")
	}
	if !m.New("name", "abc").Valid() {
		t.Fatal("lowercase should pass")
	}
}

func TestValidatesLength(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	m.ValidatesLength("name", LengthOpts{Min: 3, Max: 5})
	i := m.New("name", "ab")
	i.Valid()
	if i.Errors().On("name")[0] != "is shorter than 3 characters" {
		t.Fatalf("short msg = %v", i.Errors().On("name"))
	}
	j := m.New("name", "abcdef")
	j.Valid()
	if j.Errors().On("name")[0] != "is longer than 5 characters" {
		t.Fatalf("long msg = %v", j.Errors().On("name"))
	}
	if !m.New("name", "abcd").Valid() {
		t.Fatal("in-range should pass")
	}
	// exact length
	me := dbWith(newMock()).Model("t").SetColumns("c")
	me.ValidatesLength("c", LengthOpts{Is: 4, HasIs: true})
	ie := me.New("c", "abc")
	ie.Valid()
	if ie.Errors().On("c")[0] != "is not 4 characters" {
		t.Fatalf("exact msg = %v", ie.Errors().On("c"))
	}
	// non-string
	if me.New("c", 9).Valid() {
		t.Fatal("non-string length should fail")
	}
	in := me.New("c", 9)
	in.Valid()
	if in.Errors().On("c")[0] != "is not a valid string" {
		t.Fatalf("non-string msg = %v", in.Errors().On("c"))
	}
}

func TestValidatesUnique(t *testing.T) {
	// new record, no duplicate
	mock := newMock() // count query returns no rows
	m := itemsModel(dbWith(mock))
	m.ValidatesUnique("name")
	i := m.New("name", "y")
	if !i.Valid() {
		t.Fatal("no duplicate should be valid")
	}
	if mock.last() != "SELECT 1 AS one FROM items WHERE (name = 'y') LIMIT 1" {
		t.Fatalf("unique sql = %q", mock.last())
	}
	// duplicate found
	mock2 := newMock([]map[string]Value{{"one": 1}})
	m2 := itemsModel(dbWith(mock2))
	m2.ValidatesUnique("name")
	dup := m2.New("name", "y")
	if dup.Valid() {
		t.Fatal("duplicate should be invalid")
	}
	if dup.Errors().On("name")[0] != "is already taken" {
		t.Fatal("unique message")
	}
	// existing record, unchanged column -> skipped (no SQL)
	mock3 := newMock()
	m3 := itemsModel(dbWith(mock3))
	m3.ValidatesUnique("name")
	ex := m3.Load(map[string]Value{"id": 3, "name": "y"})
	if !ex.Valid() {
		t.Fatal("unchanged should be valid")
	}
	if len(mock3.sqls) != 0 {
		t.Fatalf("expected no SQL for unchanged, got %v", mock3.sqls)
	}
	// existing record, changed column -> excludes own pk
	mock4 := newMock()
	m4 := itemsModel(dbWith(mock4))
	m4.ValidatesUnique("name")
	ch := m4.Load(map[string]Value{"id": 3, "name": "old"})
	ch.Set("name", "new")
	if !ch.Valid() {
		t.Fatal("changed unique should be valid")
	}
	if mock4.last() != "SELECT 1 AS one FROM items WHERE ((name = 'new') AND (id != 3)) LIMIT 1" {
		t.Fatalf("unique-exclude sql = %q", mock4.last())
	}
}

func TestValidatesUniqueError(t *testing.T) {
	mock := newMock()
	mock.errAt = 1
	mock.errVal = errors.New("db down")
	m := itemsModel(dbWith(mock))
	m.ValidatesUnique("name")
	i := m.New("name", "y")
	if i.Valid() {
		t.Fatal("unique query error should invalidate")
	}
	if i.Errors().On("name")[0] != "could not be validated for uniqueness" {
		t.Fatalf("unique err msg = %v", i.Errors().On("name"))
	}
}

func TestErrorsObjectAndFailedSave(t *testing.T) {
	e := newErrors()
	if !e.Empty() {
		t.Fatal("new errors empty")
	}
	e.Add("a", "bad")
	e.Add("a", "worse")
	e.Add("b", "nope")
	if e.Count() != 3 {
		t.Fatalf("count = %d", e.Count())
	}
	want := []string{"a bad", "a worse", "b nope"}
	if !reflect.DeepEqual(e.FullMessages(), want) {
		t.Fatalf("full = %v", e.FullMessages())
	}
	// Save returns ValidationFailed
	m := itemsModel(dbWith(newMock()))
	m.ValidatesPresence("name")
	i := m.New("price", 1)
	err := i.Save()
	var vf *ValidationFailed
	if !errors.As(err, &vf) {
		t.Fatalf("expected ValidationFailed, got %T", err)
	}
	if vf.Error() == "" {
		t.Fatal("error string empty")
	}
}

func TestAddValidationCustom(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	m.AddValidation(func(i *Instance) {
		if i.Get("price") == 0 {
			i.Errors().Add("price", "must be positive")
		}
	})
	if m.New("price", 0).Valid() {
		t.Fatal("custom validation should fail")
	}
	if !m.New("price", 5).Valid() {
		t.Fatal("custom validation should pass")
	}
}

// ---- misc / panics ------------------------------------------------------

func TestModelPanics(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	assertPanic(t, "SetAll odd", func() { m.New("a") })
	assertPanic(t, "undefined dataset method", func() { m.DatasetMethod("nope") })
	assertPanic(t, "undefined association", func() { m.Load(map[string]Value{}).AssociationDataset("nope") })
}

func TestModelNameHelpers(t *testing.T) {
	m := itemsModel(dbWith(newMock())).SetName("Item")
	if m.Name() != "Item" || m.Table() != "items" {
		t.Fatalf("name/table = %q/%q", m.Name(), m.Table())
	}
	if !reflect.DeepEqual(m.Columns(), []string{"id", "name", "price"}) {
		t.Fatalf("columns = %v", m.Columns())
	}
	if !reflect.DeepEqual(m.PrimaryKey(), []string{"id"}) {
		t.Fatalf("pk = %v", m.PrimaryKey())
	}
}
