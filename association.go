// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import "strings"

// This file implements the four core Sequel associations
// (one_to_many/many_to_one/one_to_one/many_to_many): their reflection, the
// per-instance association datasets (Sequel's <name>_dataset), lazy accessors,
// add/remove modifiers, and both eager strategies (batch eager and
// eager_graph). The generated SQL mirrors what the gem builds — the association
// dataset qualifies the foreign key against the owning table exactly as Sequel
// does.

// AssocOption configures a declared association.
type AssocOption func(*association)

// Key sets the foreign-key column for a one_to_many/one_to_one (on the target)
// or many_to_one (on this model). Defaults follow Sequel's conventions when
// unset.
func Key(col string) AssocOption { return func(a *association) { a.key = col } }

// JoinTable sets the many_to_many join table.
func JoinTable(t string) AssocOption { return func(a *association) { a.joinTable = t } }

// LeftKey sets the many_to_many join-table column referencing this model.
func LeftKey(col string) AssocOption { return func(a *association) { a.leftKey = col } }

// RightKey sets the many_to_many join-table column referencing the target.
func RightKey(col string) AssocOption { return func(a *association) { a.rightKey = col } }

// OneToMany declares a has-many association: the target rows carry a foreign key
// (default "<thismodel>_id") back to this model's primary key.
func (m *ModelClass) OneToMany(name string, target *ModelClass, opts ...AssocOption) *ModelClass {
	return m.addAssoc(name, OneToManyType, target, opts)
}

// ManyToOne declares a belongs-to association: this model carries the foreign
// key (default "<name>_id") to the target's primary key.
func (m *ModelClass) ManyToOne(name string, target *ModelClass, opts ...AssocOption) *ModelClass {
	return m.addAssoc(name, ManyToOneType, target, opts)
}

// OneToOne declares a has-one association: like one_to_many but a single row.
func (m *ModelClass) OneToOne(name string, target *ModelClass, opts ...AssocOption) *ModelClass {
	return m.addAssoc(name, OneToOneType, target, opts)
}

// ManyToMany declares a has-and-belongs-to-many association through a join
// table. Defaults: join table = the two table names joined by "_" in sorted
// order, left key = "<thismodel>_id", right key = "<target>_id".
func (m *ModelClass) ManyToMany(name string, target *ModelClass, opts ...AssocOption) *ModelClass {
	return m.addAssoc(name, ManyToManyType, target, opts)
}

func (m *ModelClass) addAssoc(name string, typ AssocType, target *ModelClass, opts []AssocOption) *ModelClass {
	a := &association{name: name, typ: typ, target: target}
	for _, o := range opts {
		o(a)
	}
	a.applyDefaults(m)
	m.assocs[name] = a
	m.assocOrder = append(m.assocOrder, name)
	return m
}

// applyDefaults fills the conventional keys Sequel would infer.
func (a *association) applyDefaults(owner *ModelClass) {
	switch a.typ {
	case ManyToOneType:
		if a.key == "" {
			a.key = a.name + "_id"
		}
	case OneToManyType, OneToOneType:
		if a.key == "" {
			a.key = singular(owner.table) + "_id"
		}
	case ManyToManyType:
		if a.leftKey == "" {
			a.leftKey = singular(owner.table) + "_id"
		}
		if a.rightKey == "" {
			a.rightKey = singular(a.target.table) + "_id"
		}
		if a.joinTable == "" {
			names := []string{owner.table, a.target.table}
			if names[0] > names[1] {
				names[0], names[1] = names[1], names[0]
			}
			a.joinTable = names[0] + "_" + names[1]
		}
	}
}

// singular is a minimal English singulariser sufficient for FK-name inference
// ("artists" -> "artist"). It is deliberately simple; pass an explicit Key when
// a table name does not follow the trailing-"s" convention.
func singular(table string) string {
	if strings.HasSuffix(table, "s") {
		return table[:len(table)-1]
	}
	return table
}

// targetPK returns the target model's single primary-key column.
func (a *association) targetPK() string { return a.target.primaryKey[0] }

// ---- Per-instance association datasets ----------------------------------

// AssociationDataset returns the dataset for a declared association on this
// instance — the Go form of Sequel's <name>_dataset. The generated SQL matches
// the gem's byte-for-byte.
func (i *Instance) AssociationDataset(name string) *Dataset {
	a := i.class.mustAssoc(name)
	return a.datasetFor(i)
}

func (m *ModelClass) mustAssoc(name string) *association {
	a, ok := m.assocs[name]
	if !ok {
		panic("sequel: undefined association " + name)
	}
	return a
}

func (a *association) datasetFor(i *Instance) *Dataset {
	td := a.target.dataset
	switch a.typ {
	case OneToManyType:
		return td.Where(H(Qualify(a.target.table, a.key), i.PK()))
	case OneToOneType:
		return td.Where(H(Qualify(a.target.table, a.key), i.PK())).Limit(1)
	case ManyToOneType:
		return td.Where(H(Qualify(a.target.table, a.targetPK()), i.values[a.key])).Limit(1)
	case ManyToManyType:
		return td.
			Select(qualifiedStar{a.target.table}).
			Join(a.joinTable, Cmp("=", Qualify(a.joinTable, a.rightKey), Qualify(a.target.table, a.targetPK()))).
			Where(H(Qualify(a.joinTable, a.leftKey), i.PK()))
	}
	return td
}

// qualifiedStar renders "table.*" (dialect-quoted) for a many_to_many SELECT.
type qualifiedStar struct{ table string }

func (q qualifiedStar) litSQL(b *builder) {
	b.WriteString(b.dialect.quoteIdentifier(q.table))
	b.WriteString(".*")
}

// ---- Lazy accessors -----------------------------------------------------

// Related returns the associated instances for a to-many association (or the
// single-element/empty slice for a to-one), loading and caching on first use —
// the Go form of Sequel's association accessor.
func (i *Instance) Related(name string) ([]*Instance, error) {
	if cached, ok := i.assoc[name]; ok {
		return cached.([]*Instance), nil
	}
	a := i.class.mustAssoc(name)
	rows, err := a.target.db.Run(a.datasetFor(i).SelectSQL())
	if err != nil {
		return nil, err
	}
	insts := make([]*Instance, len(rows))
	for k, r := range rows {
		insts[k] = a.target.Load(r)
	}
	i.assoc[name] = insts
	return insts, nil
}

// RelatedOne returns the single associated instance for a many_to_one/one_to_one
// association (or nil), loading and caching on first use.
func (i *Instance) RelatedOne(name string) (*Instance, error) {
	insts, err := i.Related(name)
	if err != nil {
		return nil, err
	}
	if len(insts) == 0 {
		return nil, nil
	}
	return insts[0], nil
}

// ---- Modifiers ----------------------------------------------------------

// Add associates another instance with this one — Sequel's add_<name>. For
// one_to_many/one_to_one it sets the target's foreign key to this row's pk and
// saves the target; for many_to_many it inserts a join-table row.
func (i *Instance) Add(name string, other *Instance) error {
	a := i.class.mustAssoc(name)
	switch a.typ {
	case OneToManyType, OneToOneType:
		other.Set(a.key, i.PK())
		if err := other.Save(); err != nil {
			return err
		}
	case ManyToManyType:
		sql := i.class.db.T(a.joinTable).InsertSQL(a.leftKey, i.PK(), a.rightKey, other.PK())
		if _, err := i.class.db.Run(sql); err != nil {
			return err
		}
	default:
		panic("sequel: Add is not defined for a many_to_one association")
	}
	delete(i.assoc, name)
	return nil
}

// Remove dissociates another instance — Sequel's remove_<name>. For
// one_to_many/one_to_one it nils the target's foreign key and saves; for
// many_to_many it deletes the join-table row.
func (i *Instance) Remove(name string, other *Instance) error {
	a := i.class.mustAssoc(name)
	switch a.typ {
	case OneToManyType, OneToOneType:
		other.Set(a.key, nil)
		if err := other.Save(); err != nil {
			return err
		}
	case ManyToManyType:
		ds := i.class.db.T(a.joinTable).Where(H(a.leftKey, i.PK(), a.rightKey, other.PK()))
		if _, err := i.class.db.Run(ds.DeleteSQL()); err != nil {
			return err
		}
	default:
		panic("sequel: Remove is not defined for a many_to_one association")
	}
	delete(i.assoc, name)
	return nil
}

// ---- Eager batch loading ------------------------------------------------

// eagerLoad batch-loads one association across a set of already-loaded parent
// instances, populating each parent's association cache with a single extra
// query using IN (...), exactly as Sequel's eager does.
func (m *ModelClass) eagerLoad(name string, parents []*Instance) error {
	a := m.mustAssoc(name)
	if len(parents) == 0 {
		return nil
	}
	switch a.typ {
	case OneToManyType, OneToOneType:
		return a.eagerToMany(parents)
	case ManyToOneType:
		return a.eagerManyToOne(parents)
	case ManyToManyType:
		return a.eagerManyToMany(parents)
	}
	return nil
}

func (a *association) eagerToMany(parents []*Instance) error {
	ids, byID := parentKeys(parents, parents[0].class.primaryKey[0])
	ds := a.target.dataset.Where(H(Qualify(a.target.table, a.key), listVal(ids)))
	rows, err := a.target.db.Run(ds.SelectSQL())
	if err != nil {
		return err
	}
	groups := map[string][]*Instance{}
	for _, r := range rows {
		child := a.target.Load(r)
		groups[keyStr(r[a.key])] = append(groups[keyStr(r[a.key])], child)
	}
	single := a.typ == OneToOneType
	for id, ps := range byID {
		for _, p := range ps {
			children := groups[id]
			if single {
				if len(children) > 0 {
					p.assoc[a.name] = []*Instance{children[0]}
				} else {
					p.assoc[a.name] = []*Instance{}
				}
				continue
			}
			p.assoc[a.name] = children
		}
	}
	return nil
}

func (a *association) eagerManyToOne(parents []*Instance) error {
	ids, _ := parentKeys(parents, a.key)
	ds := a.target.dataset.Where(H(Qualify(a.target.table, a.targetPK()), listVal(ids)))
	rows, err := a.target.db.Run(ds.SelectSQL())
	if err != nil {
		return err
	}
	byPK := map[string]*Instance{}
	for _, r := range rows {
		byPK[keyStr(r[a.targetPK()])] = a.target.Load(r)
	}
	for _, p := range parents {
		if t, ok := byPK[keyStr(p.values[a.key])]; ok {
			p.assoc[a.name] = []*Instance{t}
		} else {
			p.assoc[a.name] = []*Instance{}
		}
	}
	return nil
}

func (a *association) eagerManyToMany(parents []*Instance) error {
	pk := parents[0].class.primaryKey[0]
	ids, byID := parentKeys(parents, pk)
	ds := a.target.dataset.
		Select(qualifiedStar{a.target.table}, As(Qualify(a.joinTable, a.leftKey), foreignKeyAlias)).
		Join(a.joinTable, Cmp("=", Qualify(a.joinTable, a.rightKey), Qualify(a.target.table, a.targetPK()))).
		Where(H(Qualify(a.joinTable, a.leftKey), listVal(ids)))
	rows, err := a.target.db.Run(ds.SelectSQL())
	if err != nil {
		return err
	}
	groups := map[string][]*Instance{}
	for _, r := range rows {
		owner := keyStr(r[foreignKeyAlias])
		clean := map[string]Value{}
		for k, v := range r {
			if k != foreignKeyAlias {
				clean[k] = v
			}
		}
		groups[owner] = append(groups[owner], a.target.Load(clean))
	}
	for id, ps := range byID {
		for _, p := range ps {
			p.assoc[a.name] = groups[id]
		}
	}
	return nil
}

// foreignKeyAlias is the column alias Sequel uses to carry the owning key back
// through a many_to_many eager query.
const foreignKeyAlias = "x_foreign_key_x"

// parentKeys returns the distinct non-nil values of col across parents and an
// index from each value's string form to the parents holding it.
func parentKeys(parents []*Instance, col string) ([]Value, map[string][]*Instance) {
	var ids []Value
	byID := map[string][]*Instance{}
	seen := map[string]bool{}
	for _, p := range parents {
		v := p.values[col]
		if v == nil {
			continue
		}
		s := keyStr(v)
		if !seen[s] {
			seen[s] = true
			ids = append(ids, v)
		}
		byID[s] = append(byID[s], p)
	}
	return ids, byID
}

func listVal(ids []Value) []Value { return ids }

// ---- eager_graph --------------------------------------------------------

// allEagerGraph loads the base rows and their associations in a single LEFT
// OUTER JOIN query, splitting each row into the base instance and its associated
// instances. The JOIN semantics mirror Sequel's eager_graph; the per-column
// aliasing uses a deterministic "<assoc>_<column>" scheme (Sequel's internal
// alias scheme differs, so the emitted SQL text is not byte-identical, but the
// join structure and row-splitting behaviour are).
func (md *ModelDataset) allEagerGraph() ([]*Instance, error) {
	sql, assocs := md.eagerGraphSQL()
	rows, err := md.class.db.Run(sql)
	if err != nil {
		return nil, err
	}
	order := []string{}
	byPK := map[string]*Instance{}
	pk := md.class.primaryKey[0]
	for _, r := range rows {
		base := extractBase(r, md.class)
		id := keyStr(base[pk])
		parent, ok := byPK[id]
		if !ok {
			parent = md.class.Load(base)
			byPK[id] = parent
			order = append(order, id)
			for _, a := range assocs {
				if a.typ == OneToManyType || a.typ == ManyToManyType {
					parent.assoc[a.name] = []*Instance{}
				}
			}
		}
		for _, a := range assocs {
			sub, present := extractAssoc(r, a)
			if !present {
				continue
			}
			child := a.target.Load(sub)
			switch a.typ {
			case ManyToOneType, OneToOneType:
				parent.assoc[a.name] = []*Instance{child}
			default:
				parent.assoc[a.name] = append(parent.assoc[a.name].([]*Instance), child)
			}
		}
	}
	out := make([]*Instance, len(order))
	for k, id := range order {
		out[k] = byPK[id]
	}
	return out, nil
}

// eagerGraphSQL builds the LEFT OUTER JOIN SELECT and returns it with the
// resolved associations, in declaration order.
func (md *ModelDataset) eagerGraphSQL() (string, []*association) {
	d := md.class.dataset.dialect()
	b := newBuilder(d)
	assocs := make([]*association, 0, len(md.eagerGraph))
	for _, n := range md.eagerGraph {
		assocs = append(assocs, md.class.mustAssoc(n))
	}
	b.WriteString("SELECT ")
	first := true
	writeCol := func(e Expr) {
		if !first {
			b.WriteString(", ")
		}
		first = false
		e.litSQL(b)
	}
	for _, c := range md.class.columns {
		writeCol(Qualify(md.class.table, c))
	}
	for _, a := range assocs {
		for _, c := range a.target.columns {
			writeCol(As(Qualify(a.target.table, c), a.name+"_"+c))
		}
	}
	b.WriteString(" FROM ")
	b.WriteString(d.quoteIdentifier(md.class.table))
	for _, a := range assocs {
		b.WriteString(" LEFT OUTER JOIN ")
		b.WriteString(d.quoteIdentifier(a.target.table))
		b.WriteString(" ON ")
		a.graphOn(md.class).litSQL(b)
	}
	// Preserve any WHERE/ORDER the caller layered on before graphing.
	md.ds.appendWhere(b)
	md.ds.appendOrder(b)
	return b.String(), assocs
}

// graphOn returns the LEFT OUTER JOIN ON condition for an eager_graph edge.
func (a *association) graphOn(owner *ModelClass) Expr {
	switch a.typ {
	case ManyToOneType:
		return Cmp("=", Qualify(a.target.table, a.targetPK()), Qualify(owner.table, a.key))
	default: // one_to_many / one_to_one
		return Cmp("=", Qualify(a.target.table, a.key), Qualify(owner.table, owner.primaryKey[0]))
	}
}

// extractBase pulls the base-table columns out of a graphed row.
func extractBase(row map[string]Value, m *ModelClass) map[string]Value {
	out := map[string]Value{}
	for _, c := range m.columns {
		if v, ok := row[c]; ok {
			out[c] = v
		}
	}
	return out
}

// extractAssoc pulls one association's aliased columns out of a graphed row,
// stripping the "<assoc>_" prefix. It reports absent when the association's
// primary key came back NULL (an unmatched LEFT JOIN row).
func extractAssoc(row map[string]Value, a *association) (map[string]Value, bool) {
	out := map[string]Value{}
	for _, c := range a.target.columns {
		if v, ok := row[a.name+"_"+c]; ok {
			out[c] = v
		}
	}
	if v, ok := out[a.targetPK()]; !ok || v == nil {
		return nil, false
	}
	return out, true
}
