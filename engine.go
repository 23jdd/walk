package walk

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/23jdd/walk/executor"
	"github.com/23jdd/walk/parser"
)

type planStep struct {
	Op     string
	Detail string
	Bucket string
	Prefix string
	Est    int
}

type engine struct {
	e      *executor.Exec
	q      *parser.Query
	opts   QueryOptions
	bound  map[string]bool
	vars   []string
	hidden map[string]bool
	plan   []planStep
	gen    int
}

func newEngine(e *executor.Exec, q *parser.Query, opts QueryOptions) *engine {
	return &engine{
		e:      e,
		q:      q,
		opts:   opts,
		bound:  make(map[string]bool),
		hidden: make(map[string]bool),
	}
}

func singleSolution() executor.Stream {
	return func(yield func(executor.Solution) bool) {
		yield(executor.Solution{})
	}
}

func emptyStream() executor.Stream {
	return func(yield func(executor.Solution) bool) {}
}

func (g *engine) bind(name string) {
	if name == "" || g.bound[name] {
		return
	}
	g.bound[name] = true
	g.vars = append(g.vars, name)
}

func (g *engine) genVar(prefix string) string {
	g.gen++
	name := fmt.Sprintf("__%s%d", prefix, g.gen)
	g.hidden[name] = true
	return name
}

func (g *engine) nodeVar(np *parser.NodePattern, idx int) string {
	if np.Var != "" {
		return np.Var
	}
	return fmt.Sprintf("__n%d", idx)
}

// run compiles the query into an operator chain and drains it.
func (g *engine) run() (*Result, error) {
	stream, err := g.build()
	if err != nil {
		return nil, err
	}
	res := &Result{Columns: []string{}, Rows: [][]any{}}
	if g.opts.Explain || g.q.Explain {
		res.Plan = g.formatPlan()
		res.Columns = []string{"plan"}
		for _, line := range res.Plan {
			res.Rows = append(res.Rows, []any{line})
		}
		return res, nil
	}
	if g.q.Return == nil {
		for range stream {
		}
		if err := g.e.Err(); err != nil {
			return nil, err
		}
		if g.q.HasWrites() {
			s := g.e.Summary
			res.Summary = &s
		}
		res.Warnings = append(res.Warnings, g.e.Warnings...)
		return res, nil
	}
	rows, columns, err := g.project(stream)
	if err != nil {
		return nil, err
	}
	res.Columns = columns
	for row := range rows {
		res.Rows = append(res.Rows, []any(row))
	}
	if err := g.e.Err(); err != nil {
		return nil, err
	}
	if g.q.HasWrites() {
		s := g.e.Summary
		res.Summary = &s
	}
	res.Warnings = append(res.Warnings, g.e.Warnings...)
	return res, nil
}

func (g *engine) build() (executor.Stream, error) {
	stream := singleSolution()
	first := true
	for _, pat := range g.q.Match {
		if err := g.validatePattern(pat); err != nil {
			return nil, err
		}
		shared := g.patternSharesBinding(pat)
		if !shared && !first {
			right := g.compilePattern(singleSolution(), pat)
			g.addPlan("CrossJoin", "disconnected pattern", "", "", 0)
			stream = executor.CrossJoin(stream, right)
		} else {
			stream = g.compilePattern(stream, pat)
		}
		first = false
	}
	if g.q.Where != nil {
		where := g.q.Where
		g.addPlan("Keep", "WHERE filter", "", "", 0)
		stream = g.e.Keep(stream, func(sol executor.Solution) (executor.Value, error) {
			ctx := &evalCtx{e: g.e, sol: sol}
			return ctx.eval(where)
		})
	}
	if len(g.q.Create) > 0 {
		var err error
		stream, err = g.compileCreate(stream)
		if err != nil {
			return nil, err
		}
	}
	for _, item := range g.q.Set {
		switch {
		case item.Label != "":
			g.addPlan("SetLabel", item.Var+":"+item.Label, "", "", 0)
			stream = g.e.SetLabel(stream, item.Var, item.Label)
		case item.Map:
			ex := item.Value
			add := item.Add
			g.addPlan("SetMap", item.Var, "", "", 0)
			stream = g.e.SetMapMode(stream, item.Var, func(sol executor.Solution) (map[string]executor.Value, error) {
				return (&evalCtx{e: g.e, sol: sol}).evalMap(ex)
			}, !add)
		default:
			prop, ex := item.Prop, item.Value
			g.addPlan("SetProp", item.Var+"."+prop, "", "", 0)
			stream = g.e.SetProp(stream, item.Var, prop, func(sol executor.Solution) (executor.Value, error) {
				return (&evalCtx{e: g.e, sol: sol}).eval(ex)
			})
		}
	}
	for _, item := range g.q.Remove {
		if item.Label != "" {
			g.addPlan("RemoveLabel", item.Var+":"+item.Label, "", "", 0)
			stream = g.e.RemoveLabel(stream, item.Var, item.Label)
		} else {
			g.addPlan("RemoveProp", item.Var+"."+item.Prop, "", "", 0)
			stream = g.e.RemoveProp(stream, item.Var, item.Prop)
		}
	}
	for _, item := range g.q.Delete {
		g.addPlan("Delete", item.Var, "", "", 0)
		stream = g.e.Delete(stream, item.Var, g.q.Detach)
	}
	return stream, nil
}

func (g *engine) validatePattern(pat *parser.Pattern) error {
	if pat == nil || len(pat.Parts) == 0 {
		return fmt.Errorf("empty pattern")
	}
	for _, part := range pat.Parts {
		if part == nil || len(part.Nodes) == 0 {
			return fmt.Errorf("empty path pattern")
		}
		if len(part.Nodes) != len(part.Rels)+1 {
			return fmt.Errorf("malformed path pattern")
		}
		for _, rel := range part.Rels {
			if rel.VarLength {
				if rel.Var != "" {
					return fmt.Errorf("variable-length relationship variables are not supported (via ambiguity)")
				}
				if rel.Min < 1 {
					return fmt.Errorf("variable-length lower bound must be >= 1")
				}
				if rel.Max != 0 && rel.Max < rel.Min {
					return fmt.Errorf("invalid variable-length range %d..%d", rel.Min, rel.Max)
				}
				if len(rel.Props) > 0 {
					return fmt.Errorf("properties on variable-length relationships are not supported")
				}
			}
		}
		if pat.Shortest {
			if len(part.Nodes) != 2 || len(part.Rels) != 1 {
				return fmt.Errorf("shortestPath requires exactly one relationship between two nodes")
			}
			if !part.Rels[0].VarLength {
				return fmt.Errorf("shortestPath requires a variable-length relationship (e.g. -[*..8]->)")
			}
		}
	}
	return nil
}

func (g *engine) patternSharesBinding(pat *parser.Pattern) bool {
	for _, part := range pat.Parts {
		for _, np := range part.Nodes {
			if np.Var != "" && g.bound[np.Var] {
				return true
			}
		}
	}
	return false
}

func (g *engine) compilePattern(in executor.Stream, pat *parser.Pattern) executor.Stream {
	if pat.Shortest {
		return g.compileShortest(in, pat)
	}
	if len(pat.Parts) != 1 {
		// Multiple parts are parsed as separate patterns; this is a safeguard.
		out := in
		for _, part := range pat.Parts {
			out = g.compilePath(out, &parser.Pattern{PathVar: pat.PathVar, Parts: []*parser.PathPart{part}})
		}
		return out
	}
	return g.compilePath(in, pat)
}

func (g *engine) compilePath(in executor.Stream, pat *parser.Pattern) executor.Stream {
	part := pat.Parts[0]
	reversed := false
	anchor := g.chooseAnchor(part, pat.PathVar != "")
	if anchor == len(part.Nodes)-1 && len(part.Nodes) > 1 && pat.PathVar == "" {
		part = reversePart(part)
		reversed = true
		anchor = 0
	}
	nodes := make([]string, len(part.Nodes))
	for i, np := range part.Nodes {
		if np.Var != "" {
			nodes[i] = np.Var
		} else {
			nodes[i] = g.genVar("n")
		}
	}
	// Anchor node.
	np0 := part.Nodes[0]
	if g.bound[nodes[0]] {
		g.addPlan("Scan", "bound anchor "+nodes[0], "", "", 0)
		in = g.filterNode(in, nodes[0], np0)
	} else {
		props, err := g.constProps(np0.Props)
		if err != nil {
			return g.fail(err)
		}
		in = g.e.Find(np0.Labels, props, nodes[0])
		g.bind(nodes[0])
		g.addPlan("Find", describeNode(np0), executor.BucketNodeLabel, labelPrefix(np0.Labels), g.estimateLabels(np0.Labels))
	}
	if pat.PathVar != "" {
		in = g.e.StartPath(in, nodes[0], pat.PathVar)
		g.bind(pat.PathVar)
	}

	for i, rel := range part.Rels {
		src := nodes[i]
		dstPattern := part.Nodes[i+1]
		dst := nodes[i+1]
		typeIDs, ok := g.resolveTypes(rel.Types)
		if !ok {
			g.addPlan("Empty", "unknown relationship type", executor.BucketAdjOut, strings.Join(rel.Types, "|"), 0)
			return emptyStream()
		}
		dir := executor.OutDir
		switch rel.Direction {
		case parser.In:
			dir = executor.InDir
		case parser.Both:
			dir = executor.BothDir
		}
		via := rel.Var
		if via == "" && len(rel.Props) > 0 {
			via = g.genVar("e")
		}
		if rel.VarLength {
			lo := rel.Min
			hi := rel.Max
			if hi == 0 {
				hi = g.opts.MaxDepth
			}
			if dstBound, exists := g.bound[dst]; exists && dstBound {
				tmp := g.genVar("end")
				in = g.e.VarLen(in, src, dir, typeIDs, lo, hi, tmp)
				in = g.filterSame(in, tmp, dst)
			} else {
				in = g.e.VarLenWithPath(in, src, dir, typeIDs, lo, hi, dst, boolPath(pat.PathVar))
				g.bind(dst)
			}
			g.addPlan("VarLen", fmt.Sprintf("%s %s %d..%d", dir, joinTypes(rel.Types), lo, hi), executor.BucketAdjOut, "", 0)
		} else {
			if dstBound, exists := g.bound[dst]; exists && dstBound {
				tmp := g.genVar("end")
				in = g.e.HopWithPath(in, src, dir, typeIDs, via, tmp, boolPath(pat.PathVar))
				in = g.filterSame(in, tmp, dst)
			} else {
				in = g.e.HopWithPath(in, src, dir, typeIDs, via, dst, boolPath(pat.PathVar))
				g.bind(dst)
			}
			g.addPlan("Hop", fmt.Sprintf("%s %s", dir, joinTypes(rel.Types)), executor.BucketAdjOut, "", 0)
		}
		if rel.Var != "" {
			g.bind(rel.Var)
		}
		in = g.filterNode(in, dst, dstPattern)
		if len(rel.Props) > 0 {
			in = g.filterEdgeProps(in, via, rel.Props)
		}
	}
	if reversed {
		g.addPlan("Reverse", "anchored at right endpoint", "", "", 0)
	}
	return in
}

func (g *engine) compileShortest(in executor.Stream, pat *parser.Pattern) executor.Stream {
	part := pat.Parts[0]
	rel := part.Rels[0]
	leftP, rightP := part.Nodes[0], part.Nodes[1]
	left := leftP.Var
	right := rightP.Var
	if left == "" {
		left = g.genVar("s")
	}
	if right == "" {
		right = g.genVar("t")
	}
	if !g.bound[left] {
		props, err := g.constProps(leftP.Props)
		if err != nil {
			return g.fail(err)
		}
		leftStream := g.e.Find(leftP.Labels, props, left)
		in = executor.CrossJoin(in, leftStream)
		g.bind(left)
	} else {
		in = g.filterNode(in, left, leftP)
	}
	if !g.bound[right] {
		props, err := g.constProps(rightP.Props)
		if err != nil {
			return g.fail(err)
		}
		rightStream := g.e.Find(rightP.Labels, props, right)
		in = executor.CrossJoin(in, rightStream)
		g.bind(right)
	} else {
		in = g.filterNode(in, right, rightP)
	}
	typeIDs, ok := g.resolveTypes(rel.Types)
	if !ok {
		return emptyStream()
	}
	dir := executor.OutDir
	switch rel.Direction {
	case parser.In:
		dir = executor.InDir
	case parser.Both:
		dir = executor.BothDir
	}
	maxDepth := rel.Max
	if maxDepth == 0 {
		maxDepth = g.opts.MaxDepth
	}
	pathAs := ""
	if pat.PathVar != "" {
		pathAs = pat.PathVar
		g.bind(pat.PathVar)
	}
	g.addPlan("Shortest", fmt.Sprintf("BFS %s %s..%d", dir, joinTypes(rel.Types), maxDepth), executor.BucketAdjOut, "", 0)
	return g.e.Shortest(in, left, right, dir, typeIDs, maxDepth, right, pathAs)
}

func (g *engine) chooseAnchor(part *parser.PathPart, forceLeft bool) int {
	if forceLeft || len(part.Nodes) <= 1 {
		return 0
	}
	best, bestScore := 0, -1
	last := len(part.Nodes) - 1
	for i, np := range part.Nodes {
		score := 0
		if np.Var != "" && g.bound[np.Var] {
			score += 100
		}
		if len(np.Labels) > 0 {
			score += 10 + len(np.Labels)
		}
		if len(np.Props) > 0 {
			score += 5
		}
		// Prefer the ends so the chain can be expanded linearly.
		if i != 0 && i != last {
			score -= 20
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best == last {
		return last
	}
	return 0
}

func reversePart(part *parser.PathPart) *parser.PathPart {
	out := &parser.PathPart{Span: part.Span}
	for i := len(part.Nodes) - 1; i >= 0; i-- {
		out.Nodes = append(out.Nodes, part.Nodes[i])
	}
	for i := len(part.Rels) - 1; i >= 0; i-- {
		rel := *part.Rels[i]
		switch rel.Direction {
		case parser.In:
			rel.Direction = parser.Out
		case parser.Out:
			rel.Direction = parser.In
		}
		out.Rels = append(out.Rels, &rel)
	}
	return out
}

func (g *engine) filterNode(in executor.Stream, name string, np *parser.NodePattern) executor.Stream {
	if len(np.Labels) == 0 && len(np.Props) == 0 {
		return in
	}
	props, err := g.constProps(np.Props)
	if err != nil {
		g.e.Fail(err)
		return in
	}
	return g.e.Keep(in, func(sol executor.Solution) (executor.Value, error) {
		ent, ok := sol[name]
		if !ok || !ent.IsNode() {
			return executor.BoolValue(false), nil
		}
		n, err := g.e.GetNode(ent.Id)
		if err != nil {
			return executor.BoolValue(false), nil
		}
		for _, l := range np.Labels {
			if !n.HasLabel(l) {
				return executor.BoolValue(false), nil
			}
		}
		for _, p := range props {
			v, ok := n.Props[p.Name]
			if !ok || !v.Equal(p.Value) {
				return executor.BoolValue(false), nil
			}
		}
		return executor.BoolValue(true), nil
	})
}

func (g *engine) filterEdgeProps(in executor.Stream, edgeVar string, props []*parser.Prop) executor.Stream {
	consts, err := g.constProps(props)
	if err != nil {
		g.e.Fail(err)
		return in
	}
	return g.e.Keep(in, func(sol executor.Solution) (executor.Value, error) {
		ent, ok := sol[edgeVar]
		if !ok || !ent.IsEdge() {
			return executor.BoolValue(false), nil
		}
		ed, err := g.e.GetEdge(ent.Id)
		if err != nil {
			return executor.BoolValue(false), nil
		}
		for _, p := range consts {
			v, ok := ed.Props[p.Name]
			if !ok || !v.Equal(p.Value) {
				return executor.BoolValue(false), nil
			}
		}
		return executor.BoolValue(true), nil
	})
}

func (g *engine) filterSame(in executor.Stream, a, b string) executor.Stream {
	return g.e.Keep(in, func(sol executor.Solution) (executor.Value, error) {
		x, ok1 := sol[a]
		y, ok2 := sol[b]
		return executor.BoolValue(ok1 && ok2 && x.Kind == y.Kind && x.Id == y.Id), nil
	})
}

func (g *engine) constProps(props []*parser.Prop) ([]executor.PropPred, error) {
	ctx := &evalCtx{e: g.e}
	out := make([]executor.PropPred, 0, len(props))
	for _, p := range props {
		v, err := ctx.constValue(p.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, executor.PropPred{Name: p.Key, Value: v})
	}
	return out, nil
}

func (g *engine) resolveTypes(types []string) ([]uint64, bool) {
	if len(types) == 0 {
		return nil, true
	}
	out := make([]uint64, 0, len(types))
	for _, t := range types {
		id, err := g.e.GetTypeID(t)
		if err != nil {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

// ---------------------------------------------------------------------------
// CREATE
// ---------------------------------------------------------------------------

func (g *engine) compileCreate(in executor.Stream) (executor.Stream, error) {
	for _, pat := range g.q.Create {
		if pat.Shortest {
			return nil, fmt.Errorf("shortestPath cannot appear in CREATE")
		}
		for _, part := range pat.Parts {
			nodeVars := make([]string, len(part.Nodes))
			for i, np := range part.Nodes {
				v := np.Var
				if v == "" {
					v = g.genVar("n")
				}
				nodeVars[i] = v
				if g.bound[v] {
					continue
				}
				props, err := g.constPropsMap(np.Props)
				if err != nil {
					return nil, err
				}
				in = g.e.New(in, np.Labels, props, v)
				g.bind(v)
				g.addPlan("New", describeNode(np), executor.BucketNodes, "", 0)
			}
			edgeVars := make([]string, len(part.Rels))
			for i, rel := range part.Rels {
				if rel.VarLength {
					return nil, fmt.Errorf("variable-length relationships are not allowed in CREATE")
				}
				if len(rel.Types) != 1 {
					return nil, fmt.Errorf("CREATE requires exactly one relationship type")
				}
				props, err := g.constPropsMap(rel.Props)
				if err != nil {
					return nil, err
				}
				via := rel.Var
				if via == "" {
					via = g.genVar("e")
				}
				edgeVars[i] = via
				in = g.e.Link(in, nodeVars[i], rel.Types[0], nodeVars[i+1], props, via)
				g.bind(via)
				g.addPlan("Link", fmt.Sprintf("%s -[%s]-> %s", nodeVars[i], rel.Types[0], nodeVars[i+1]), executor.BucketEdges, "", 0)
			}
			if pat.PathVar != "" {
				in = bindPath(in, nodeVars, edgeVars, pat.PathVar)
				g.bind(pat.PathVar)
			}
		}
	}
	return in, nil
}

func bindPath(in executor.Stream, nodeVars, edgeVars []string, pathVar string) executor.Stream {
	return func(yield func(executor.Solution) bool) {
		for sol := range in {
			p := &executor.Path{}
			ok := true
			for i, nv := range nodeVars {
				ent, exists := sol[nv]
				if !exists || !ent.IsNode() {
					ok = false
					break
				}
				p.Nodes = append(p.Nodes, ent.Id)
				if i < len(edgeVars) {
					ee, exists := sol[edgeVars[i]]
					if !exists || !ee.IsEdge() {
						ok = false
						break
					}
					p.Edges = append(p.Edges, ee.Id)
				}
			}
			if !ok {
				continue
			}
			next := executor.CloneSolution(sol)
			next[pathVar] = executor.PathEntity(p)
			if !yield(next) {
				return
			}
		}
	}
}

func (g *engine) constPropsMap(props []*parser.Prop) (map[string]executor.Value, error) {
	ctx := &evalCtx{e: g.e}
	out := make(map[string]executor.Value, len(props))
	for _, p := range props {
		v, err := ctx.constValue(p.Value)
		if err != nil {
			return nil, err
		}
		if !v.IsNull() {
			out[p.Key] = v
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// RETURN / GIVE
// ---------------------------------------------------------------------------

func (g *engine) project(in executor.Stream) (executor.RowStream, []string, error) {
	items := g.q.Return.Items
	var projections []executor.Projection
	var columns []string
	if g.q.Return.Star {
		for _, v := range g.vars {
			if g.hidden[v] {
				continue
			}
			projections = append(projections, executor.Projection{Name: v, Entity: v})
			columns = append(columns, v)
		}
	} else {
		for _, item := range items {
			proj, err := g.projection(item)
			if err != nil {
				return nil, nil, err
			}
			name := item.Alias
			if name == "" {
				if item.Text != "" {
					name = item.Text
				} else {
					name = parser.ExprText(item.Expr)
				}
			}
			proj.Name = name
			projections = append(projections, proj)
			columns = append(columns, name)
		}
	}
	if len(projections) == 0 {
		return emptyRows(), nil, nil
	}
	opts := executor.GiveOptions{Distinct: g.q.Return.Distinct}
	// ORDER BY
	aliasNames := map[string]bool{}
	for _, c := range columns {
		aliasNames[c] = true
	}
	aggQuery := hasAggregateProj(projections)
	for _, ob := range g.q.Return.OrderBy {
		key := executor.OrderKey{Desc: ob.Desc}
		if v, ok := ob.Expr.(*parser.VariableExpr); ok && aliasNames[v.Name] {
			key.Alias = v.Name
		} else if aliasNames[parser.ExprText(ob.Expr)] {
			key.Alias = parser.ExprText(ob.Expr)
		} else {
			if aggQuery && containsAggregate(ob.Expr) {
				return nil, nil, fmt.Errorf("ORDER BY aggregate expressions require an alias")
			}
			ex := ob.Expr
			key.Expr = func(sol executor.Solution) (executor.Value, error) {
				return (&evalCtx{e: g.e, sol: sol}).eval(ex)
			}
		}
		opts.OrderBy = append(opts.OrderBy, key)
	}
	if g.q.Return.Skip != nil {
		n, err := g.constInt(g.q.Return.Skip)
		if err != nil {
			return nil, nil, fmt.Errorf("SKIP: %w", err)
		}
		if n < 0 {
			return nil, nil, fmt.Errorf("SKIP must be non-negative")
		}
		opts.Skip = n
	}
	if g.q.Return.Limit != nil {
		n, err := g.constInt(g.q.Return.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("LIMIT: %w", err)
		}
		if n < 0 {
			return nil, nil, fmt.Errorf("LIMIT must be non-negative")
		}
		opts.Limit = n
		opts.HasLimit = true
	}
	g.addPlan("Give", fmt.Sprintf("%d column(s)", len(columns)), "", "", 0)
	if len(opts.OrderBy) > 0 {
		g.addPlan("Order", fmt.Sprintf("%d key(s)", len(opts.OrderBy)), "", "", 0)
	}
	if opts.Distinct {
		g.addPlan("Distinct", "", "", "", 0)
	}
	if opts.Skip > 0 {
		g.addPlan("Skip", strconv.Itoa(opts.Skip), "", "", 0)
	}
	if opts.HasLimit {
		g.addPlan("Limit", strconv.Itoa(opts.Limit), "", "", 0)
	}
	return g.e.Give(in, projections, opts), columns, nil
}

func (g *engine) projection(item *parser.ReturnItem) (executor.Projection, error) {
	if fn, ok := item.Expr.(*parser.FuncExpr); ok {
		kind, arg, isAgg, err := aggregateKind(fn)
		if err != nil {
			return executor.Projection{}, err
		}
		if isAgg {
			p := executor.Projection{Agg: kind}
			if arg != nil {
				a := arg
				p.AggArg = func(sol executor.Solution) (executor.Value, error) {
					return (&evalCtx{e: g.e, sol: sol}).eval(a)
				}
			}
			return p, nil
		}
	}
	if v, ok := item.Expr.(*parser.VariableExpr); ok {
		return executor.Projection{Entity: v.Name}, nil
	}
	ex := item.Expr
	return executor.Projection{Expr: func(sol executor.Solution) (executor.Value, error) {
		return (&evalCtx{e: g.e, sol: sol}).eval(ex)
	}}, nil
}

func aggregateKind(fn *parser.FuncExpr) (executor.AggKind, parser.Expr, bool, error) {
	switch strings.ToLower(fn.Name) {
	case "count":
		if fn.Star {
			return executor.AggCountStar, nil, true, nil
		}
		if len(fn.Args) != 1 {
			return 0, nil, false, fmt.Errorf("count() takes one argument")
		}
		return executor.AggCount, fn.Args[0], true, nil
	case "sum", "avg", "min", "max":
		if len(fn.Args) != 1 {
			return 0, nil, false, fmt.Errorf("%s() takes one argument", fn.Name)
		}
		switch strings.ToLower(fn.Name) {
		case "sum":
			return executor.AggSum, fn.Args[0], true, nil
		case "avg":
			return executor.AggAvg, fn.Args[0], true, nil
		case "min":
			return executor.AggMin, fn.Args[0], true, nil
		default:
			return executor.AggMax, fn.Args[0], true, nil
		}
	}
	return 0, nil, false, nil
}

func hasAggregateProj(items []executor.Projection) bool {
	for _, it := range items {
		if it.Agg != executor.AggNone {
			return true
		}
	}
	return false
}

func containsAggregate(ex parser.Expr) bool {
	switch x := ex.(type) {
	case *parser.FuncExpr:
		switch strings.ToLower(x.Name) {
		case "count", "sum", "avg", "min", "max":
			return true
		}
		for _, a := range x.Args {
			if containsAggregate(a) {
				return true
			}
		}
	case *parser.BinaryExpr:
		return containsAggregate(x.Left) || containsAggregate(x.Right)
	case *parser.UnaryExpr:
		return containsAggregate(x.Expr)
	case *parser.PropertyExpr:
		return containsAggregate(x.Target)
	case *parser.IsNullExpr:
		return containsAggregate(x.Expr)
	}
	return false
}

func (g *engine) constInt(ex parser.Expr) (int, error) {
	v, err := (&evalCtx{e: g.e}).eval(ex)
	if err != nil {
		return 0, err
	}
	if v.Type() != executor.IntType {
		return 0, fmt.Errorf("expected an integer, got %s", v.Type())
	}
	return int(v.AsInt()), nil
}

func emptyRows() executor.RowStream {
	return func(yield func(executor.Row) bool) {}
}

// ---------------------------------------------------------------------------
// EXPLAIN
// ---------------------------------------------------------------------------

func (g *engine) addPlan(op, detail, bucket, prefix string, est int) {
	g.plan = append(g.plan, planStep{Op: op, Detail: detail, Bucket: bucket, Prefix: prefix, Est: est})
}

func (g *engine) formatPlan() []string {
	lines := []string{fmt.Sprintf("EXPLAIN  operators=%d", len(g.plan))}
	lines = append(lines, fmt.Sprintf("%-4s %-12s %-44s %-14s %-16s %s", "#", "OPERATOR", "DETAIL", "BUCKET", "PREFIX", "EST"))
	for i, s := range g.plan {
		lines = append(lines, fmt.Sprintf("%-4d %-12s %-44s %-14s %-16s %d", i+1, s.Op, s.Detail, s.Bucket, s.Prefix, s.Est))
	}
	return lines
}

func (g *engine) estimateLabels(labels []string) int {
	if len(labels) == 0 {
		return g.e.NodeCount()
	}
	best := -1
	for _, l := range labels {
		c := g.e.CountLabelsNode(l)
		if best < 0 || c < best {
			best = c
		}
	}
	return best
}

func (g *engine) fail(err error) executor.Stream {
	g.e.Fail(err)
	return emptyStream()
}

func describeNode(np *parser.NodePattern) string {
	parts := []string{}
	if np.Var != "" {
		parts = append(parts, np.Var)
	}
	for _, l := range np.Labels {
		parts = append(parts, ":"+l)
	}
	if len(np.Props) > 0 {
		parts = append(parts, "{...}")
	}
	return strings.Join(parts, "")
}

func labelPrefix(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	return labels[0]
}

func joinTypes(types []string) string {
	if len(types) == 0 {
		return "*"
	}
	return strings.Join(types, "|")
}

func boolPath(s string) string { return s }

// sortedVars is used by tests and EXPLAIN for deterministic output.
func sortedVars(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
