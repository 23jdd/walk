# Query planner and EXPLAIN

The v1 planner is a two-level heuristic planner. It is intentionally small and
deterministic; every decision is visible in `EXPLAIN` output.

## Inputs

* ordered `MATCH` patterns from the AST
* the optional `WHERE` expression
* live statistics: `nodes.KeyN`, `edges.KeyN`, `node_label` prefix counts,
  relationship-type counts, declared F-25 indexes
* limits: `--max-solutions`, `--max-depth`, `--max-detach`

## Output

An ordered list of operator closures (`Find`, `Hop`, `VarLen`, `Shortest`,
`Keep`, `New`, `Link`, `Set*`, `Del`, `Give`, `Distinct`, `Aggregate`, `Order`,
`Skip`, `Limit`) plus a text plan.

## Anchor selection

For each path pattern the planner scores every node:

| condition | score |
|---|---|
| variable already bound by an earlier pattern | +100 |
| has labels | +10 + number of labels |
| has inline properties | +5 |
| is an interior node of the chain | −20 |

The highest-scoring node is the anchor. If it is the right endpoint of a chain
and the pattern has no path variable, the chain is reversed (relationship
directions `out`/`in` are swapped, `both` is unchanged) so expansion always
moves away from the anchor. A path variable forces a left anchor so the path
order remains the source order.

Within `Find`, the candidate source is chosen in this order:

1. F-25 property-index point lookup for the first label with an indexed
   equality property;
2. the smallest `node_label` posting list among the labels;
3. full `nodes` bucket scan (with a warning) when there is no label.

This is why `Find` never does a full table filter when an anchor index exists.

## Traversal direction

For a single hop, the direction comes from the pattern. When the destination is
already bound, the planner expands from the source and filters the endpoint by
identity; when the source is bound and the destination is not, the hop binds the
destination directly. For undirected expansion the executor merges `adj_out`
and `adj_in` scans and deduplicates by edge id, which also makes self-loops
produce exactly one result per hop.

## Filters

* Inline node properties on the anchor become `PropPred` equality tests inside
  `Find`; on non-anchor nodes they become an in-stream `Keep`.
* Relationship properties are evaluated after the hop by an in-stream `Keep`
  over the bound edge.
* `WHERE` is applied after all `MATCH` patterns, matching Cypher's scope. The
  operator API allows callers to insert `Keep` between hops for explicit
  push-down; the Cypher front-end does not rewrite `WHERE` into the pattern yet.

## EXPLAIN format

```
EXPLAIN  operators=N
#    OPERATOR     DETAIL                                       BUCKET         PREFIX           EST
1    Find         n:Person                                     node_label     Person           2
2    Hop          out KNOWS                                    adj_out                        0
3    Keep         WHERE filter
4    Give         2 column(s)
5    Order        1 key(s)
```

`EST` for a `Find` is the minimum label count (or node count for an unlabelled
find). `0` means "not estimated at plan time". Plans are generated without
draining any operator, so `EXPLAIN` never writes or emits rows.

## Determinism

All scans are key-ordered. Neighbours are sorted by edge id; `ORDER BY` uses a
stable sort; `LIMIT` stops upstream. The same database state therefore produces
the same result order across runs, which the golden tests rely on.
