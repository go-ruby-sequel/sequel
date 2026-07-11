// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

// This file implements the model validation layer (the Go equivalent of
// Sequel's validation_helpers plugin) and the errors object. The declarative
// validators — presence, unique, format, length — register onto the class and
// run during Valid, populating the instance's [Errors] with the same default
// messages the gem uses ("is not present", "is already taken", "is invalid",
// the length messages). validates_unique runs the same "SELECT 1 AS one FROM t
// WHERE ... LIMIT 1" existence probe the gem does.

// Errors collects validation failures keyed by column, in insertion order —
// the Go equivalent of Sequel::Model::Errors.
type Errors struct {
	byCol map[string][]string
	order []string
}

func newErrors() *Errors { return &Errors{byCol: map[string][]string{}} }

// Add records a message against a column.
func (e *Errors) Add(col, msg string) {
	if _, ok := e.byCol[col]; !ok {
		e.order = append(e.order, col)
	}
	e.byCol[col] = append(e.byCol[col], msg)
}

// On returns the messages for a column (nil when none) — Sequel's errors.on.
func (e *Errors) On(col string) []string { return e.byCol[col] }

// Empty reports whether there are no errors.
func (e *Errors) Empty() bool { return len(e.byCol) == 0 }

// Count returns the total number of error messages.
func (e *Errors) Count() int {
	n := 0
	for _, msgs := range e.byCol {
		n += len(msgs)
	}
	return n
}

// FullMessages returns "<column> <message>" for every error, in column then
// message order — Sequel's errors.full_messages.
func (e *Errors) FullMessages() []string {
	out := make([]string, 0, e.Count())
	for _, col := range e.order {
		for _, msg := range e.byCol[col] {
			out = append(out, col+" "+msg)
		}
	}
	return out
}

func (e *Errors) clear() {
	e.byCol = map[string][]string{}
	e.order = nil
}

// ValidationFailed is returned by Save when validation fails.
type ValidationFailed struct{ Errors *Errors }

func (v *ValidationFailed) Error() string {
	return "sequel: validation failed: " + fmt.Sprint(v.Errors.FullMessages())
}

// ---- Declarative validators ---------------------------------------------

// ValidatesPresence requires each named column to be non-nil and non-empty —
// Sequel's validates_presence. Message: "is not present".
func (m *ModelClass) ValidatesPresence(cols ...string) *ModelClass {
	m.validations = append(m.validations, func(i *Instance) {
		for _, c := range cols {
			if isBlank(i.values[c]) {
				i.errs.Add(c, "is not present")
			}
		}
	})
	return m
}

// ValidatesFormat requires a column to match a regexp — Sequel's
// validates_format. Message: "is invalid".
func (m *ModelClass) ValidatesFormat(re *regexp.Regexp, col string) *ModelClass {
	m.validations = append(m.validations, func(i *Instance) {
		s, ok := i.values[col].(string)
		if !ok || !re.MatchString(s) {
			i.errs.Add(col, "is invalid")
		}
	})
	return m
}

// LengthOpts configures a length validation. Set the bound(s) that apply; a
// zero field is ignored except Is, which is applied when >= 0 via HasIs.
type LengthOpts struct {
	Min   int // minimum length (0 = no minimum)
	Max   int // maximum length (0 = no maximum)
	Is    int // exact length; only applied when HasIs is set
	HasIs bool
}

// ValidatesLength checks a column's string length against the options —
// Sequel's validates_{min,max,exact}_length. Messages mirror the gem: "is
// shorter than N characters", "is longer than N characters", "is not N
// characters".
func (m *ModelClass) ValidatesLength(col string, opts LengthOpts) *ModelClass {
	m.validations = append(m.validations, func(i *Instance) {
		s, ok := i.values[col].(string)
		if !ok {
			i.errs.Add(col, "is not a valid string")
			return
		}
		n := utf8len(s)
		if opts.HasIs && n != opts.Is {
			i.errs.Add(col, fmt.Sprintf("is not %d characters", opts.Is))
		}
		if opts.Min > 0 && n < opts.Min {
			i.errs.Add(col, fmt.Sprintf("is shorter than %d characters", opts.Min))
		}
		if opts.Max > 0 && n > opts.Max {
			i.errs.Add(col, fmt.Sprintf("is longer than %d characters", opts.Max))
		}
	})
	return m
}

// ValidatesUnique requires the value(s) of the named column(s) to be unique in
// the table — Sequel's validates_unique. It runs "SELECT 1 AS one FROM t WHERE
// (...) LIMIT 1" (excluding the current row by primary key for a persisted
// record) and, if a row exists, adds "is already taken". For a persisted record
// the check is skipped when none of the columns changed, matching the gem.
// Message: "is already taken".
func (m *ModelClass) ValidatesUnique(cols ...string) *ModelClass {
	m.validations = append(m.validations, func(i *Instance) {
		if !i.isNew {
			changedAny := false
			for _, c := range cols {
				if i.changed[c] {
					changedAny = true
					break
				}
			}
			if !changedAny {
				return
			}
		}
		pairs := make([]Value, 0, len(cols)*2)
		for _, c := range cols {
			pairs = append(pairs, c, i.values[c])
		}
		ds := m.dataset.Select(As(Lit("1"), "one")).Where(H(pairs...))
		if !i.isNew {
			ds = ds.Where(Neq(m.primaryKey[0], i.values[m.primaryKey[0]]))
		}
		rows, err := m.db.Run(ds.Limit(1).SelectSQL())
		if err != nil {
			i.errs.Add(cols[0], "could not be validated for uniqueness")
			return
		}
		if len(rows) > 0 {
			for _, c := range cols {
				i.errs.Add(c, "is already taken")
			}
		}
	})
	return m
}

// AddValidation registers a custom validation callback — the Go form of
// overriding Sequel's #validate. It runs after the declarative validators.
func (m *ModelClass) AddValidation(fn func(*Instance)) *ModelClass {
	m.validations = append(m.validations, fn)
	return m
}

// ---- Running validation -------------------------------------------------

// Valid runs the before/after-validation hooks and every registered validator,
// (re)populating the instance's errors, and reports whether the instance is
// valid — Sequel's valid?.
func (i *Instance) Valid() bool {
	i.errs.clear()
	if err := i.runHooks(BeforeValidation); err != nil {
		i.errs.Add("", err.Error())
		return false
	}
	for _, v := range i.class.validations {
		v(i)
	}
	if err := i.runHooks(AfterValidation); err != nil {
		i.errs.Add("", err.Error())
		return false
	}
	return i.errs.Empty()
}

// ---- helpers ------------------------------------------------------------

// isBlank reports whether a value counts as absent for presence validation:
// nil, an empty/whitespace string, or an empty byte slice.
func isBlank(v Value) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return len(trimSpace(x)) == 0
	case []byte:
		return len(x) == 0
	default:
		return false
	}
}

// valuesEqual reports whether two column values are equal for dirty tracking.
// It compares the comparable dynamic types this library models; byte slices are
// compared element-wise. Unequal or incomparable values report false.
func valuesEqual(a, b Value) bool {
	if ab, ok := a.([]byte); ok {
		bb, ok := b.([]byte)
		if !ok || len(ab) != len(bb) {
			return false
		}
		for k := range ab {
			if ab[k] != bb[k] {
				return false
			}
		}
		return true
	}
	// a is not a []byte here (that case returned above); a []byte b compares
	// unequal to any non-slice value.
	if _, ok := b.([]byte); ok {
		return false
	}
	return a == b
}

// keyStr renders a primary/foreign key value as a stable string map key.
func keyStr(v Value) string { return fmt.Sprintf("%T:%v", v, v) }

// trimSpace trims ASCII whitespace without importing strings for one use.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && isSpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// utf8len returns the number of runes in s (character count, as Sequel measures
// length).
func utf8len(s string) int { return utf8.RuneCountInString(s) }
