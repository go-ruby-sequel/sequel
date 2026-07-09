# Ruby examples

Pure-Ruby examples for the `sequel` toolkit as provided by
[go-embedded-ruby](https://github.com/go-embedded-ruby/ruby) (rbgo).
`Sequel.sqlite` opens an in-memory SQLite-backed database, so the query builder
both generates SQL and runs it against live rows. Run it with the `rbgo`
interpreter:

```sh
rbgo examples/sequel_usage.rb
```

| File | Shows |
| --- | --- |
| [`sequel_usage.rb`](sequel_usage.rb) | `Sequel.sqlite`, `create_table` schema DSL, `insert`, chainable `where`/`order`/`select` with `#sql`, and terminal `count`/`first`/`all`/`update`/`delete`. |

Each example is executed as-is under rbgo (`require "sequel"`).
