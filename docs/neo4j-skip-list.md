# Neo4j compatibility skip list

This file records syntax that is intentionally not parsed (or has different
semantics) so the differential test harness can skip it explicitly.

## Not parsed (parser returns a positioned error)

| Syntax | Reason / alternative |
|---|---|
| `OPTIONAL MATCH` | needs nested scope / optional solution semantics |
| `WITH ...` | pipelined scope; `RETURN` only in v1 |
| `UNWIND` | list data model is out of scope |
| `MERGE` | needs match-or-create atomic semantics |
| `CALL { ... }`, `CALL db.*` | procedure subsystem |
| `UNION`, `UNION ALL` | multi-query composition |
| `MATCH ... WHERE EXISTS { ... }` | subquery support |
| `MATCH (n) WHERE n.age IN [1,2]` | list literals (parser rejects `[` in expressions) |
| `[x IN list | x + 1]` | list comprehensions |
| `reduce`, `collect`, `size`, `head`, `tail` | collection functions |
| `CASE WHEN ...` | conditional expressions |
| `$param` | parameter binding is not part of the CLI/HTTP subset |
| `CREATE INDEX ...` / `DROP INDEX ...` | use `walk index add|drop` |
| `CREATE CONSTRAINT ...` | no constraints in v1 |
| `LOAD CSV ...` | use `walk load` |
| `MATCH p = (a)-[*]->(b) RETURN nodes(p)` | path decomposition functions |
| `MATCH ... RETURN p` where p is a shortest path | shortestPath only binds a path value; no `nodes(p)`/`relationships(p)` |
| `MATCH (a)<-[*]-(b)` with `via` | variable-length edge binding is ambiguous |
| `MATCH (a)-[r*]->(b) RETURN r` | same as above |
| Multiple statements separated by `;` | one statement per call (a trailing `;` is allowed) |

## Parsed but semantically different

| Syntax | Difference |
|---|---|
| `null` comparison | returns `false`, never `null` (see `docs/cypher-deviations.md`) |
| `*` variable length | capped at 8 (or `--max-depth`) |
| `*0..` | rejected (lower bound must be >= 1) |
| `RETURN count(DISTINCT x)` | parsed but identical to `count(x)` in v1; use `RETURN DISTINCT` |
| `ORDER BY` mixed types | null < bool < number < string, instead of Neo4j's own ordering |
| `DISTINCT` int/float | type-strict: `1` and `1.0` are different |
| `avg` | always float64 |
| `sum` overflow | int64 wraps silently |
| `DELETE` on a connected node | errors; use `DETACH DELETE` |
| `DETACH DELETE` | capped by `--max-detach` (default 100,000) |
| Whole-query write failure | bbolt transaction rolls back every write in the query |

## Differential test notes

The golden tests in `testdata/` compare sorted TSV output. When adding a new
case, either implement the syntax or add it to this list with a reason. Do not
silently change expected output.
