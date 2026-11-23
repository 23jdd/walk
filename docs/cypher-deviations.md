# Cypher deviations from openCypher / Neo4j (v1)

The implementation targets a deliberately small, fail-fast subset. Known
differences are listed here and in `docs/neo4j-skip-list.md`.

## Null semantics

* A comparison (`=`, `<>`, `<`, `<=`, `>`, `>=`, `CONTAINS`, `STARTS WITH`,
  `ENDS WITH`) with `null` on either side returns `false`, not `null`. This
  simplifies filtering and is locked by tests.
* `NOT null` evaluates as `false`; `null` in `AND`/`OR` is treated as `false`.
* `IS NULL` / `IS NOT NULL` are the only operators that observe null directly.
* Missing properties read as `null`; there is no distinction between "missing"
  and "explicit null" (null properties are not stored).

## Variable-length paths

* `*` (no bounds) means `1..8`; the upper bound is also capped by
  `--max-depth` (default 8).
* A lower bound of `0` is rejected (`0..` would make the start node match
  itself ambiguously).
* Relationship uniqueness applies per path; nodes may repeat.
* `via` is not supported on a variable-length relationship because binding one
  edge out of a multi-edge path is ambiguous.
* Properties on variable-length relationships are rejected.
* Paths are supported as values (`p`, `length(p)`), but shortest-path ties do
  not expose a deterministic path ordering beyond the first BFS hit.

## Aggregation

* `sum`/`avg`/`min`/`max` on an empty or all-null group return `null`
  (`count` returns `0`), matching SQL rather than returning a row with no group.
* `avg` always returns `float64`.
* `int64` sum overflow wraps silently (documented, not an error).
* Type errors in aggregate arguments (e.g. `sum(n.name)`) abort the whole
  query instead of being ignored.
* `count(DISTINCT expr)` is parsed but currently behaves as `count(expr)`;
  `RETURN DISTINCT` is the supported way to deduplicate rows.

## Ordering

* Total order is `null < bool < number < string`; numbers compare numerically
  across int/float; strings compare by UTF-8 bytes.
* `DESC` reverses the whole key, so nulls sort last in descending order.
* Ordering is stable (`slices.SortStableFunc`).

## DISTINCT

* `DISTINCT` is type-strict: `int(1)` and `float(1.0)` are distinct rows,
  even though ordering compares them as equal numbers.
* Two nulls in the same column merge into one row.

## Write semantics

* `CREATE` accepts only literal property values. Per-solution expression
  properties are out of scope.
* `SET n = {map}` replaces all properties; `SET n += {map}` merges.
* `SET n.p = null` deletes the property.
* `DELETE n` fails if the node still has edges; use `DETACH DELETE`.
* `DETACH DELETE` is limited by `--max-detach` (default 100,000).
* Deleting an entity does not create a tombstone: later solutions in the same
  stream read its properties as null.
* A whole write query is one bbolt transaction; any error rolls back every
  write in the query.

## Expressions and functions

* Available functions: `count`, `sum`, `avg`, `min`, `max`, `length`,
  `id`, `toString`, `toInteger`, `toFloat`.
* Arithmetic supports `+ - * / %`; `/` and `%` always return floats.
* Comparing incompatible types (`"30" > 30`) is a type error that aborts the
  query; comparing different non-numeric types with `=` is `false`.
* No list or map values are stored; properties are `null/bool/int64/float64/
  string` only.
* No parameters (`$name`), no `CASE`, no `COALESCE`, no user-defined functions.

## Planning / execution

* `MATCH` patterns are joined left-to-right with anchor selection; `WHERE` is
  applied after all patterns. Filter push-down is explicit rather than
  optimizer-driven (the low-level operator API allows `keep` between hops).
* `EXPLAIN` returns a text plan and does not execute the query.
* Query timeout is cooperative: the executor checks the context every 1024
  solutions and at operator boundaries; bbolt transactions are not force-killed.
