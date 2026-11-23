CREATE (a:Person {name: "Alice", age: 39}), (b:Person {name: "Bob", age: 27}), (c:Person {name: "Carol", age: 39}), (a)-[:KNOWS {since: 2020}]->(b), (b)-[:KNOWS]->(c)
