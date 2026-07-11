// Copyright (c) the go-ruby-sequel/sequel authors
//
// SPDX-License-Identifier: BSD-3-Clause

package sequel

import (
	"errors"
	"reflect"
	"testing"
)

// buildAssocModels wires artists/albums/tags models with the four association
// kinds onto one database/executor.
func buildAssocModels(exec Executor) (artist, album, tag *ModelClass) {
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

func TestAssociationDatasets(t *testing.T) {
	artist, album, _ := buildAssocModels(newMock())
	a := artist.Load(map[string]Value{"id": 1, "name": "x"})
	al := album.Load(map[string]Value{"id": 2, "name": "y", "artist_id": 1})

	cases := []struct {
		name string
		inst *Instance
		want string
	}{
		{"albums", a, "SELECT * FROM albums WHERE (albums.artist_id = 1)"},
		{"first_album", a, "SELECT * FROM albums WHERE (albums.artist_id = 1) LIMIT 1"},
		{"artist", al, "SELECT * FROM artists WHERE (artists.id = 1) LIMIT 1"},
		{"tags", al, "SELECT tags.* FROM tags INNER JOIN albums_tags ON (albums_tags.tag_id = tags.id) WHERE (albums_tags.album_id = 2)"},
	}
	for _, c := range cases {
		if got := c.inst.AssociationDataset(c.name).SelectSQL(); got != c.want {
			t.Errorf("%s dataset:\n got=%q\nwant=%q", c.name, got, c.want)
		}
	}
}

func TestAssociationDefaults(t *testing.T) {
	// No explicit keys/join-table: exercise the convention inference.
	db := Connect("default", newMock())
	artist := db.Model("artists").SetColumns("id")
	album := db.Model("albums").SetColumns("id", "artist_id")
	tag := db.Model("tags").SetColumns("id")
	artist.OneToMany("albums", album)
	album.ManyToOne("artist", artist)
	album.ManyToMany("tags", tag)

	a := artist.Load(map[string]Value{"id": 1})
	if got := a.AssociationDataset("albums").SelectSQL(); got != "SELECT * FROM albums WHERE (albums.artist_id = 1)" {
		t.Errorf("otm default key: %q", got)
	}
	al := album.Load(map[string]Value{"id": 2, "artist_id": 1})
	if got := al.AssociationDataset("artist").SelectSQL(); got != "SELECT * FROM artists WHERE (artists.id = 1) LIMIT 1" {
		t.Errorf("mto default key: %q", got)
	}
	// join table = sorted table names joined by "_": albums_tags
	want := "SELECT tags.* FROM tags INNER JOIN albums_tags ON (albums_tags.tag_id = tags.id) WHERE (albums_tags.album_id = 2)"
	if got := al.AssociationDataset("tags").SelectSQL(); got != want {
		t.Errorf("mtm defaults: %q", got)
	}
	// singular() no-op branch for a table not ending in "s"
	db2 := Connect("default", newMock())
	sheep := db2.Model("sheep").SetColumns("id")
	kid := db2.Model("kids").SetColumns("id", "sheep_id")
	sheep.OneToMany("kids", kid)
	sk := sheep.Load(map[string]Value{"id": 3})
	if got := sk.AssociationDataset("kids").SelectSQL(); got != "SELECT * FROM kids WHERE (kids.sheep_id = 3)" {
		t.Errorf("singular no-op: %q", got)
	}
}

func TestAssociationLazyLoad(t *testing.T) {
	mock := newMock(
		[]map[string]Value{{"id": 10, "name": "a1", "artist_id": 1}, {"id": 11, "name": "a2", "artist_id": 1}},
	)
	artist, _, _ := buildAssocModels(mock)
	a := artist.Load(map[string]Value{"id": 1, "name": "x"})
	albums, err := a.Related("albums")
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 2 || albums[0].PK() != 10 {
		t.Fatalf("albums = %v", albums)
	}
	// second call is cached (no new SQL)
	if _, err := a.Related("albums"); err != nil {
		t.Fatal(err)
	}
	if len(mock.sqls) != 1 {
		t.Fatalf("expected 1 query (cached), got %v", mock.sqls)
	}
}

func TestAssociationRelatedOne(t *testing.T) {
	mock := newMock([]map[string]Value{{"id": 1, "name": "x"}})
	_, album, _ := buildAssocModels(mock)
	al := album.Load(map[string]Value{"id": 2, "name": "y", "artist_id": 1})
	art, err := al.RelatedOne("artist")
	if err != nil || art == nil || art.PK() != 1 {
		t.Fatalf("related one = %v %v", art, err)
	}
	// empty -> nil
	mock2 := newMock()
	_, album2, _ := buildAssocModels(mock2)
	al2 := album2.Load(map[string]Value{"id": 2, "artist_id": 99})
	got, err := al2.RelatedOne("artist")
	if err != nil || got != nil {
		t.Fatalf("expected nil, got %v %v", got, err)
	}
}

func TestAssociationRelatedError(t *testing.T) {
	mock := newMock()
	mock.always = true
	mock.errVal = errors.New("boom")
	artist, _, _ := buildAssocModels(mock)
	a := artist.Load(map[string]Value{"id": 1})
	if _, err := a.Related("albums"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := a.RelatedOne("albums"); err == nil {
		t.Fatal("expected error from RelatedOne")
	}
}

func TestAssociationAddRemove(t *testing.T) {
	mock := newMock()
	artist, album, tag := buildAssocModels(mock)
	a := artist.Load(map[string]Value{"id": 3, "name": "x"})

	// one_to_many add: sets FK and saves the child
	newAlbum := album.Load(map[string]Value{"id": 9, "name": "z", "artist_id": nil})
	if err := a.Add("albums", newAlbum); err != nil {
		t.Fatal(err)
	}
	if mock.last() != "UPDATE albums SET artist_id = 3 WHERE (id = 9)" {
		t.Fatalf("otm add sql = %q", mock.last())
	}
	// one_to_many remove: nils the FK
	if err := a.Remove("albums", newAlbum); err != nil {
		t.Fatal(err)
	}
	if mock.last() != "UPDATE albums SET artist_id = NULL WHERE (id = 9)" {
		t.Fatalf("otm remove sql = %q", mock.last())
	}

	// many_to_many add/remove: join-table rows
	al := album.Load(map[string]Value{"id": 1, "name": "a"})
	rock := tag.Load(map[string]Value{"id": 7, "name": "rock"})
	if err := al.Add("tags", rock); err != nil {
		t.Fatal(err)
	}
	if mock.last() != "INSERT INTO albums_tags (album_id, tag_id) VALUES (1, 7)" {
		t.Fatalf("mtm add sql = %q", mock.last())
	}
	if err := al.Remove("tags", rock); err != nil {
		t.Fatal(err)
	}
	if mock.last() != "DELETE FROM albums_tags WHERE ((album_id = 1) AND (tag_id = 7))" {
		t.Fatalf("mtm remove sql = %q", mock.last())
	}
}

func TestAssociationAddRemoveErrorsAndPanics(t *testing.T) {
	boom := errors.New("boom")
	mkErr := func() *mockExec { mk := newMock(); mk.errAt = 1; mk.errVal = boom; return mk }

	// otm add save error
	artist, album, tag := buildAssocModels(mkErr())
	a := artist.Load(map[string]Value{"id": 3})
	if err := a.Add("albums", album.Load(map[string]Value{"id": 9, "artist_id": nil})); err != boom {
		t.Fatalf("otm add err = %v", err)
	}
	// otm remove save error
	artist2, album2, _ := buildAssocModels(mkErr())
	a2 := artist2.Load(map[string]Value{"id": 3})
	if err := a2.Remove("albums", album2.Load(map[string]Value{"id": 9, "artist_id": 3})); err != boom {
		t.Fatalf("otm remove err = %v", err)
	}
	// mtm add/remove exec error
	artist3, album3, tag3 := buildAssocModels(mkErr())
	_ = artist3
	al := album3.Load(map[string]Value{"id": 1})
	if err := al.Add("tags", tag3.Load(map[string]Value{"id": 7})); err != boom {
		t.Fatalf("mtm add err = %v", err)
	}
	artist4, album4, tag4 := buildAssocModels(mkErr())
	_ = artist4
	al4 := album4.Load(map[string]Value{"id": 1})
	if err := al4.Remove("tags", tag4.Load(map[string]Value{"id": 7})); err != boom {
		t.Fatalf("mtm remove err = %v", err)
	}

	// panic: Add/Remove on many_to_one
	_, album5, _ := buildAssocModels(newMock())
	al5 := album5.Load(map[string]Value{"id": 1, "artist_id": 1})
	assertPanic(t, "add mto", func() { al5.Add("artist", nil) })
	assertPanic(t, "remove mto", func() { al5.Remove("artist", nil) })
	_ = tag
}

// ---- eager loading ------------------------------------------------------

func TestEagerOneToMany(t *testing.T) {
	mock := newMock(
		[]map[string]Value{{"id": 1, "name": "a"}, {"id": 2, "name": "b"}}, // artists
		[]map[string]Value{ // albums
			{"id": 10, "name": "x", "artist_id": 1},
			{"id": 11, "name": "y", "artist_id": 1},
			{"id": 12, "name": "z", "artist_id": 2},
		},
	)
	artist, _, _ := buildAssocModels(mock)
	artists, err := artist.Eager("albums").All()
	if err != nil {
		t.Fatal(err)
	}
	if mock.sqls[1] != "SELECT * FROM albums WHERE (albums.artist_id IN (1, 2))" {
		t.Fatalf("eager otm sql = %q", mock.sqls[1])
	}
	a1, _ := artists[0].Related("albums")
	if len(a1) != 2 {
		t.Fatalf("artist1 albums = %d", len(a1))
	}
	a2, _ := artists[1].Related("albums")
	if len(a2) != 1 {
		t.Fatalf("artist2 albums = %d", len(a2))
	}
}

func TestEagerOneToOne(t *testing.T) {
	mock := newMock(
		[]map[string]Value{{"id": 1, "name": "a"}, {"id": 2, "name": "b"}},
		[]map[string]Value{{"id": 10, "name": "x", "artist_id": 1}},
	)
	artist, _, _ := buildAssocModels(mock)
	artists, err := artist.Eager("first_album").All()
	if err != nil {
		t.Fatal(err)
	}
	one, _ := artists[0].Related("first_album")
	if len(one) != 1 || one[0].PK() != 10 {
		t.Fatalf("o2o eager = %v", one)
	}
	none, _ := artists[1].Related("first_album")
	if len(none) != 0 {
		t.Fatalf("o2o eager empty = %v", none)
	}
}

func TestEagerManyToOne(t *testing.T) {
	mock := newMock(
		[]map[string]Value{{"id": 10, "name": "x", "artist_id": 5}, {"id": 11, "name": "y", "artist_id": 5}},
		[]map[string]Value{{"id": 5, "name": "artist"}},
	)
	_, album, _ := buildAssocModels(mock)
	albums, err := album.Eager("artist").All()
	if err != nil {
		t.Fatal(err)
	}
	if mock.sqls[1] != "SELECT * FROM artists WHERE (artists.id IN (5))" {
		t.Fatalf("eager mto sql = %q", mock.sqls[1])
	}
	art, _ := albums[0].RelatedOne("artist")
	if art == nil || art.PK() != 5 {
		t.Fatalf("eager mto = %v", art)
	}
	// no match -> empty cache
	mock2 := newMock(
		[]map[string]Value{{"id": 10, "name": "x", "artist_id": 99}},
		[]map[string]Value{},
	)
	_, album2, _ := buildAssocModels(mock2)
	albums2, _ := album2.Eager("artist").All()
	got, _ := albums2[0].RelatedOne("artist")
	if got != nil {
		t.Fatalf("expected nil eager mto, got %v", got)
	}
}

func TestEagerManyToMany(t *testing.T) {
	mock := newMock(
		[]map[string]Value{{"id": 1, "name": "a"}, {"id": 2, "name": "b"}}, // albums
		[]map[string]Value{ // tags with x_foreign_key_x
			{"id": 100, "name": "rock", "x_foreign_key_x": 1},
			{"id": 101, "name": "pop", "x_foreign_key_x": 1},
			{"id": 102, "name": "jazz", "x_foreign_key_x": 2},
		},
	)
	_, album, _ := buildAssocModels(mock)
	albums, err := album.Eager("tags").All()
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT tags.*, albums_tags.album_id AS x_foreign_key_x FROM tags INNER JOIN albums_tags ON (albums_tags.tag_id = tags.id) WHERE (albums_tags.album_id IN (1, 2))"
	if mock.sqls[1] != want {
		t.Fatalf("eager mtm sql = %q", mock.sqls[1])
	}
	t1, _ := albums[0].Related("tags")
	if len(t1) != 2 {
		t.Fatalf("album1 tags = %d", len(t1))
	}
	// loaded tags must not carry the foreign-key alias column
	if _, ok := t1[0].values["x_foreign_key_x"]; ok {
		t.Fatal("eager mtm leaked x_foreign_key_x into instance")
	}
}

func TestEagerErrorsAndEmpty(t *testing.T) {
	// main query error
	mkMain := newMock()
	mkMain.errAt = 1
	mkMain.errVal = errors.New("boom")
	artist, _, _ := buildAssocModels(mkMain)
	if _, err := artist.Eager("albums").All(); err == nil {
		t.Fatal("expected main query error")
	}
	// association query error
	mkAssoc := newMock([]map[string]Value{{"id": 1, "name": "a"}})
	mkAssoc.errAt = 2
	mkAssoc.errVal = errors.New("boom")
	artist2, _, _ := buildAssocModels(mkAssoc)
	if _, err := artist2.Eager("albums").All(); err == nil {
		t.Fatal("expected assoc query error")
	}
	// eager with zero parents is a no-op for the association
	empty := newMock([]map[string]Value{})
	artist3, _, _ := buildAssocModels(empty)
	res, err := artist3.Eager("albums").All()
	if err != nil || len(res) != 0 {
		t.Fatalf("empty eager = %v %v", res, err)
	}
	if len(empty.sqls) != 1 {
		t.Fatalf("empty eager should issue only main query, got %v", empty.sqls)
	}
	// mto/mtm assoc query errors
	for _, name := range []string{"artist", "tags"} {
		mk := newMock([]map[string]Value{{"id": 1, "name": "a", "artist_id": 2}})
		mk.errAt = 2
		mk.errVal = errors.New("boom")
		_, album, _ := buildAssocModels(mk)
		if _, err := album.Eager(name).All(); err == nil {
			t.Fatalf("expected %s eager error", name)
		}
	}
}

func TestEagerSkipsNilKeys(t *testing.T) {
	// A parent whose foreign key is nil is skipped from the IN(...) list.
	mock := newMock(
		[]map[string]Value{{"id": 10, "name": "x", "artist_id": nil}},
		[]map[string]Value{},
	)
	_, album, _ := buildAssocModels(mock)
	if _, err := album.Eager("artist").All(); err != nil {
		t.Fatal(err)
	}
	// with all keys nil, no IN list -> the association query still runs with an
	// empty IN set. Assert the main query ran.
	if mock.sqls[0] != "SELECT * FROM albums" {
		t.Fatalf("main sql = %q", mock.sqls[0])
	}
}

// ---- eager_graph --------------------------------------------------------

func TestEagerGraphManyToOne(t *testing.T) {
	mock := newMock([]map[string]Value{
		{"id": 10, "name": "x", "artist_id": 5, "artist_id_col": 5, "artist_name": "A"},
	})
	_, album, _ := buildAssocModels(mock)
	albums, err := album.EagerGraph("artist").All()
	if err != nil {
		t.Fatal(err)
	}
	// The join structure is asserted; column aliasing uses <assoc>_<col>.
	sql, _ := album.EagerGraph("artist").eagerGraphSQL()
	want := "SELECT albums.id, albums.name, albums.artist_id, artists.id AS artist_id, artists.name AS artist_name FROM albums LEFT OUTER JOIN artists ON (artists.id = albums.artist_id)"
	if sql != want {
		t.Fatalf("eager_graph sql:\n got=%q\nwant=%q", sql, want)
	}
	if len(albums) != 1 {
		t.Fatalf("albums = %d", len(albums))
	}
	art, _ := albums[0].RelatedOne("artist")
	if art == nil || art.Get("name") != "A" {
		t.Fatalf("graphed artist = %v", art)
	}
}

func TestEagerGraphOneToMany(t *testing.T) {
	// Two albums for one artist come back as two joined rows; the parent is
	// de-duplicated and both children attach.
	mock := newMock([]map[string]Value{
		{"id": 1, "name": "artist", "albums_id": 10, "albums_name": "x", "albums_artist_id": 1},
		{"id": 1, "name": "artist", "albums_id": 11, "albums_name": "y", "albums_artist_id": 1},
		{"id": 2, "name": "lonely", "albums_id": nil, "albums_name": nil, "albums_artist_id": nil},
	})
	artist, _, _ := buildAssocModels(mock)
	artists, err := artist.EagerGraph("albums").All()
	if err != nil {
		t.Fatal(err)
	}
	if len(artists) != 2 {
		t.Fatalf("artists = %d", len(artists))
	}
	kids, _ := artists[0].Related("albums")
	if len(kids) != 2 {
		t.Fatalf("artist1 albums = %d", len(kids))
	}
	none, _ := artists[1].Related("albums")
	if len(none) != 0 {
		t.Fatalf("lonely albums = %d", len(none))
	}
}

func TestEagerGraphError(t *testing.T) {
	mock := newMock()
	mock.errAt = 1
	mock.errVal = errors.New("boom")
	artist, _, _ := buildAssocModels(mock)
	if _, err := artist.EagerGraph("albums").All(); err == nil {
		t.Fatal("expected eager_graph error")
	}
}

var _ = reflect.DeepEqual
