# walk Cypher 语法大全

本文是 `walk` 数据库**实际支持**的 Cypher 子集完整参考。它不是
openCypher 全集的承诺；明确不支持的部分列在最后一节，并与
[`docs/lang.md`](lang.md)（英文 EBNF）和
[`docs/cypher-deviations.md`](cypher-deviations.md)（语义偏差）保持一致。

---

## 1. 一条语句的结构

```cypher
[EXPLAIN]
[MATCH pattern (, pattern)* [WHERE expr]]
[CREATE pattern (, pattern)*]
[SET item (, item)*]
[REMOVE item (, item)*]
[DELETE var (, var)* | DETACH DELETE var (, var)*]
[RETURN [DISTINCT] item (, item)*
        [ORDER BY key (, key)*]
        [SKIP int]
        [LIMIT int]]
[;]
```

* 一条语句只能包含一个 `RETURN`。
* `WHERE` 可以出现在 `MATCH` 之后；多个 `WHERE` 会按 `AND` 合并。
* `MATCH`、`CREATE`、`SET`、`REMOVE`、`DELETE` 可以组合在一条语句中，
  执行器按“匹配 → 过滤 → 创建 → 更新 → 删除 → 投影”的顺序执行。
* 写语句可以没有 `RETURN`；此时只返回 `summary` 计数。
* 读语句可以省略 `RETURN`（会消费结果但不输出列），但通常应写 `RETURN`。
* `EXPLAIN` 必须是第一个关键字；只输出计划，不执行语句。
* 末尾允许一个分号，不支持一次提交多条语句。

### 示例

```cypher
MATCH (n:Person)
WHERE n.age >= 18
RETURN n.name AS name, n.age
ORDER BY age DESC
SKIP 0 LIMIT 20;

CREATE (a:Person {name: "Alice"}), (b:Person {name: "Bob"}),
       (a)-[:KNOWS {since: 2020}]->(b);

MATCH (a:Person {name: "Alice"})
SET a.age = 40, a:Admin
RETURN a;

MATCH (n:Person) DETACH DELETE n;
```

---

## 2. 词法规则

| 元素 | 语法 |
|---|---|
| 关键字 | 大小写不敏感：`MATCH`/`match` 等价 |
| 变量、标签、关系类型、属性名 | 大小写敏感；字母/下划线/数字/非 ASCII，不能以数字开头 |
| 反引号标识符 | `` `weird name` ``；内部反引号写成 ``` `` ``` |
| 字符串 | 单引号或双引号；支持 `\n \r \t \\ \' \" \uXXXX` |
| 整数 | 十进制，如 `42`、`-7` |
| 浮点数 | 如 `1.5`、`2e3`；科学计数法会推断为 float |
| 布尔 | `true` / `false`（大小写不敏感） |
| null | `NULL`/`null` |
| 注释 | `// 行注释` 与 `/* 块注释 */` |

字符串内支持同引号双写转义：`'it''s'`。

---

## 3. 节点模式

```cypher
(n)                         // 任意节点，绑定变量 n
(:Person)                   // 任意 Person 节点，不绑定变量
(n:Person)                  // 绑定并限制标签
(n:Person:Employee)         // 多标签 AND
(n {name: "Alice"})         // 内联属性等值
(n:Person {name: "Alice", age: 39})
(:Person {name: "Alice"})   // 匿名 + 标签 + 属性
```

* 多标签表示“同时具有所有标签”。
* 模式中的属性值必须是**字面量**（数字/字符串/布尔/null）；不支持在
  `MATCH`/`CREATE` 的 `{...}` 中引用变量或写表达式。
* 一个模式变量只能绑定一次；同一语句中重复出现 `(n)` 表示引用同一节点。
* 没有标签、没有属性、也没有已绑定变量的 `MATCH (n)` 会全表扫描，并在
  `warnings` 中提示。

### 多个模式

`MATCH` 后用逗号分隔多个 pattern；多个 `MATCH` 子句等价于把它们依次连接：

```cypher
// 共享变量 b，形成连接
MATCH (a:Person)-[:KNOWS]->(b), (b)-[:LIKES]->(c)
RETURN a.name, c.name;

// 不共享变量，形成笛卡尔积
MATCH (a:Person), (b:City)
RETURN a.name, b.name;

MATCH (a:Person)
MATCH (b:City)
RETURN count(*) AS pairs;
```

规划器会为每个 pattern 选择锚点；没有共享变量的 pattern 通过 CrossJoin
组合，结果集可能很大，建议用 `WHERE` 或 `LIMIT` 限制。

---

## 4. 关系模式

### 4.1 方向与变量

```cypher
(a)--(b)                    // 无向
(a)-->(b)                   // a -> b
(a)<--(b)                   // b -> a
(a)-[r]->(b)                // 绑定关系变量 r
(a)-[r]-(b)                 // 无向 + 绑定 r
(a)-[:KNOWS]->(b)           // 指定类型
(a)-[:KNOWS|LIKES]->(b)     // 多类型（任一匹配）
(a)-[r:KNOWS|LIKES]->(b)    // 多类型 + 绑定 r
(a)-[:KNOWS {since: 2020}]->(b)
(a)-[r:KNOWS {since: 2020}]->(b)
```

* `-[...]-`、`-[...]->`、`<-[...]-` 都支持。
* 多类型用 `|` 分隔；空类型列表表示所有类型。
* 关系属性在匹配阶段按等值过滤，值必须是字面量。
* 平行边（同一对节点间多条同类型边）是合法的；每条边是独立结果。
* 自环（`a = b`）合法；无向遍历时通过 `edgeID` 去重，只产生一次。

### 4.2 变长路径

```cypher
(a)-[:KNOWS*]->(b)          // 1..8（默认上限）
(a)-[:KNOWS*2]->(b)         // 恰好 2 条边
(a)-[:KNOWS*1..3]->(b)      // 1 到 3 条边
(a)-[:KNOWS*..3]->(b)       // 1 到 3 条边
(a)-[:KNOWS*2..]->(b)       // 2 到默认上限
(a)-[*1..2]->(b)            // 所有关系类型
(a)-[*]-(b)                 // 无向变长
```

* 变长路径对**每条路径**保证关系唯一性：同一条边不能在同一条路径中重复；
  节点可以重复（菱形、环图会产生多条路径）。
* 每个路径终点产出一个解；同一终点经不同边集到达会产生多个解，需要去重
  时使用 `RETURN DISTINCT`。
* 下界必须 `>= 1`；`*0..n` 会被拒绝。
* 上界取 `min(显式上界, --max-depth)`，默认 `--max-depth=8`。
* 变长关系不支持 `via`/关系变量绑定（多边绑定有歧义），也不支持在变长
  关系上写属性过滤。

### 4.3 路径变量

```cypher
MATCH p = (a:Person)-[:KNOWS*1..3]->(b)
RETURN p, length(p) AS hops;
```

* `p` 绑定整个路径；`RETURN p` 序列化为节点/边摘要数组。
* `length(p)` 返回路径上的边数。
* 单跳路径也支持：`MATCH p = (a)-[:KNOWS]->(b) RETURN length(p)`。

### 4.4 最短路径

```cypher
MATCH p = shortestPath((a:Person {name: "Alice"})-[:KNOWS*..8]->(b:Person {name: "Bob"}))
RETURN length(p) AS hops, p;

MATCH p = shortestPath((a)-[*]-(b))
RETURN length(p);
```

* `shortestPath(...)` 只能作为 `MATCH` 的模式体，且必须恰好包含两个节点和
  一条**变长**关系。
* 使用 BFS；首次到达终点即停止。存在多条并列最短路径时取 BFS 访问顺序
  中先到的一条，结果确定。
* 不可达的起终点对静默丢弃（非 OPTIONAL 语义）。
* `shortestPath` 不在 `RETURN` 中作为普通函数使用。

---

## 5. 表达式

### 5.1 优先级（从低到高）

```text
OR
AND
NOT
=  <>  !=  <  <=  >  >=
STARTS WITH / ENDS WITH / CONTAINS
IS NULL / IS NOT NULL
+  -
*  /  %
一元 - / +
属性访问 .
函数调用 / 括号
```

### 5.2 字面量与属性访问

```cypher
null
true
false
42
-7
3.14
"Alice"
'Alice'
n.name
r.since
```

属性不存在时返回 `null`；实体已被删除时属性读取也是 `null`。

### 5.3 比较与逻辑

```cypher
n.age = 39
n.age <> 30
n.age != 30
n.age < 40
n.age <= 40
n.age > 18
n.age >= 18
n.name STARTS WITH "Al"
n.name ENDS WITH "ce"
n.name CONTAINS "lic"
n.age IS NULL
n.age IS NOT NULL
NOT n.age > 30
n.age > 18 AND n.name = "Alice"
n.age < 18 OR n.age > 65
(n.age > 18 OR n.verified = true) AND n.active = true
```

* 数字之间 `int64`/`float64` 可以跨类型比较。
* 字符串按 UTF-8 字节序比较。
* **null 参与比较一律返回 `false`**（不是三值逻辑）；判断缺失属性只能用
  `IS NULL` / `IS NOT NULL`。
* 类型不匹配（例如 `n.age > "30"`）会中止整条查询，而不是返回 null。
* 布尔运算中 null 按 `false` 处理。

### 5.4 算术

```cypher
n.age + 1
n.age - 1
n.age * 2
n.age / 2
n.age % 10
-n.age
```

* `int + int` 等仍为 int；任一侧为 float 则结果为 float。
* `/` 与 `%` 总是返回 float；除数为 0 会报错。
* int64 溢出会静默回绕（文档化行为）。

### 5.5 函数

| 函数 | 说明 |
|---|---|
| `length(p)` | 路径边数；参数必须是路径 |
| `id(n)` / `id(r)` | 节点/边 id |
| `toString(v)` | 转为字符串 |
| `toInteger(v)` | 转为整数 |
| `toFloat(v)` | 转为浮点数 |
| `count(*)` | 聚合：行数（只能在 `RETURN`） |
| `count(expr)` | 聚合：非 null 数量（实体变量也支持） |
| `sum(expr)` | 聚合：数值求和 |
| `avg(expr)` | 聚合：平均值，返回 float64 |
| `min(expr)` | 聚合：最小值 |
| `max(expr)` | 聚合：最大值 |

聚合函数不能出现在 `WHERE`、`SET` 或 `CREATE` 中；聚合只能位于
`RETURN` 的顶层投影项。

---

## 6. RETURN 投影

### 6.1 基本形式

```cypher
RETURN n
RETURN n.name
RETURN n.name AS name
RETURN n, r, p
RETURN *
RETURN DISTINCT n.age
```

* `RETURN *` 返回所有可见绑定变量，按绑定顺序排列；匿名节点不会出现。
* 变量投影（节点/边/路径）返回结构化摘要；属性投影返回标量。
* 列名默认使用源码文本（如 `n.name`、`count(*)`），`AS` 可覆盖。
* `RETURN DISTINCT` 按整行去重；`int(1)` 与 `float(1.0)` 视为不同行，
  两个 null 视为相同。

### 6.2 聚合与隐式分组

```cypher
MATCH (n:Person)
RETURN count(*) AS total;

MATCH (n:Person)
RETURN n.age AS age, count(*) AS c, avg(n.score) AS avg_score
ORDER BY age;

MATCH (n:Person)
RETURN sum(n.age) AS total_age, min(n.age) AS youngest, max(n.age) AS oldest;
```

* 所有非聚合投影项自动成为分组键。
* 空输入且没有分组键时，聚合返回一行：`count=0`，`sum/avg/min/max=null`。
* 空输入且有分组键时返回 0 行。
* `sum`/`avg` 跳过 null；全是 null 时返回 null；遇到非数值会中止查询。
* `count(*)` 统计所有行；`count(expr)` 跳过 null。
* `count(DISTINCT expr)` 目前按 `count(expr)` 处理（见偏差文档）。

### 6.3 ORDER BY / SKIP / LIMIT

```cypher
RETURN n.name AS name, n.age AS age
ORDER BY age DESC, name ASC
SKIP 10
LIMIT 5;
```

* 多键排序，可混合 `ASC`/`DESC`；省略方向时默认 `ASC`。
* 全序：`null < bool < number < string`；布尔 `false < true`；数字跨
  int/float 比较；字符串按 UTF-8 字节序。
* 排序是稳定的：所有键相等的行保持输入顺序。
* `DESC` 反转整个键，因此 null 在降序时排在最后。
* `SKIP`/`LIMIT` 必须是整数常量，不能为负；`LIMIT 0` 不输出行并尽早停止。
* 固定执行顺序：`聚合 → 投影 → DISTINCT → ORDER BY → SKIP → LIMIT`。

---

## 7. CREATE

```cypher
CREATE (n:Person {name: "Alice", age: 39});

CREATE (a:Person {name: "Alice"}),
       (b:Person {name: "Bob"}),
       (a)-[:KNOWS {since: 2020}]->(b);

MATCH (a:Person {name: "Alice"})
CREATE (a)-[:KNOWS]->(b:Person {name: "Bob"});
```

* 支持节点、关系、多 pattern、自环、平行边。
* `CREATE` 中未绑定的变量会创建新节点/边；已绑定的变量直接复用。
* 关系类型必须显式给出，且 `CREATE` 中一条关系只能有一个类型。
* 属性值只能是字面量；`null` 属性不会被写入。
* 作为首个子句时只执行一次；放在 `MATCH` 后时为每个上游解执行一次。
* 写操作与整条查询在同一 bbolt 事务中，出错全部回滚。

---

## 8. SET / REMOVE

```cypher
MATCH (n:Person {name: "Alice"})
SET n.age = 40;

MATCH (n:Person {name: "Alice"})
SET n.age = n.age + 1;

MATCH (n:Person {name: "Alice"})
SET n.age = null;                  // 删除属性

MATCH (n:Person {name: "Alice"})
SET n = {name: "Alice", age: 40};  // 整体覆盖属性

MATCH (n:Person {name: "Alice"})
SET n += {age: 40, city: "Paris"}; // 合并属性

MATCH (n:Person {name: "Alice"})
SET n:Admin:Active;                // 追加多个标签
```

```cypher
MATCH (n:Person {name: "Alice"})
REMOVE n.age;

MATCH (n:Person {name: "Alice"})
REMOVE n:Admin:Active;
```

* `SET n.p = expr` 的表达式可以引用当前解中的变量，例如
  `SET a.total = a.x + b.y`。
* `SET n.p = null` 等价于删除该属性。
* `SET n = {map}` 整体覆盖属性；`SET n += {map}` 合并。
* 标签追加/删除会同步维护 `node_label` 倒排索引与 F-25 属性索引。
* 目标节点不存在时会报错。

---

## 9. DELETE / DETACH DELETE

```cypher
MATCH (n:Person {name: "Bob"})
DELETE n;

MATCH (n:Person {name: "Bob"})
DETACH DELETE n;

MATCH ()-[r:KNOWS]->() DELETE r;

MATCH (n:Person {name: "A"}), (m:Person {name: "B"})
DELETE n, m;
```

* `DELETE n`：节点仍有邻接边时直接报错，并列出残留边数；用
  `DETACH DELETE n` 级联删除。
* `DELETE r`：删除边以及 `adj_out`、`adj_in`、`edge_type` 三处索引。
* `DETACH DELETE` 的关联边数量受 `--max-detach` 限制（默认 100,000）。
* 重复删除同一实体是幂等的（后续操作为跳过）。
* 删除后同一查询中继续读取该实体属性会得到 `null`；建议把删除放在查询末尾。

---

## 10. EXPLAIN

```cypher
EXPLAIN MATCH (n:Person) WHERE n.age > 30 RETURN n;
EXPLAIN CREATE (n:Temp);
```

输出文本计划，包括算子顺序、Bucket、前缀、估算行数和下推的过滤条件；
`EXPLAIN` 不会写入，也不会产出数据行。

---

## 11. 类型系统

存储属性只支持五种标量：

| 类型 | 示例 | 说明 |
|---|---|---|
| null | `null` | 不写入存储；读取缺失属性得到 null |
| bool | `true`/`false` | |
| int64 | `42` | 超过 2^53 仍精确保存 |
| float64 | `3.14` | `avg` 结果为 float64 |
| string | `"Alice"` | UTF-8 |

不支持数组、嵌套对象、列表、map 作为属性值。

写入限制：

* 单对象属性数 ≤ 128
* 属性名长度 ≤ 256 字节
* 单属性值 ≤ 64 KB
* 单对象 JSON ≤ 1 MB

超出限制显式报错，不静默截断。

---

## 12. 路径与实体序列化

`RETURN n`：

```json
{"id": 1, "labels": ["Person"], "props": {"name": "Alice"}}
```

`RETURN r`：

```json
{"id": 1, "from": 1, "to": 2, "type": "KNOWS", "props": {"since": 2020}}
```

`RETURN p`：

```json
{"nodes": [{"id": 1, "...": "..."}], "edges": [{"id": 1, "...": "..."}], "length": 1}
```

---

## 13. 明确不支持（v1）

以下语法不解析或语义不同，详见
[`docs/cypher-deviations.md`](cypher-deviations.md) 与
[`docs/neo4j-skip-list.md`](neo4j-skip-list.md)：

* `OPTIONAL MATCH`、`WITH`、`UNWIND`、`MERGE`、`CALL`、`UNION`
* `CASE`、`EXISTS`、`COALESCE`、`IN`、`=~` 正则、参数 `$x`
* 列表字面量、列表推导、`COLLECT`、`reduce`、`size`、`head`、`tail`
* 嵌套属性、map/array 属性
* 加权最短路径（Dijkstra）、路径分解函数 `nodes(p)`/`relationships(p)`
* 多语句一次提交、`LOAD CSV`、全文/地理索引、组合索引、约束
* `CREATE INDEX`/`DROP INDEX`（请使用 `walk index add|drop`）
* `count(DISTINCT expr)` 的去重语义（当前等价于 `count(expr)`）
* 变长关系的 `via` 绑定与属性过滤

---

## 14. CLI / HTTP 中的语法使用

```bash
# 写
walk write "CREATE (n:Person {name: 'Alice'})"

# 读
walk query "MATCH (n:Person) RETURN n.name AS name ORDER BY name"

# 解释计划
walk query --explain "MATCH (n:Person) WHERE n.name = 'Alice' RETURN n"

# HTTP
curl -X POST localhost:7474/query \
     --data 'MATCH (n:Person) RETURN n.name AS name'
curl -X POST localhost:7474/update \
     --data 'CREATE (n:Person {name: "Alice"})'
```

HTTP `/query` 仅接受只读语句（`EXPLAIN` 写语句除外）；`/update` 仅接受
写语句。请求体可以是纯 Cypher 文本，也可以是 `{"query": "..."}` JSON。
