// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"os/exec"
	"strings"
	"testing"
)

// This oracle pins the Model layer's generated SQL to the real `sequel` gem's
// Model layer, exactly as oracle_test.go does for the dataset/schema core. It
// builds the same artists/albums/tags model graph in Ruby (on the gem's mock
// adapter, so no database is touched) and in Go, then compares the SQL each
// emits for the association datasets, the finders, the instance CRUD statements
// (INSERT/UPDATE/refresh-SELECT, with the transaction wrapper stripped), and the
// validates_unique existence probe — byte-for-byte. It skips itself where ruby
// or the gem is absent, like the other oracle; the deterministic golden
// behavioural tests hold coverage at 100% on those lanes.

// rubyModelPreamble defines the shared model graph on a mock database and helper
// methods the per-case bodies use.
const rubyModelPreamble = `require "sequel"
$stdout.binmode
db = Sequel.mock(autoid: 1, numrows: 1)
def mk(db, tbl, cols)
  c = Class.new(Sequel::Model(db[tbl]))
  c.define_singleton_method(:columns){ cols }
  c.send(:def_column_accessor, *cols)
  c.plugin :validation_helpers
  c
end
Artist = mk(db, :artists, [:id, :name])
Album  = mk(db, :albums, [:id, :name, :artist_id])
Tag    = mk(db, :tags, [:id, :name])
Artist.one_to_many :albums, class: Album, key: :artist_id
Artist.one_to_one :first_album, class: Album, key: :artist_id
Album.many_to_one :artist, class: Artist, key: :artist_id
Album.many_to_many :tags, class: Tag, left_key: :album_id, right_key: :tag_id, join_table: :albums_tags
# sqls_no_tx returns the recorded SQL minus the transaction wrapper and minus
# the mock adapter's "LIMIT 0" naked-column introspection probes (the Go Model
# takes its columns from SetColumns and issues no such probe).
def sqls_no_tx(db)
  db.sqls.reject{|s| s=="BEGIN"||s=="COMMIT"||s.end_with?(" LIMIT 0")}.join("\n")
end
`

// gemModelSQL runs a per-case Ruby body after the shared preamble and returns
// the trimmed SQL it prints.
func gemModelSQL(t *testing.T, bin, body string) string {
	t.Helper()
	out, err := exec.Command(bin, "-e", rubyModelPreamble+body).CombinedOutput()
	if err != nil {
		t.Fatalf("ruby error: %v\noutput:\n%s", err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// goModels builds the Go model graph mirroring rubyModelPreamble against exec.
func goModels(exec Executor) (artist, album, tag *ModelClass) {
	db := Connect("default", exec)
	artist = db.Model("artists").SetColumns("id", "name")
	album = db.Model("albums").SetColumns("id", "name", "artist_id")
	tag = db.Model("tags").SetColumns("id", "name")
	artist.OneToMany("albums", album, Key("artist_id"))
	artist.OneToOne("first_album", album, Key("artist_id"))
	album.ManyToOne("artist", artist, Key("artist_id"))
	album.ManyToMany("tags", tag, JoinTable("albums_tags"), LeftKey("album_id"), RightKey("tag_id"))
	return
}

func TestOracleModel(t *testing.T) {
	bin := rubySequel(t)

	cases := []struct {
		name string
		got  func() string
		ruby string
	}{
		{"otm-dataset", func() string {
			a, _, _ := goModels(newMock())
			return a.Load(map[string]Value{"id": 1, "name": "x"}).AssociationDataset("albums").SelectSQL()
		}, `print Artist.load(id: 1, name: "x").albums_dataset.sql`},

		{"oto-dataset", func() string {
			a, _, _ := goModels(newMock())
			return a.Load(map[string]Value{"id": 1}).AssociationDataset("first_album").SelectSQL()
		}, `print Artist.load(id: 1).first_album_dataset.sql`},

		{"mto-dataset", func() string {
			_, al, _ := goModels(newMock())
			return al.Load(map[string]Value{"id": 2, "artist_id": 1}).AssociationDataset("artist").SelectSQL()
		}, `print Album.load(id: 2, artist_id: 1).artist_dataset.sql`},

		{"mtm-dataset", func() string {
			_, al, _ := goModels(newMock())
			return al.Load(map[string]Value{"id": 2}).AssociationDataset("tags").SelectSQL()
		}, `print Album.load(id: 2).tags_dataset.sql`},

		{"by-pk", func() string {
			_, al, _ := goModels(newMock())
			return al.withPKDataset(2).Limit(1).SelectSQL()
		}, `print Album.dataset.where(id: 2).limit(1).sql`},

		{"where", func() string {
			_, al, _ := goModels(newMock())
			return al.Where(H("name", "y")).SQL()
		}, `print Album.where(name: "y").sql`},

		{"order-limit", func() string {
			_, al, _ := goModels(newMock())
			return al.Order("name").Limit(5).SQL()
		}, `print Album.order(:name).limit(5).sql`},

		{"insert", func() string {
			_, al, _ := goModels(newMock())
			return al.New("name", "y", "artist_id", 1).insertSQL()
		}, `print Album.dataset.insert_sql(name: "y", artist_id: 1)`},

		{"update", func() string {
			_, al, _ := goModels(newMock())
			i := al.Load(map[string]Value{"id": 2, "name": "x"})
			i.Set("name", "z")
			return i.updateSQL()
		}, `print Album.dataset.where(id: 2).update_sql(name: "z")`},

		// End-to-end create: the INSERT and the refresh SELECT the Model issues,
		// with BEGIN/COMMIT stripped, must match the gem's create statements.
		{"create-sqls", func() string {
			mock := newMock([]map[string]Value{{"id": 1, "name": "y", "artist_id": 1}})
			_, al, _ := goModels(mock)
			if _, err := al.Create("name", "y", "artist_id", 1); err != nil {
				t.Fatal(err)
			}
			return strings.Join(mock.sqls, "\n")
		}, `db.fetch = {id: 1, name: "y", artist_id: 1}
Album.create(name: "y", artist_id: 1); print sqls_no_tx(db)`},

		// validates_unique existence probe (new record).
		{"unique-new", func() string {
			mock := newMock()
			_, al, _ := goModels(mock)
			al.ValidatesUnique("name")
			al.New("name", "y").Valid()
			return mock.last()
		}, `Album.class_eval{ def validate; validates_unique(:name); end }
Album.new(name: "y").valid?; print sqls_no_tx(db)`},

		// validates_unique probe (existing record, changed value -> excludes pk).
		{"unique-exclude", func() string {
			mock := newMock()
			_, al, _ := goModels(mock)
			al.ValidatesUnique("name")
			i := al.Load(map[string]Value{"id": 3, "name": "old"})
			i.Set("name", "new")
			i.Valid()
			return mock.last()
		}, `Album.class_eval{ def validate; validates_unique(:name); end }
a = Album.load(id: 3, name: "old"); a.name = "new"; a.valid?; print sqls_no_tx(db)`},

		// eager batch loading: main query + IN(...) association query.
		{"eager-otm", func() string {
			mock := newMock(
				[]map[string]Value{{"id": 1, "name": "a"}, {"id": 2, "name": "b"}},
				[]map[string]Value{{"id": 10, "name": "x", "artist_id": 1}},
			)
			a, _, _ := goModels(mock)
			if _, err := a.Eager("albums").All(); err != nil {
				t.Fatal(err)
			}
			return strings.Join(mock.sqls, "\n")
		}, `db.fetch = [{id: 1, name: "a"}, {id: 2, name: "b"}]
Artist.eager(:albums).all; print sqls_no_tx(db)`},

		{"eager-mto", func() string {
			mock := newMock(
				[]map[string]Value{{"id": 10, "name": "x", "artist_id": 5}},
				[]map[string]Value{{"id": 5, "name": "a"}},
			)
			_, al, _ := goModels(mock)
			if _, err := al.Eager("artist").All(); err != nil {
				t.Fatal(err)
			}
			return strings.Join(mock.sqls, "\n")
		}, `db.fetch = [{id: 10, name: "x", artist_id: 5}]
Album.eager(:artist).all; print sqls_no_tx(db)`},

		{"eager-mtm", func() string {
			mock := newMock(
				[]map[string]Value{{"id": 1, "name": "a"}, {"id": 2, "name": "b"}},
				[]map[string]Value{{"id": 100, "name": "rock", "x_foreign_key_x": 1}},
			)
			_, al, _ := goModels(mock)
			if _, err := al.Eager("tags").All(); err != nil {
				t.Fatal(err)
			}
			return strings.Join(mock.sqls, "\n")
		}, `db.fetch = [{id: 1, name: "a"}, {id: 2, name: "b"}]
Album.eager(:tags).all; print sqls_no_tx(db)`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := gemModelSQL(t, bin, c.ruby)
			got := c.got()
			if got != want {
				t.Errorf("Model SQL mismatch:\n go  =%q\n gem =%q", got, want)
			}
		})
	}
}
