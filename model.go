// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"fmt"
	"sort"
)

// This file implements the Model (active-record) layer — the Go equivalent of
// Sequel::Model. A [ModelClass] is a subclass of Sequel::Model bound to a table
// or dataset; an [Instance] is a row object with dirty tracking, hooks,
// validations and associations. The layer is thin glue over the dataset/schema
// core (SQL generation) plus the injected [Executor] seam (row execution): the
// class builds the same SQL the gem's Model builds and maps the returned rows
// into instances.
//
// What is implemented: class definition over a table/dataset, per-instance CRUD
// (create/save/update/delete/destroy/refresh), dataset-backed finders
// ([]/with_pk/first/all/where/order/limit), the four core associations
// (one_to_many/many_to_one/one_to_one/many_to_many) with eager batch-loading and
// eager_graph, validations (presence/unique/format/length) with an errors
// object, before/after hooks (create/update/save/destroy/validation), dirty
// tracking (changed_columns/modified?), and named dataset methods
// (dataset_module). Plugins beyond this core (single_table_inheritance,
// timestamps, nested_attributes, dirty history, serialization, prepared
// statements, sharding, composite eager_graph column aliasing) are not included
// and are named in the README.

// ---- Executor capabilities ---------------------------------------------

// KeyExecutor is an optional capability an [Executor] may implement so a Model
// insert can learn the freshly inserted row's primary key. It mirrors how a
// Sequel adapter's dataset #insert returns the last insert id. When the wired
// executor does not implement it, [Instance.Save] on a new record runs the
// INSERT through [Executor.Execute] and expects the caller to have supplied the
// primary key explicitly (auto-increment recovery needs this seam).
type KeyExecutor interface {
	// ExecuteInsert runs an INSERT and returns the generated primary key value.
	ExecuteInsert(sql string) (Value, error)
}

// ---- ModelClass ---------------------------------------------------------

// AssocType names one of Sequel's four core association kinds.
type AssocType int

const (
	// OneToManyType is a has-many: the target rows carry a foreign key back to
	// this model's primary key.
	OneToManyType AssocType = iota
	// ManyToOneType is a belongs-to: this model carries the foreign key to the
	// target's primary key.
	ManyToOneType
	// OneToOneType is a has-one: like one_to_many but yielding a single row.
	OneToOneType
	// ManyToManyType is a has-and-belongs-to-many through a join table.
	ManyToManyType
)

// HookType names a model lifecycle hook position.
type HookType int

const (
	// BeforeValidation runs before validation.
	BeforeValidation HookType = iota
	// AfterValidation runs after validation.
	AfterValidation
	// BeforeSave runs before any insert or update.
	BeforeSave
	// AfterSave runs after any insert or update.
	AfterSave
	// BeforeCreate runs before an insert.
	BeforeCreate
	// AfterCreate runs after an insert.
	AfterCreate
	// BeforeUpdate runs before an update.
	BeforeUpdate
	// AfterUpdate runs after an update.
	AfterUpdate
	// BeforeDestroy runs before a destroy.
	BeforeDestroy
	// AfterDestroy runs after a destroy.
	AfterDestroy
)

// association is the resolved reflection for one declared association.
type association struct {
	name   string
	typ    AssocType
	target *ModelClass

	// key is the foreign-key column. For one_to_many/one_to_one it lives on the
	// target table; for many_to_one it lives on this model's table.
	key string

	// join-table wiring for many_to_many.
	joinTable string
	leftKey   string // join-table column referencing this model's pk
	rightKey  string // join-table column referencing the target's pk
}

// Hook is a lifecycle callback. Returning a non-nil error aborts the operation
// (the Go analogue of a Sequel hook returning false / raising HookFailed).
type Hook func(*Instance) error

// validation is one registered declarative validation.
type validation func(*Instance)

// ModelClass is a model definition bound to a table/dataset — the Go equivalent
// of a subclass of Sequel::Model. It is mutable at definition time (columns,
// associations, validations, hooks are registered onto it) and then used to
// build datasets and instantiate rows.
type ModelClass struct {
	db      *Database
	dataset *Dataset
	table   string
	name    string

	columns    []string
	primaryKey []string

	assocs         map[string]*association
	assocOrder     []string
	validations    []validation
	hooks          map[HookType][]Hook
	datasetMethods map[string]func(*Dataset) *Dataset

	// refreshOnCreate mirrors Sequel refreshing a row after INSERT to reload
	// server-side defaults. Enabled by default; disable when the executor cannot
	// serve the follow-up SELECT.
	refreshOnCreate bool
}

// Model defines a model class over a dataset — the Go form of
// Sequel::Model(DB[:items]). The dataset carries the database (and thus the
// dialect and executor). The primary key defaults to "id".
func Model(ds *Dataset) *ModelClass {
	m := &ModelClass{
		db:              ds.db,
		dataset:         ds,
		table:           ds.lastFromName(),
		primaryKey:      []string{"id"},
		assocs:          map[string]*association{},
		hooks:           map[HookType][]Hook{},
		datasetMethods:  map[string]func(*Dataset) *Dataset{},
		refreshOnCreate: true,
	}
	m.name = m.table
	return m
}

// Model defines a model class over a named table — the Go form of
// Sequel::Model(:items) using this database.
func (db *Database) Model(table string) *ModelClass { return Model(db.T(table)) }

// Table returns the model's table name.
func (m *ModelClass) Table() string { return m.table }

// Name returns the model's name (defaults to the table name); used only for
// error-message prefixes and diagnostics.
func (m *ModelClass) Name() string { return m.name }

// SetName overrides the model name.
func (m *ModelClass) SetName(name string) *ModelClass { m.name = name; return m }

// Dataset returns the class's base dataset — the Go form of Model.dataset.
func (m *ModelClass) Dataset() *Dataset { return m.dataset }

// Columns returns the model's known columns.
func (m *ModelClass) Columns() []string { return m.columns }

// SetColumns declares the model's columns. Mirrors setting a model's columns in
// Sequel specs so no live schema introspection is needed.
func (m *ModelClass) SetColumns(cols ...string) *ModelClass { m.columns = cols; return m }

// PrimaryKey returns the primary-key column(s).
func (m *ModelClass) PrimaryKey() []string { return m.primaryKey }

// SetPrimaryKey sets the primary-key column(s) — Sequel's set_primary_key.
func (m *ModelClass) SetPrimaryKey(cols ...string) *ModelClass { m.primaryKey = cols; return m }

// ---- Instance -----------------------------------------------------------

// Instance is a model row object — the Go equivalent of a Sequel::Model
// instance. It holds the row values, tracks which columns changed since load,
// records whether it is a new (unsaved) record, and carries the validation
// errors and association cache.
type Instance struct {
	class   *ModelClass
	values  map[string]Value
	order   []string // column set order (drives INSERT/UPDATE column order)
	changed map[string]bool
	isNew   bool
	errs    *Errors
	assoc   map[string]any
}

// New builds a new (unsaved) instance from alternating column/value pairs, in
// order — the Go form of Model.new(col: v, ...).
func (m *ModelClass) New(pairs ...Value) *Instance {
	inst := m.newEmpty(true)
	inst.SetAll(pairs...)
	return inst
}

// Load builds an existing (persisted) instance directly from a row, marking it
// not-new and clean — the Go form of Model.load(row).
func (m *ModelClass) Load(row map[string]Value) *Instance {
	inst := m.newEmpty(false)
	// Preserve a deterministic column order: declared columns first, then any
	// extra keys sorted, so INSERT/UPDATE/diagnostics are stable.
	for _, c := range m.columns {
		if v, ok := row[c]; ok {
			inst.values[c] = v
			inst.order = append(inst.order, c)
		}
	}
	extra := make([]string, 0)
	for k := range row {
		if _, ok := inst.values[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		inst.values[k] = row[k]
		inst.order = append(inst.order, k)
	}
	return inst
}

func (m *ModelClass) newEmpty(isNew bool) *Instance {
	return &Instance{
		class:   m,
		values:  map[string]Value{},
		changed: map[string]bool{},
		isNew:   isNew,
		errs:    newErrors(),
		assoc:   map[string]any{},
	}
}

// Get returns the value of a column.
func (i *Instance) Get(col string) Value { return i.values[col] }

// Values returns the instance's column values (the live map — do not mutate
// directly; use Set).
func (i *Instance) Values() map[string]Value { return i.values }

// Set assigns a column, marking it changed (dirty) unless the value is
// identical to the current one — mirroring Sequel only flagging real changes.
func (i *Instance) Set(col string, v Value) *Instance {
	if cur, ok := i.values[col]; ok {
		if valuesEqual(cur, v) {
			return i
		}
	} else {
		i.order = append(i.order, col)
	}
	i.values[col] = v
	i.changed[col] = true
	return i
}

// SetAll assigns several columns from alternating column/value pairs.
func (i *Instance) SetAll(pairs ...Value) *Instance {
	if len(pairs)%2 != 0 {
		panic("sequel: Instance.SetAll requires an even number of arguments")
	}
	for k := 0; k < len(pairs); k += 2 {
		i.Set(pairs[k].(string), pairs[k+1])
	}
	return i
}

// PK returns the primary-key value (or a []Value for a composite key).
func (i *Instance) PK() Value {
	if len(i.class.primaryKey) == 1 {
		return i.values[i.class.primaryKey[0]]
	}
	pk := make([]Value, len(i.class.primaryKey))
	for k, c := range i.class.primaryKey {
		pk[k] = i.values[c]
	}
	return pk
}

// IsNew reports whether the instance is a new (unsaved) record.
func (i *Instance) IsNew() bool { return i.isNew }

// ChangedColumns returns the columns modified since load/save, in set order —
// Sequel's changed_columns.
func (i *Instance) ChangedColumns() []string {
	out := make([]string, 0, len(i.changed))
	for _, c := range i.order {
		if i.changed[c] {
			out = append(out, c)
		}
	}
	return out
}

// Modified reports whether any column changed since load/save — Sequel's
// modified?.
func (i *Instance) Modified() bool { return len(i.changed) > 0 }

// Errors returns the instance's validation errors (populated by Valid).
func (i *Instance) Errors() *Errors { return i.errs }

// ---- CRUD ---------------------------------------------------------------

// Create builds and saves a new instance in one call — Model.create(col: v).
func (m *ModelClass) Create(pairs ...Value) (*Instance, error) {
	inst := m.New(pairs...)
	if err := inst.Save(); err != nil {
		return nil, err
	}
	return inst, nil
}

// Save persists the instance: an INSERT for a new record, or an UPDATE of the
// changed columns for an existing one. It runs validation and the save/create
// or save/update hooks in Sequel's order. A new record with no columns set, or
// an existing record with no changes, performs no DML.
func (i *Instance) Save() error {
	if !i.Valid() {
		return &ValidationFailed{Errors: i.errs}
	}
	if err := i.runHooks(BeforeSave); err != nil {
		return err
	}
	if i.isNew {
		if err := i.insert(); err != nil {
			return err
		}
	} else {
		if err := i.update(); err != nil {
			return err
		}
	}
	return i.runHooks(AfterSave)
}

func (i *Instance) insert() error {
	if err := i.runHooks(BeforeCreate); err != nil {
		return err
	}
	sql := i.insertSQL()
	if ke, ok := i.executor().(KeyExecutor); ok {
		pk, err := ke.ExecuteInsert(sql)
		if err != nil {
			return err
		}
		if len(i.class.primaryKey) == 1 && i.values[i.class.primaryKey[0]] == nil {
			i.setLoaded(i.class.primaryKey[0], pk)
		}
	} else {
		if _, err := i.class.db.Run(sql); err != nil {
			return err
		}
	}
	i.isNew = false
	i.changed = map[string]bool{}
	if err := i.runHooks(AfterCreate); err != nil {
		return err
	}
	if i.class.refreshOnCreate && i.executor() != nil {
		if err := i.Refresh(); err != nil {
			return err
		}
	}
	return nil
}

func (i *Instance) update() error {
	changed := i.ChangedColumns()
	if len(changed) == 0 {
		return nil
	}
	if err := i.runHooks(BeforeUpdate); err != nil {
		return err
	}
	if _, err := i.class.db.Run(i.updateSQL()); err != nil {
		return err
	}
	i.changed = map[string]bool{}
	return i.runHooks(AfterUpdate)
}

// Update assigns the given columns and saves — Sequel's instance update.
func (i *Instance) Update(pairs ...Value) error {
	i.SetAll(pairs...)
	return i.Save()
}

// Delete removes the row with a DELETE keyed on the primary key. It runs no
// hooks — Sequel's Model#delete.
func (i *Instance) Delete() error {
	_, err := i.class.db.Run(i.pkDataset().DeleteSQL())
	return err
}

// Destroy removes the row, running the before/after destroy hooks —
// Sequel's Model#destroy.
func (i *Instance) Destroy() error {
	if err := i.runHooks(BeforeDestroy); err != nil {
		return err
	}
	if err := i.Delete(); err != nil {
		return err
	}
	return i.runHooks(AfterDestroy)
}

// Refresh reloads the instance's columns from the database by primary key,
// clearing dirty state — Sequel's Model#refresh.
func (i *Instance) Refresh() error {
	rows, err := i.class.db.Run(i.pkDataset().Limit(1).SelectSQL())
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	i.assoc = map[string]any{}
	for k, v := range rows[0] {
		i.setLoaded(k, v)
	}
	i.changed = map[string]bool{}
	return nil
}

// setLoaded assigns a column without marking it dirty (used by insert/refresh).
func (i *Instance) setLoaded(col string, v Value) {
	if _, ok := i.values[col]; !ok {
		i.order = append(i.order, col)
	}
	i.values[col] = v
}

func (i *Instance) insertSQL() string {
	kv := make([]Value, 0, len(i.order)*2)
	for _, c := range i.order {
		kv = append(kv, c, i.values[c])
	}
	return i.class.dataset.InsertSQL(kv...)
}

func (i *Instance) updateSQL() string {
	kv := make([]Value, 0)
	for _, c := range i.ChangedColumns() {
		kv = append(kv, c, i.values[c])
	}
	return i.pkDataset().UpdateSQL(kv...)
}

// pkDataset returns the class dataset filtered to this instance's primary key.
func (i *Instance) pkDataset() *Dataset {
	pairs := make([]Value, 0, len(i.class.primaryKey)*2)
	for _, c := range i.class.primaryKey {
		pairs = append(pairs, c, i.values[c])
	}
	return i.class.dataset.Where(H(pairs...))
}

func (i *Instance) executor() Executor { return i.class.db.executor }

func (i *Instance) runHooks(t HookType) error {
	for _, h := range i.class.hooks[t] {
		if err := h(i); err != nil {
			return err
		}
	}
	return nil
}

// ---- Finders ------------------------------------------------------------

// WithPK loads the row with the given primary key, or returns (nil, nil) when
// absent — Sequel's Model.with_pk / Model[pk].
func (m *ModelClass) WithPK(pk Value) (*Instance, error) {
	ds := m.withPKDataset(pk).Limit(1)
	rows, err := m.db.Run(ds.SelectSQL())
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return m.Load(rows[0]), nil
}

// Get is an alias for WithPK — the Go form of Model[pk].
func (m *ModelClass) Get(pk Value) (*Instance, error) { return m.WithPK(pk) }

// withPKDataset returns the class dataset filtered to a primary-key value. A
// []Value pk is matched component-wise against a composite key.
func (m *ModelClass) withPKDataset(pk Value) *Dataset {
	if len(m.primaryKey) == 1 {
		return m.dataset.Where(H(m.primaryKey[0], pk))
	}
	vals, _ := pk.([]Value)
	pairs := make([]Value, 0, len(m.primaryKey)*2)
	for k, c := range m.primaryKey {
		var v Value
		if k < len(vals) {
			v = vals[k]
		}
		pairs = append(pairs, c, v)
	}
	return m.dataset.Where(H(pairs...))
}

// First returns the first row, or nil — Sequel's Model.first.
func (m *ModelClass) First() (*Instance, error) { return m.ModelDataset().First() }

// All returns every row as instances — Sequel's Model.all.
func (m *ModelClass) All() ([]*Instance, error) { return m.ModelDataset().All() }

// Where returns a model dataset filtered by cond — Sequel's Model.where.
func (m *ModelClass) Where(cond Value) *ModelDataset { return m.ModelDataset().Where(cond) }

// Order returns a model dataset ordered by the given terms — Model.order.
func (m *ModelClass) Order(terms ...Value) *ModelDataset { return m.ModelDataset().Order(terms...) }

// Limit returns a model dataset limited to n rows — Model.limit.
func (m *ModelClass) Limit(n int) *ModelDataset { return m.ModelDataset().Limit(n) }

// Eager returns a model dataset that will eager-load the named associations —
// Model.eager.
func (m *ModelClass) Eager(names ...string) *ModelDataset { return m.ModelDataset().Eager(names...) }

// EagerGraph returns a model dataset that will eager-load via a LEFT OUTER JOIN
// — Model.eager_graph.
func (m *ModelClass) EagerGraph(names ...string) *ModelDataset {
	return m.ModelDataset().EagerGraph(names...)
}

// ---- ModelDataset -------------------------------------------------------

// ModelDataset is a dataset whose rows are materialised as model instances —
// the Go equivalent of a Sequel model dataset. Filtering/ordering methods return
// a new ModelDataset; All/First/Each execute and yield instances.
type ModelDataset struct {
	class      *ModelClass
	ds         *Dataset
	eager      []string
	eagerGraph []string
}

// ModelDataset returns the class's base model dataset.
func (m *ModelClass) ModelDataset() *ModelDataset {
	return &ModelDataset{class: m, ds: m.dataset}
}

// SQL returns the SELECT SQL of the underlying dataset.
func (md *ModelDataset) SQL() string { return md.ds.SelectSQL() }

// Dataset returns the underlying plain dataset.
func (md *ModelDataset) Dataset() *Dataset { return md.ds }

func (md *ModelDataset) with(ds *Dataset) *ModelDataset {
	nd := *md
	nd.ds = ds
	return &nd
}

// Where filters the model dataset.
func (md *ModelDataset) Where(cond Value) *ModelDataset { return md.with(md.ds.Where(cond)) }

// Exclude negates a filter on the model dataset.
func (md *ModelDataset) Exclude(cond Value) *ModelDataset { return md.with(md.ds.Exclude(cond)) }

// Order orders the model dataset.
func (md *ModelDataset) Order(terms ...Value) *ModelDataset { return md.with(md.ds.Order(terms...)) }

// Limit limits the model dataset.
func (md *ModelDataset) Limit(n int) *ModelDataset { return md.with(md.ds.Limit(n)) }

// Eager marks associations for batch eager loading.
func (md *ModelDataset) Eager(names ...string) *ModelDataset {
	nd := *md
	nd.eager = append(append([]string(nil), md.eager...), names...)
	return &nd
}

// EagerGraph marks associations for LEFT OUTER JOIN eager loading.
func (md *ModelDataset) EagerGraph(names ...string) *ModelDataset {
	nd := *md
	nd.eagerGraph = append(append([]string(nil), md.eagerGraph...), names...)
	return &nd
}

// Named applies a named dataset method registered via DatasetModule/Def.
func (md *ModelDataset) Named(name string) *ModelDataset {
	fn, ok := md.class.datasetMethods[name]
	if !ok {
		panic(fmt.Sprintf("sequel: undefined dataset method %q", name))
	}
	return md.with(fn(md.ds))
}

// All executes the dataset and returns instances, applying any eager loading.
func (md *ModelDataset) All() ([]*Instance, error) {
	if len(md.eagerGraph) > 0 {
		return md.allEagerGraph()
	}
	rows, err := md.class.db.Run(md.ds.SelectSQL())
	if err != nil {
		return nil, err
	}
	insts := make([]*Instance, len(rows))
	for k, r := range rows {
		insts[k] = md.class.Load(r)
	}
	for _, name := range md.eager {
		if err := md.class.eagerLoad(name, insts); err != nil {
			return nil, err
		}
	}
	return insts, nil
}

// First executes the dataset with LIMIT 1 and returns the first instance or nil.
func (md *ModelDataset) First() (*Instance, error) {
	rows, err := md.class.db.Run(md.ds.Limit(1).SelectSQL())
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return md.class.Load(rows[0]), nil
}

// Each executes the dataset and calls fn for each instance until fn errors.
func (md *ModelDataset) Each(fn func(*Instance) error) error {
	insts, err := md.All()
	if err != nil {
		return err
	}
	for _, inst := range insts {
		if err := fn(inst); err != nil {
			return err
		}
	}
	return nil
}

// ---- Named dataset methods (dataset_module) -----------------------------

// DatasetModule registers named dataset methods in one call — the Go form of
// dataset_module { def young; where{...}; end }. Each entry maps a name to a
// dataset transform. Invoke a registered method via Model.DatasetMethod(name) or
// modelDataset.Named(name).
func (m *ModelClass) DatasetModule(methods map[string]func(*Dataset) *Dataset) *ModelClass {
	for name, fn := range methods {
		m.datasetMethods[name] = fn
	}
	return m
}

// Def registers a single named dataset method — a convenience over
// DatasetModule.
func (m *ModelClass) Def(name string, fn func(*Dataset) *Dataset) *ModelClass {
	m.datasetMethods[name] = fn
	return m
}

// DatasetMethod applies a registered named dataset method to the base dataset.
func (m *ModelClass) DatasetMethod(name string) *ModelDataset {
	return m.ModelDataset().Named(name)
}

// ---- Hooks --------------------------------------------------------------

// AddHook registers a lifecycle hook at the given position.
func (m *ModelClass) AddHook(t HookType, h Hook) *ModelClass {
	m.hooks[t] = append(m.hooks[t], h)
	return m
}

// BeforeCreate registers a before-create hook.
func (m *ModelClass) BeforeCreate(h Hook) *ModelClass { return m.AddHook(BeforeCreate, h) }

// AfterCreate registers an after-create hook.
func (m *ModelClass) AfterCreate(h Hook) *ModelClass { return m.AddHook(AfterCreate, h) }

// BeforeUpdate registers a before-update hook.
func (m *ModelClass) BeforeUpdate(h Hook) *ModelClass { return m.AddHook(BeforeUpdate, h) }

// AfterUpdate registers an after-update hook.
func (m *ModelClass) AfterUpdate(h Hook) *ModelClass { return m.AddHook(AfterUpdate, h) }

// BeforeSave registers a before-save hook.
func (m *ModelClass) BeforeSave(h Hook) *ModelClass { return m.AddHook(BeforeSave, h) }

// AfterSave registers an after-save hook.
func (m *ModelClass) AfterSave(h Hook) *ModelClass { return m.AddHook(AfterSave, h) }

// BeforeDestroy registers a before-destroy hook.
func (m *ModelClass) BeforeDestroy(h Hook) *ModelClass { return m.AddHook(BeforeDestroy, h) }

// AfterDestroy registers an after-destroy hook.
func (m *ModelClass) AfterDestroy(h Hook) *ModelClass { return m.AddHook(AfterDestroy, h) }

// BeforeValidation registers a before-validation hook.
func (m *ModelClass) BeforeValidation(h Hook) *ModelClass { return m.AddHook(BeforeValidation, h) }

// AfterValidation registers an after-validation hook.
func (m *ModelClass) AfterValidation(h Hook) *ModelClass { return m.AddHook(AfterValidation, h) }
