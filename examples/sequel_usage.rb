# frozen_string_literal: true
#
# Pure-Ruby usage of the Sequel toolkit, as provided by go-embedded-ruby (rbgo).
# Run it with:  rbgo examples/sequel_usage.rb
#
# Sequel.sqlite opens an in-memory database backed by a real SQLite executor, so
# the query builder both generates SQL and truly runs it against live rows.

require "sequel"

DB = Sequel.sqlite # in-memory SQLite database

DB.create_table(:artists) do
  primary_key :id
  String :name
  Integer :year
end

artists = DB[:artists]
artists.insert(name: "Miles Davis",   year: 1959)
artists.insert(name: "John Coltrane", year: 1965)
artists.insert(name: "Bill Evans",    year: 1961)

# Chainable builders compose SQL without touching the database.
puts artists.where(year: 1959).sql        # SELECT * FROM `artists` WHERE (`year` = 1959)
puts artists.order(:year).select(:name).sql

# Terminal methods run the query through the executor.
puts artists.count                        # => 3
p artists.where(name: "Miles Davis").first
artists.order(:year).all.each { |row| puts "#{row[:year]}: #{row[:name]}" }

# insert / update / delete report their effect.
puts artists.where(year: 1959).update(year: 1960) # affected rows => 1
puts artists.where(name: "Bill Evans").delete      # affected rows => 1
puts artists.count                                 # => 2
