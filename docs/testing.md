# Testing strategy (F-22)

All tests run without external services; they use `t.TempDir()` databases.

## Layers

| layer | location | what it locks |
|---|---|---|
| value/entity JSON | `executor/storage_test.go` | int64 > 2^53, float/bool/string, legacy keys |
| storage invariants | `executor/storage_test.go` | adjacency mirror counts, label postings, cascade delete, explicit IDs, F-25 index maintenance |
| operators | `executor/storage_test.go` | Hop both/self-loop dedup, VarLen edge uniqueness and node revisits, LIMIT early stop |
| parser | `parser/parser_test.go` | clause/pattern/direction/range parsing, precedence, string escapes, positioned errors |
| import | `load_test.go` | JSONL/CSV, batching, progress, type inference, duplicate/dangling rejection |
| query engine | `engine_test.go` | CRUD, WHERE/IS NULL, VarLen, shortest path, aggregation, grouping, ordering, DISTINCT, path values, SET/REMOVE |
| DB lifecycle | `db_test.go` | hot backup, compact + verification, write rollback, detach delete, stats, concurrent readers/serial writers |
| HTTP | `server_test.go` | `/query`, `/update`, `/stats`, `/backup`, read-only enforcement |
| property-style | `property_test.go` | fixed-seed random CRUD, invariants checked after every op (run with `-race`) |
| golden/differential | `golden_test.go` + `testdata/golden` | `.cypher` input vs sorted `.tsv` output |
| benchmarks | `benchmark_test.go` | node create, 1-hop, VarLen, ORDER+LIMIT |

## Commands

```bash
go test ./...
go test -race ./...
go vet ./...
go test . -run '^$' -bench . -benchmem
```

## Golden cases

Each `testdata/golden/<case>.cypher` may have a `<case>.setup.cypher` executed
first. The result is compared with `<case>.tsv` after sorting rows, so scan
order changes do not break the test. Add a case by writing the three files; no
Go code is needed.

## Manual crash test

F-22 asks for a `kill -9` scenario. On Linux/macOS:

```bash
# terminal 1: continuously import
walk load --nodes big-nodes.jsonl --edges big-edges.jsonl --batch-size 1000

# terminal 2: kill the process mid-import
kill -9 <pid>

# reopen and verify
walk stats
```

Because every batch is one bbolt `Update`, the reopened database contains a
prefix of completed batches; `walk stats` must report no invariant warnings.
The same check is available on Windows with `Stop-Process -Force`.

A fully automated child-process crash test is intentionally not part of the
default suite because it is timing sensitive on CI; the property test and
concurrency test cover the same transactional/invariant code paths.
