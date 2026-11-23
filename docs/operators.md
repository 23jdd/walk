# Operator Specification and Implementation Details (v1)

This document is the implementation contract for the `walk` executor. It is the
Markdown form of `docs/算子规格与实现细节.pdf`. Where earlier drafts disagree,
the decision table in §7 wins (notably the `null` ordering rule).

## 1. Data model and core contracts

### 1.1 bbolt bucket layout

All multi-field keys concatenate fixed-width big-endian fields so that prefix
scans are correct:

```
nodes       : nodeID                    -> JSON {labels:[...], props:{...}}
edges       : edgeID                    -> JSON {type:string, from:nodeID, to:nodeID, props:{...}}
node_label  : labelID(8) | nodeID(8)    -> nil                     (F-03)
adj_out     : from(8) | type(8) | edge(8) -> to(8)                 (F-02)
adj_in      : to(8)   | type(8) | edge(8) -> from(8)
reltypes    : typeName                  -> typeID
reltype_by_id: typeID(8)                -> typeName
edge_type   : typeID(8) | edgeID(8)     -> nil                     (type -> edge)
prop_idx    : labelID(8) | propLen(2) | prop | encVal | nodeID(8) -> nil  (F-25)
meta        : index definitions and schema version
```

Key points:

* Fixed-width adjacency keys make `Seek([from])` scan all outgoing edges and
  `Seek([from|type])` scan one relationship type without touching `edges`.
* An unknown relationship type on the read path yields an empty stream
  (schema-less, same as Neo4j). On the write path it allocates a new type id.
  This asymmetry is intentional and documented.
* Labels and relationship types are case-sensitive (`Person != person`).

### 1.2 Core types

```go
type Kind uint8
const (NodeKind Kind = iota; EdgeKind; PathKind)

type Entity struct { Kind Kind; ID uint64; Path *Path }
type Solution map[string]Entity        // variable -> entity
type Row      []any                    // projected row
type Stream    = iter.Seq[Solution]
type RowStream = iter.Seq[Row]
type QueryError struct { Span parser.Span; Msg string } // only error type
```

### 1.3 Three global disciplines

1. **Clone before branching.** Any operator that emits multiple downstream
   solutions for one upstream solution must `CloneSolution` first. Locked by
   `TestExec_NoAliasOnBranch`.
2. **`yield false` means full stop.** A downstream stop (`LIMIT`, user break)
   must unwind every loop and terminate upstream work.
3. **Fail fast.** The executor stores the first error in `Exec.Err()`; every
   operator stops producing. There is no panic/recover and no channel in the
   execution path.

### 1.4 Determinism

Tests rely on the following order:

* Node candidates: `node_label` scan by nodeID ascending; unlabelled `find`
  scans the `nodes` bucket by key ascending.
* Adjacency expansion: edgeID ascending.
* VarLen: DFS with neighbours in edgeID order.
* ORDER BY: `slices.SortStableFunc`, equal keys preserve input order.

## 2. Execution model

```
AST --compile--> []Operator closures --assemble--> Solution stream
    --give--> Row stream --> Result
```

One query is one bbolt transaction. The compiler detects `New/Link/Set/Del` and
chooses `Update` (serial writes) or `View` (concurrent reads). A mid-stream
fail-fast error makes `Update` roll back, so write queries have whole-query
atomicity for free.

Compile-time checks: variable must be bound before reference, no duplicate
`as` binding, Hop source must be a node, VarLen does not support `via`, ranges
must satisfy `1 <= lo <= hi`, `skip/limit >= 0`, `give` must follow write
clauses, aggregates only in `give`, string predicates require string operands.

`Result` has two shapes:

```go
type Result struct {
    Columns []string
    Rows    [][]any
    Summary *WriteSummary // nodes_created/edges_deleted/...
}
```

## 3. Solution-stream operators

Uniform shape: `func (e *Exec) Xxx(in Stream, ...) Stream`;
`Find`, `New`, `Shortest` have no upstream input.

### 3.1 Find (anchor)

Signature: `Find(labels []string, props []PropPred, as string) Stream`.

Semantics: emit one `{x: node}` solution per node carrying all labels and all
inline equality properties. Unlabelled find is legal (full scan, warning).

Algorithm:

1. If labels are non-empty:
   * if a declared F-25 index exists for `(labels[0], prop)` and the value is a
     literal, point-lookup the index (`O(log n)`);
   * otherwise scan the smallest label posting list and verify the remaining
     labels and inline properties.
2. Otherwise scan the whole `nodes` bucket.

Unknown label = empty stream, no error. Inline property values must be literals.

Tests: `TestFind_LabelScan`, `TestFind_MultiLabelAND`,
`TestFind_IndexFastPath`, `TestFind_UnknownLabelEmpty`.

### 3.2 Hop (single traversal)

Signature: `Hop(in Stream, src string, dir Dir, typeIDs []uint64, via, as string) Stream`.

For each upstream solution take `sol[src]` (must be a node) and scan the
adjacency bucket in the requested direction. Every matching neighbour produces
a cloned solution with the endpoint bound to `as` (if given) and the traversed
edge bound to `via` (if given).

* `out`: `Seek adj_out [from|type]`, yields `(edge,to)`.
* `in`: `Seek adj_in [to|type]`, yields `(edge,from)`.
* `both`: merge both scans and deduplicate by edgeID (self-loops appear twice).

A solution with no neighbour dies. A bare Hop is therefore a legal existence
filter. Source bound to an edge is a compile error. Empty type list = all types.

Tests: `TestHop_Out`, `TestHop_In`, `TestHop_BothDedupSelfLoop`,
`TestHop_NoNeighborKillsSolution`, `TestHop_NoAliasOnBranch`.

### 3.3 VarLen (variable-length paths)

Signature: `VarLen(in Stream, src string, dir Dir, typeIDs []uint64, lo, hi int, as string) Stream`.

Enumerate every path starting at `src` with length `k in [lo,hi]`; within one
path each edge may appear at most once (relationship uniqueness), while nodes
may repeat. Each path endpoint produces `{as: endpoint}`. Intermediate nodes are
not bound and paths are not produced unless a path variable is requested.

```go
func dfs(cur uint64, depth int, used map[uint64]bool) {
    if depth >= lo {
        next := CloneSolution(sol); next[as] = NodeEntity(cur)
        if !yield(next) { return } // emit, then keep exploring
    }
    if depth == hi { return }
    for _, nb := range neighbors(cur) { // edgeID ascending
        if used[nb.EdgeID] { continue } // relationship uniqueness
        used[nb.EdgeID] = true
        dfs(nb.To, depth+1, used)
        used[nb.EdgeID] = false         // backtrack
    }
}
```

Boundaries and cost:

* `lo >= 1` is enforced; `0..` is rejected to avoid self-match ambiguity.
* `*` is capped at 8 by default (`--max-depth`).
* Worst-case complexity is exponential in `hi`; edge uniqueness plus the cap
  keeps it practical. Documented as a cost in the paper.
* `via` is not supported (ambiguous with multiple edges).
* Path values `p` and `length(p)` are supported.

Tests: `TestVarLen_ExactN`, `TestVarLen_RangeEmitsEveryLength`,
`TestVarLen_EdgeUniqueness`, `TestVarLen_NodeRevisitAllowed`,
`TestVarLen_Cap8`, `TestVarLen_UnreachableEmpty`.

### 3.4 Shortest (BFS)

Signature: `Shortest(in Stream, src, dst string, dir Dir, typeIDs []uint64, maxDepth int, as, pathAs string) Stream`.

The right find is evaluated once into a set of destination ids (the Cypher
front-end may bind both endpoints first). For each start solution run BFS over
all requested types, expanding both adjacency buckets. The first time a
destination is reached it is emitted; unreachable pairs are silently dropped
(non-OPTIONAL semantics). Parent pointers are kept so a deterministic path can
be returned; equal-length ties are resolved by edgeID order.

Tests: `TestShortest_SinglePair`, `TestShortest_MultiEnd`,
`TestShortest_UnreachableDropped`, `TestShortest_EarlyExit`,
`TestShortest_BidirectionalAllTypes`.

### 3.5 New

Signature: `New(in Stream, labels []string, props map[string]Value, as string) Stream`.

For every upstream solution create one node and bind it to `as`; as the first
clause (`in == nil`) execute exactly once. Properties are literals in v1.
Storage: `nodes.NextSequence()` -> `Put` JSON -> one `node_label` entry per
label; `nodes_created++`.

Cost warning: `find ... new :Log` creates one node per match. This is useful
per-match write semantics but scales with the number of solutions.

Tests: `TestNew_Basic`, `TestNew_PerSolution`, `TestNew_LabelInverted`.

### 3.6 Keep (predicate)

Signature: `Keep(in Stream, ex ast.Expr) Stream`.

Evaluate `ex` per solution. `true` passes; `false` and `null` are dropped; a
type error aborts the whole query. Null comparisons are false (F-12 deviation).
Predicate push-down is explicit: placing `keep` between two hops prunes early;
no optimizer is required.

Tests: `TestKeep_Comparison`, `TestKeep_NullDrops`, `TestKeep_CrossVar`,
`TestKeep_LogicOr`, `TestKeep_TypeErrorAborts`.

### 3.7 Link

Signature: `Link(in Stream, a, type, b string, props []PropLit, via string) Stream`.

Both endpoints must be nodes. Create an edge and write four places: the `edges`
record, `adj_out`, `adj_in`, and the type registry/index. Unknown type allocates
a new typeID. The original solution is passed through; `via r` additionally binds
the new edge. Self-loops (`a == b`) and parallel edges are allowed (multigraph).

Tests: `TestLink_FourWrites`, `TestLink_ViaBinding`, `TestLink_TypeAllocated`,
`TestLink_SelfLoop`.

### 3.8 Set (property / label)

Signatures: `SetProp(in, v, prop, ex)`, `SetLabel(in, v, label)`.

`SetProp` lazily decodes `props`, evaluates the expression (which may reference
bound variables), and writes back the JSON; `= null` deletes the property.
`SetLabel` appends to the labels array and writes `node_label`; if `(label,
prop)` has an F-25 index, entries for existing properties of the node must be
backfilled, otherwise an indexed `find :L {p=v}` would miss it.

Tests: `TestSet_PropOverwrite`, `TestSet_NullDeletesProp`,
`TestSet_LabelAdds`, `TestSet_LabelMaintainsIndex`,
`TestSet_ExprRefersBoundVar`.

### 3.9 Del (delete node / cascade)

Signature: `Del(in Stream, v string) Stream` (the Cypher front-end also exposes
`DELETE` without cascade and `DETACH DELETE`).

Cascade algorithm for a node:

1. Scan `adj_out [x|]`, collect `(type,edge,to)`, delete each
   `adj_in[to|type|edge]` and `edges[edge]`.
2. Scan `adj_in [x|]`, collect `(type,edge,from)`, delete each
   `adj_out[from|type|edge]` and `edges[edge]`.
3. Read `nodes[x]`, delete each `node_label` entry.
4. Delete `prop_idx` entries for indexed properties.
5. Delete `nodes[x]`.

The cursor must collect keys before deleting to avoid invalidating the seek
position. `edges_deleted` / `nodes_deleted` are counted separately.

After a delete, later solutions that still reference the entity read its
properties as `null` (no tombstone). Prefer ending a query with `del`.

Tests: `TestDel_NodeCascade`, `TestDel_EdgeOnly`, `TestDel_Counters`,
`TestDel_AtomicallyRolledBack`.

## 4. Boundary operator: Give

Signature: `Give(in Stream, items []Projection, opts GiveOptions) RowStream`.

Item forms: `<expr> [as name]`, `count(*)`, `count(expr)`, `sum`, `avg`, `min`,
`max`. Column names default to the source text (e.g. `p.name`, `count(*)`) and
use the alias when present. Each solution produces one row; missing properties
are `null`; runtime type errors abort the query.

If any aggregate is present, `Give` evaluates values per solution and hands
them to the aggregate stage; rows are produced by the aggregate stage. Pure
projection produces rows directly.

Tests: `TestGive_RowAlignment`, `TestGive_NullFillsMissingProp`,
`TestGive_TypeErrorAborts`, `TestGive_ColumnNames`.

## 5. Row-stream operators

Uniform shape: `func Xxx(in RowStream, ...) RowStream`.

### 5.1 Distinct

Whole-row deduplication. `canonicalEncode(v)` = type tag + value
(`N`, `B0/B1`, `I8B`, `Fbits`, `S<len>utf8`). Type-strict: `int 1` and
`float 1.0` are different rows. `null` equals `null` (same as SQL).

Tests: `TestDistinct_Basic`, `TestDistinct_NullsMerge`,
`TestDistinct_IntVsFloat`.

### 5.2 Aggregate

Grouping key = canonical encoding of all non-aggregate projection values. One
row per group (keys + aggregate values). SQL semantics:

| function | all-null group / empty group | null handling |
|---|---|---|
| `count(*)` | 0 | counts all rows |
| `count(expr)` | 0 | skips null |
| `sum` | null (SQL) | skips null; non-numeric aborts |
| `avg` | null (SQL) | skips null; result is float64 |
| `min`/`max` | null | skips null |

Empty input with no grouping keys produces one row (`count = 0`, others
null). `avg` uses float64; int64 overflow wraps silently (documented).

Tests: `TestAgg_CountStarEmpty`, `TestAgg_GroupBy`,
`TestAgg_CountSkipsNull`, `TestAgg_SumTypeErrorAborts`, `TestAgg_AvgFloat`.

### 5.3 Order

Multi-key stable sort. Full order:

1. Type rank: `null=0 < bool=1 < number=2 < string=3`.
2. Within rank: `false < true`; numbers compare numerically across int/float;
   strings compare by UTF-8 bytes.
3. `desc` reverses the comparison for that key, so null sorts last under
   `desc` (natural consequence, documented).

Implementation: materialise all rows, `slices.SortStableFunc`. Memory equals
the row set size.

Tests: `TestOrder_MultiKey`, `TestOrder_NullSmallest`,
`TestOrder_IntFloatIntercompare`, `TestOrder_Stable`.

### 5.4 Skip / Limit

`skip` discards the first n rows. `limit` forwards n rows then returns, which
really terminates the pull-based upstream (free performance win). Fixed order:
`order -> skip -> limit`.

Tests: `TestLimit_EarlyStopsUpstream`, `TestSkip`, `TestSkipOrder`.

## 6. Shared components

### 6.1 Expression evaluator

`eval(sol, expr) (any, error)` is shared by Keep / Give / Set.

Precedence: `or < and < not < comparison < additive < multiplicative < unary <
postfix`. Comparison: `= != < <= > >= CONTAINS STARTS WITH ENDS WITH`, plus
`IS [NOT] NULL`. Null comparisons return false; `IS NULL` is the only way to
observe null. Type errors abort the query (`p.age > "30"`, `sum(p.name)`).
`contains/starts/ends` require strings on both sides.

### 6.2 Lazy property loader

`f.age` triggers `nodes.Get(id)` + JSON decode once per `(kind,id)` per
execution. Deleted entity -> all properties null. Edge kind reads `edges`.

### 6.3 Candidate locator

`Find`'s internals: label prefix scan + F-25 point lookup + inline filtering.
Implemented in `executor.Find` and `executor.findCandidateIDs`.

## 7. Decision table (final)

| # | Decision | Final choice | Note |
|---|---|---|---|
| D1 | Order null position | rank 0: first asc, last desc | supersedes earlier drafts |
| D2 | Cross-type order | null < bool < number < string | within-rank rules in §5.3 |
| D3 | distinct int vs float | type-strict, not merged | order compares numerically |
| D4 | tied shortest paths | v1 does not expose paths, so no ambiguity | future: path()/length() by edgeID |
| D5 | VarLen lower bound | `>= 1`; `*` = 1..8; `0..` rejected | compile error |
| D6 | VarLen repeated endpoint | multiple paths -> multiple solutions | dedupe with give distinct |
| D7 | unknown label/type on read | empty stream, no error | writes allocate new ids |
| D8 | shortest unreachable | silently drop the pair | non-OPTIONAL |
| D9 | aggregate empty stream, no keys | one row (count=0, etc.) | SQL semantics |
| D10 | sum/avg all-null group | null; non-null non-numeric aborts | fail-fast |
| D11 | expression type error | abort whole query, never null | F-12 deviation |
| D12 | reference after delete | properties null, no tombstone | prefer ending with del |
| D13 | write query atomicity | whole query in one bbolt Update | rollback on fail-fast |
| D14 | bare Hop (no as) | legal existence filter | documented |
| D15 | VarLen via / expression props | Scope Out | future work |

## 8. Test matrix

Common hooks: `collect(stream) []Solution` + `reflect.DeepEqual` for exact
solution sets; `collectRows` for Row operators. The three discipline tests must
exist first:

1. `TestExec_NoAliasOnBranch` — clone before branch.
2. `TestLimit_EarlyStopsUpstream` — pull-based early stop.
3. `TestF08_ErrorPosition` — span/line/column diagnostics.
