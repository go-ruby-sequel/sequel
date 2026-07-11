// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"errors"
	"testing"
)

// These tests close the remaining branches: the small accessors, the
// class-level Order/Limit convenience, the error paths inside Each/Destroy, the
// unreachable-in-practice association-type defaults, and the value-equality and
// whitespace helpers.

func TestModelDatasetAndValuesAccessors(t *testing.T) {
	m := itemsModel(dbWith(newMock()))
	if m.Dataset().SelectSQL() != "SELECT * FROM items" {
		t.Fatalf("Dataset() = %q", m.Dataset().SelectSQL())
	}
	i := m.New("name", "x")
	if v := i.Values(); v["name"] != "x" {
		t.Fatalf("Values() = %v", v)
	}
}

func TestModelClassOrderLimit(t *testing.T) {
	mock := newMock([]map[string]Value{{"id": 1}})
	m := itemsModel(dbWith(mock))
	md := m.Order("name").Limit(2)
	if md.SQL() != "SELECT * FROM items ORDER BY name LIMIT 2" {
		t.Fatalf("class order/limit sql = %q", md.SQL())
	}
	if m.Limit(4).SQL() != "SELECT * FROM items LIMIT 4" {
		t.Fatalf("class limit sql = %q", m.Limit(4).SQL())
	}
}

func TestEachAllError(t *testing.T) {
	mock := newMock()
	mock.always = true
	mock.errVal = errors.New("boom")
	m := itemsModel(dbWith(mock))
	if err := m.ModelDataset().Each(func(*Instance) error { return nil }); err == nil {
		t.Fatal("expected Each to propagate All error")
	}
}

func TestDestroyDeleteError(t *testing.T) {
	mock := newMock()
	mock.always = true
	mock.errVal = errors.New("boom")
	m := itemsModel(dbWith(mock))
	i := m.Load(map[string]Value{"id": 1})
	if err := i.Destroy(); err == nil {
		t.Fatal("expected destroy delete error")
	}
}

func TestValuesEqualByteMismatch(t *testing.T) {
	m := dbWith(newMock()).Model("t").SetColumns("d")
	// []byte vs non-[]byte on both sides, and length mismatch.
	i := m.Load(map[string]Value{"d": []byte("ab")})
	i.Set("d", "ab") // a=[]byte, b=string
	if !i.Modified() {
		t.Fatal("byte->string should modify")
	}
	j := m.Load(map[string]Value{"d": "ab"})
	j.Set("d", []byte("ab")) // a=string, b=[]byte
	if !j.Modified() {
		t.Fatal("string->byte should modify")
	}
	k := m.Load(map[string]Value{"d": []byte("ab")})
	k.Set("d", []byte("abc")) // length mismatch
	if !k.Modified() {
		t.Fatal("byte length mismatch should modify")
	}
}

func TestTrimSpaceBothEnds(t *testing.T) {
	m := dbWith(newMock()).Model("t").SetColumns("a")
	m.ValidatesPresence("a")
	// "  x  " trims both ends to "x": present -> valid (exercises both trim loops)
	if !m.New("a", "  x  ").Valid() {
		t.Fatal("padded content should be present")
	}
}

// TestDatasetForUnknownType and TestEagerLoadUnknownType exercise the defensive
// trailing returns for an association kind outside the four core ones (not
// reachable through the public constructors, which always set a valid kind).
func TestUnknownAssocTypeFallbacks(t *testing.T) {
	db := Connect("default", newMock())
	target := db.Model("targets").SetColumns("id")
	owner := db.Model("owners").SetColumns("id")
	bad := &association{name: "x", typ: AssocType(99), target: target, key: "k"}
	owner.assocs["x"] = bad
	inst := owner.Load(map[string]Value{"id": 1})
	if got := bad.datasetFor(inst).SelectSQL(); got != "SELECT * FROM targets" {
		t.Fatalf("unknown datasetFor = %q", got)
	}
	if err := owner.eagerLoad("x", []*Instance{inst}); err != nil {
		t.Fatalf("unknown eagerLoad = %v", err)
	}
}

func TestManyToManyJoinTableSwap(t *testing.T) {
	// owner table sorts after target: the default join-table name swaps them.
	db := Connect("default", newMock())
	owner := db.Model("zebras").SetColumns("id")
	target := db.Model("apples").SetColumns("id")
	owner.ManyToMany("apples", target)
	a := owner.assocs["apples"]
	if a.joinTable != "apples_zebras" {
		t.Fatalf("join table = %q, want apples_zebras", a.joinTable)
	}
}
