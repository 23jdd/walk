# walk

**English** | [简体中文](README.zh-CN.md)

[![CI](https://github.com/23jdd/walk/actions/workflows/ci.yml/badge.svg)](https://github.com/23jdd/walk/actions/workflows/ci.yml)
[![Release](https://github.com/23jdd/walk/actions/workflows/release.yml/badge.svg)](https://github.com/23jdd/walk/actions/workflows/release.yml)

<p align="center">
  <img src="logo.svg" alt="walk logo" width="128" height="128">
</p>

`walk` is a single-node labelled property-graph database and Cypher-subset
query engine built on [`bbolt`](https://github.com/etcd-io/bbolt). It stores
nodes, typed edges and adjacency postings in one B+tree file, and exposes the
graph through a CLI and an HTTP service.

The implementation follows the lower-level operator contract in
`docs/operators.md` (the Markdown form of
`docs/算子规格与实现细节.pdf`), with the Cypher grammar in `docs/lang.md`.
Where semantics differ from openCypher, the decision table in
`docs/operators.md` wins.

## Quick start

```bash
go build -o walk ./cmd/walk

./walk write "CREATE (a:Person {name: 'Alice', age: 39}),
                    (b:Person {name: 'Bob', age: 27}),
                    (a)-[:KNOWS {since: 2020}]->(b)"

./walk query "MATCH (a:Person)-[:KNOWS]->(b)
              RETURN a.name AS a, b.name AS b, b.age AS age
              ORDER BY age"

./walk stats
./walk serve --addr=:7474
```

On Windows the binary is `walk.exe`. The default database is `./graph.db`;
override it with `--db` or the `WALK_DB` environment variable.

## Historical figures demo

A single-file, npm-free app lives in [demo.html](demo.html): a Chinese
historical-figures relationship explorer powered by vis-network and walk's
HTTP adapter. It ships with 31 people from 春秋 to 宋 and 38 relationships
(师承、君臣、盟友、思想传承…), plus dynasty filters, search, detail cards and
shortest relationship-chain discovery.

```bash
go build -o walk ./cmd/walk
./walk serve --addr=:7474

# option A: open demo.html directly (file://)
# option B: serve this directory without npm
python -m http.server 8080
# visit http://127.0.0.1:8080/demo.html
```

Click “载入示例人物图谱” on first run, then click a person to explore. The
server sends CORS headers, so a `file://` page can call
`http://127.0.0.1:7474` directly.

## CLI

```
walk load   --nodes <file> [--edges <file>] [--format=auto|jsonl|csv] [--batch-size=N]
walk query  '<cypher>' [--explain] [--json] [--timeout=5s]
walk write  '<cypher>'
walk stats  [--json]
walk backup <path>
walk compact
walk index  add|drop|list [label [property]]
walk serve  [--addr=:7474]
```

Common flags: `--db`, `--timeout`, `--max-solutions`, `--max-depth`,
`--max-detach`, `--batch-size`, `--log-level`.

Exit codes: `0` success, `1` usage, `2` data/graph validation error,
`3` internal error.

`load` accepts:

* JSONL nodes: `{"id": 1, "labels": ["Person"], "props": {"name": "Alice"}}`
  (`id` optional; `props` or `properties`).
* JSONL edges: `{"from": 1, "to": 2, "type": "KNOWS", "props": {...}}`.
* CSV nodes: header row; optional `id`, `labels` (comma/semicolon separated),
  other columns become properties. Row index (0-based) is the default id.
* CSV edges: header must include `from_idx` and `to_idx`; optional `type`;
  other columns become properties.

CSV values infer as `int64 -> float64 -> bool -> string` in that order.

## HTTP

`walk serve` starts:

| endpoint | method | purpose |
|---|---|---|
| `/query` | POST | read-only Cypher, JSON result |
| `/update` | POST | write Cypher, JSON result with summary |
| `/backup` | GET | streaming consistent snapshot |
| `/stats` | GET | F-21 statistics as JSON |
| `/health` | GET | liveness |

Examples:

```bash
curl -X POST localhost:7474/query \
     --data 'MATCH (n:Person) RETURN n.name AS name ORDER BY name'

curl -X POST localhost:7474/update \
     --data 'CREATE (n:Person {name: "Alice"})'
```

## Language

Supported Cypher subset:

* `MATCH` with node/relationship patterns, multiple labels, inline properties,
  typed relationships, multiple types (`:T|U`), undirected traversal,
  variable-length paths (`*`, `*n`, `*n..m`, `*..m`) and `shortestPath`.
* `WHERE` with comparisons, `AND/OR/NOT`, `STARTS WITH`, `ENDS WITH`,
  `CONTAINS`, `IS [NOT] NULL`, arithmetic.
* `CREATE`, `SET` (`n.p = e`, `n = {}`, `n += {}`, `n:Label`), `REMOVE`,
  `DELETE`, `DETACH DELETE`.
* `RETURN` with aliases, `DISTINCT`, `ORDER BY`, `SKIP`, `LIMIT`, `count`,
  `sum`, `avg`, `min`, `max`, `length(p)`, node/edge/path values.
* `EXPLAIN` (text plan, no execution).

See `docs/lang.md` for the EBNF, `docs/cypher-deviations.md` for deliberate
semantic differences, and `docs/neo4j-skip-list.md` for unsupported syntax.
A complete Chinese syntax reference is in
[`docs/syntax.zh-CN.md`](docs/syntax.zh-CN.md).

## Storage and invariants

Buckets: `nodes`, `edges`, `adj_out`, `adj_in`, `labels`, `reltypes`,
`reltype_by_id`, `node_label`, `edge_type`, `prop_idx`, `meta`.

Four invariants are checked by tests and by `walk stats`:

1. every edge's endpoints exist;
2. every edge has exactly one `adj_out` and one `adj_in` entry;
3. every node label has a `node_label` posting;
4. referenced dictionary entries are never deleted.

Writes are atomic per bbolt transaction. Bulk import commits in batches
(`<= 15,000` KV operations per transaction, edges capped at 3,000 per batch).
`COMPACT` copies live data to a new file, verifies node/edge counts, then
atomically replaces the old file.

## Project layout

```
cmd/walk/        CLI entry point
executor/        storage engine, indices, operators, streams, aggregation
parser/          Cypher lexer, AST, recursive-descent parser, diagnostics
internal/httpapi/ optional HTTP adapter (query/update/stats/backup, CORS)
walk.go          DB lifecycle, transactions, backup, compact
load.go          JSONL/CSV importers
stats.go         statistics
engine.go        AST -> operator chain planner
eval.go          expression evaluator
query.go         Query/Exec/Result API
docs/            language, operator, planner and deviation docs
```

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
```

The tests cover storage invariants, parser diagnostics, CRUD/query integration,
variable-length relationship uniqueness, shortest paths, aggregation and
ordering, JSONL/CSV import, HTTP endpoints and compaction/backup.

## Scope

Deliberately out of scope for v1: `OPTIONAL MATCH`, `WITH`, `UNWIND`, `MERGE`,
`CALL`, `UNION`, weighted shortest paths, nested/list properties, full-text and
geo indexes, multi-database and clustering. See
`docs/cypher-deviations.md` and `docs/neo4j-skip-list.md`.
