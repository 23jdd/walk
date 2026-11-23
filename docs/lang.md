# Cypher subset grammar (v1)

This is the exact grammar accepted by `parser.Parse`. It is a hand-written
recursive-descent parser; error messages carry 1-based line and column and a
caret under the offending token.

```ebnf
query        := [ "EXPLAIN" ] clause+ [ ";" ] ;

clause       := match | where | create | set | remove | delete | return ;
match        := "MATCH" patternList ;
where        := "WHERE" expr ;
create       := "CREATE" patternList ;
set          := "SET" setItem ( "," setItem )* ;
remove       := "REMOVE" removeItem ( "," removeItem )* ;
delete       := [ "DETACH" ] "DELETE" ident ( "," ident )* ;
return       := "RETURN" [ "DISTINCT" ] ( "*" | returnItem ( "," returnItem )* )
                [ "ORDER" "BY" orderItem ( "," orderItem )* ]
                [ "SKIP" expr ] [ "LIMIT" expr ] ;

patternList  := pattern ( "," pattern )* ;
pattern      := [ ident "=" ] patternBody ;
patternBody  := path | shortestPath ;
shortestPath := "shortestPath" "(" path ")" ;
path         := node ( relationship node )* ;

node         := "(" [ ident ] ( ":" ident )* [ props ] ")" ;
relationship := [ "<" ] "-" [ "[" [ ident ] [ ":" relType ( "|" relType )* ]
                [ varlength ] [ props ] "]" ] "-" [ ">" ] ;
varlength    := "*" [ int [ ".." [ int ] ] | ".." [ int ] ] ;
relType      := ident ;
props        := "{" [ propEntry ( "," propEntry )* ] "}" ;
propEntry    := ( ident | string ) ":" expr ;

setItem      := ident "." ident "=" expr
              | ident "=" expr
              | ident "+=" expr
              | ident ( ":" ident )+ ;
removeItem   := ident "." ident | ident ( ":" ident )+ ;

returnItem   := expr [ "AS" ident ] ;
orderItem    := expr [ "ASC" | "DESC" ] ;

expr         := orExpr ;
orExpr       := andExpr ( "OR" andExpr )* ;
andExpr      := notExpr ( "AND" notExpr )* ;
notExpr      := "NOT" notExpr | comparison ;
comparison   := additive ( ( "=" | "<>" | "!=" | "<" | "<=" | ">" | ">=" ) additive
                         | "STARTS" "WITH" additive
                         | "ENDS" "WITH" additive
                         | "CONTAINS" additive
                         | "IS" [ "NOT" ] "NULL" )* ;
additive     := multiplicative ( ( "+" | "-" ) multiplicative )* ;
multiplicative := unary ( ( "*" | "/" | "%" ) unary )* ;
unary        := ( "-" | "+" ) unary | postfix ;
postfix      := primary ( "." ident )* ;
primary      := int | float | string | "NULL" | "TRUE" | "FALSE"
              | "(" expr ")" | map
              | ident [ "(" [ "DISTINCT" ] [ "*" | expr ( "," expr )* ] ")" ] ;
map          := "{" [ propEntry ( "," propEntry )* ] "}" ;
```

## Direction tokens

| Source | Meaning |
|---|---|
| `(a)--(b)` | undirected |
| `(a)-->(b)` | outgoing from `a` |
| `(a)<--(b)` | incoming to `a` |
| `(a)-[:T]->(b)` | typed, outgoing |
| `(a)-[:T\|U]->(b)` | multiple types |
| `(a)-[r:T {p: 1}]->(b)` | bind edge `r` and filter its props |
| `(a)-[:T*1..3]->(b)` | variable length 1..3 |
| `(a)-[:T*]->(b)` | variable length 1..8 (default cap) |
| `p = (a)-[:T*..3]->(b)` | bind the path to `p` |
| `p = shortestPath((a)-[:T*..3]->(b))` | BFS shortest path |

## Examples

```cypher
CREATE (a:Person {name: "Alice", age: 39}),
       (b:Person {name: "Bob", age: 27}),
       (a)-[:KNOWS {since: 2020}]->(b);

MATCH (n:Person)
WHERE n.age > 30 AND n.name STARTS WITH "A"
RETURN n.name AS name, n.age
ORDER BY age DESC
SKIP 0 LIMIT 10;

MATCH (a:Person {name: "Alice"})-[:KNOWS*1..3]->(x)
RETURN DISTINCT x.name AS name, count(*) AS paths
ORDER BY paths DESC;

MATCH p = shortestPath((a:Person {name: "Alice"})-[:KNOWS*..8]->(b:Person {name: "Bob"}))
RETURN length(p) AS hops;

MATCH (n:Person) SET n.age = n.age + 1, n:Active RETURN n;
MATCH (n:Person) REMOVE n.age, n:Active;
MATCH (n:Person) DETACH DELETE n;

EXPLAIN MATCH (n:Person) WHERE n.name = "Alice" RETURN n;
```

## Strings, identifiers

* Single or double quotes; escapes `\n \r \t \\ \' \" \uXXXX`.
* Backtick identifiers: `` `weird name` ``.
* Keywords are case-insensitive; identifiers, labels and relationship types
  are case-sensitive.
* `//` line comments and `/* ... */` block comments.

## Deliberately unsupported (v1)

`OPTIONAL MATCH`, `WITH`, `UNWIND`, `CALL`, `MERGE`, `UNION`, list literals,
list comprehensions, `COLLECT`, nested maps, `IN`, parameter syntax `$x`,
`CASE`, `EXISTS`, and `p = shortestPath(...)` inside `RETURN`. See
`docs/cypher-deviations.md` and `docs/neo4j-skip-list.md`.
