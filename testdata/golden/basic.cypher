MATCH (a:Person)-[:KNOWS]->(b) RETURN a.name AS a, b.name AS b ORDER BY a
