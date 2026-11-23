# walk

[English](README.md) | **简体中文**

[![CI](https://github.com/23jdd/walk/actions/workflows/ci.yml/badge.svg)](https://github.com/23jdd/walk/actions/workflows/ci.yml)
[![Release](https://github.com/23jdd/walk/actions/workflows/release.yml/badge.svg)](https://github.com/23jdd/walk/actions/workflows/release.yml)

<p align="center">
  <img src="logo.svg" alt="walk logo" width="128" height="128">
</p>

`walk` 是一个单机版带标签属性图数据库与 Cypher 子集查询引擎，底层使用
[`bbolt`](https://github.com/etcd-io/bbolt)。节点、带类型的关系边和邻接倒排索引都存储在
同一个 B+ 树文件中，并同时提供 CLI 与 HTTP 服务接口。

实现遵循 `docs/operators.md` 中的底层算子契约（该文档是
`docs/算子规格与实现细节.pdf` 的 Markdown 版本），Cypher 文法见
`docs/lang.md`。与 openCypher 的语义差异以 `docs/operators.md` 第 7 节
决策表为准。

## 快速开始

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

Windows 下二进制为 `walk.exe`。默认数据库是 `./graph.db`，可用 `--db`
参数或环境变量 `WALK_DB` 指定。

## 历史人物关系 App

单文件、无需 npm 的 [demo.html](demo.html) 是一个中国历史人物关系探索 App：
基于 vis-network，通过 walk 的 HTTP 适配器读取图谱。内置从春秋到宋的 31 位
人物与 38 条关系（师承、君臣、盟友、思想传承等），支持朝代筛选、搜索、
人物详情卡和最短关系链探索。

```bash
go build -o walk ./cmd/walk
./walk serve --addr=:7474

# 方式 A：直接用浏览器打开 demo.html（file://）
# 方式 B：不用 npm，起一个静态目录服务
python -m http.server 8080
# 然后访问 http://127.0.0.1:8080/demo.html
```

首次打开点击“载入示例人物图谱”，之后点击人物即可查看详情与关系。HTTP
服务已发送 CORS 头，因此 `file://` 页面也可以直接访问
`http://127.0.0.1:7474`。

## 命令行

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

通用参数：`--db`、`--timeout`、`--max-solutions`、`--max-depth`、
`--max-detach`、`--batch-size`、`--log-level`。

对应环境变量：`WALK_DB`、`WALK_TIMEOUT`、`WALK_MAX_SOLUTIONS`、
`WALK_MAX_DEPTH`、`WALK_MAX_DETACH`、`WALK_BATCH_SIZE`、`WALK_LOG_LEVEL`，
以及 `WALK_ADDR`。

退出码：`0` 成功，`1` 命令行用法错误，`2` 数据/图结构校验错误，
`3` 系统内部错误。

`load` 支持以下格式：

* JSONL 节点：`{"id": 1, "labels": ["Person"], "props": {"name": "Alice"}}`
  （`id` 可选；属性字段支持 `props` 或 `properties`）。
* JSONL 边：`{"from": 1, "to": 2, "type": "KNOWS", "props": {...}}`。
* CSV 节点：首行为表头；可选 `id`、`labels`（逗号/分号分隔），其余列作为属性。
  默认使用 0 基行号作为节点 id。
* CSV 边：表头必须包含 `from_idx` 与 `to_idx`；可选 `type`，其余列作为边属性。

CSV 值按 `int64 -> float64 -> bool -> string` 的顺序推断类型。

## HTTP 接口

执行 `walk serve` 后：

| 端点 | 方法 | 说明 |
|---|---|---|
| `/query` | POST | 只读 Cypher，返回 JSON 结果 |
| `/update` | POST | 写 Cypher，返回受影响计数 |
| `/backup` | GET | 流式导出数据库一致性快照 |
| `/stats` | GET | F-21 统计信息 JSON |
| `/health` | GET | 存活探测 |

示例：

```bash
curl -X POST localhost:7474/query \
     --data 'MATCH (n:Person) RETURN n.name AS name ORDER BY name'

curl -X POST localhost:7474/update \
     --data 'CREATE (n:Person {name: "Alice"})'
```

请求体既可以是纯 Cypher 文本，也可以使用 JSON：`{"query": "..."}`。

## 查询语言

支持的 Cypher 子集：

* `MATCH`：节点/关系模式、多标签、内联属性、关系类型、多类型 `:T|U`、
  无向遍历、变长路径（`*`、`*n`、`*n..m`、`*..m`）以及 `shortestPath`。
* `WHERE`：比较运算、`AND/OR/NOT`、`STARTS WITH`、`ENDS WITH`、
  `CONTAINS`、`IS [NOT] NULL`、算术运算。
* `CREATE`、`SET`（`n.p = e`、`n = {}`、`n += {}`、`n:Label`）、`REMOVE`、
  `DELETE`、`DETACH DELETE`。
* `RETURN`：别名、`DISTINCT`、`ORDER BY`、`SKIP`、`LIMIT`、`count`、
  `sum`、`avg`、`min`、`max`、`length(p)`，以及节点/边/路径值。
* `EXPLAIN`：输出文本执行计划，不真正执行查询。

完整文法见 [`docs/lang.md`](docs/lang.md)；完整中文语法大全见
[`docs/syntax.zh-CN.md`](docs/syntax.zh-CN.md)；有意的语义差异见
[`docs/cypher-deviations.md`](docs/cypher-deviations.md)；不支持的语法见
[`docs/neo4j-skip-list.md`](docs/neo4j-skip-list.md)。

示例：

```cypher
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

## 存储与不变量

Bucket 布局：`nodes`、`edges`、`adj_out`、`adj_in`、`labels`、`reltypes`、
`reltype_by_id`、`node_label`、`edge_type`、`prop_idx`、`meta`。

测试与 `walk stats` 会交叉校验以下四条不变量：

1. 每条边的 `from`/`to` 节点必须存在；
2. 每条边在 `adj_out` 与 `adj_in` 中各有一条镜像记录；
3. 每个节点的每个标签在 `node_label` 中都有对应倒排记录；
4. 被数据引用的标签/关系类型字典条目永不删除。

写操作按 bbolt 事务原子提交。批量导入按批提交（每事务不超过 15,000 次
KV 操作，边批次最多 3,000 条）。`compact` 会把在线数据复制到新文件，
校验节点/边计数后原子替换旧文件。

## 项目结构

```
cmd/walk/        CLI 入口
executor/        存储引擎、索引、算子、解流、聚合
parser/          Cypher 词法/AST/递归下降解析器、错误诊断
internal/httpapi/ 可选 HTTP 适配器（query/update/stats/backup、CORS）
walk.go          DB 生命周期、事务、备份、compact
load.go          JSONL/CSV 导入器
stats.go         统计
engine.go        AST -> 算子链计划器
eval.go          表达式求值器
query.go         Query/Exec/Result API
docs/            语言、算子、计划器、偏差、测试文档
```

## 开发与测试

```bash
go test ./...
go test -race ./...
go vet ./...
go test . -run '^$' -bench . -benchmem
```

测试覆盖存储不变量、解析器位置诊断、CRUD/查询集成、变长路径关系唯一性、
最短路径、聚合与排序、JSONL/CSV 导入、HTTP 端点、备份与 compact。
详细说明见 [`docs/testing.md`](docs/testing.md)。固定种子随机 CRUD 属性测试
可在 `-race` 下复现，金标准测试使用 `testdata/golden/*.cypher` +
`*.setup.cypher` + `*.tsv`。

## 明确不支持的范围

v1 有意不支持：`OPTIONAL MATCH`、`WITH`、`UNWIND`、`MERGE`、`CALL`、
`UNION`、加权最短路径（Dijkstra）、嵌套/列表属性、全文与地理索引、
多数据库与集群。详见 [`docs/cypher-deviations.md`](docs/cypher-deviations.md)
与 [`docs/neo4j-skip-list.md`](docs/neo4j-skip-list.md)。
