package walk

import (
	"fmt"
	"math"
	"strings"

	"github.com/23jdd/walk/executor"
	"github.com/23jdd/walk/parser"
)

// evalCtx evaluates an AST expression against one solution mapping.
type evalCtx struct {
	e   *executor.Exec
	sol executor.Solution
}

func (c *evalCtx) eval(ex parser.Expr) (executor.Value, error) {
	switch x := ex.(type) {
	case *parser.LiteralExpr:
		return x.Value, nil

	case *parser.VariableExpr:
		if _, ok := c.sol[x.Name]; ok {
			return executor.NullValue(), fmt.Errorf("variable %q is not a scalar; use a property or function", x.Name)
		}
		return executor.NullValue(), fmt.Errorf("unknown variable %q", x.Name)

	case *parser.PropertyExpr:
		ent, err := c.evalEntity(x.Target)
		if err != nil {
			return executor.NullValue(), err
		}
		return c.property(ent, x.Prop)

	case *parser.UnaryExpr:
		v, err := c.eval(x.Expr)
		if err != nil {
			return executor.NullValue(), err
		}
		switch x.Op {
		case "NOT":
			if v.IsNull() {
				return executor.BoolValue(false), nil
			}
			if v.Type() != executor.BoolType {
				return executor.NullValue(), typeError("NOT", v)
			}
			return executor.BoolValue(!v.AsBool()), nil
		case "-":
			switch v.Type() {
			case executor.IntType:
				return executor.IntValue(-v.AsInt()), nil
			case executor.FloatType:
				return executor.FloatValue(-v.AsFloat()), nil
			default:
				return executor.NullValue(), typeError("unary -", v)
			}
		case "+":
			if !isNumeric(v) {
				return executor.NullValue(), typeError("unary +", v)
			}
			return v, nil
		}
		return executor.NullValue(), fmt.Errorf("unsupported unary operator %q", x.Op)

	case *parser.IsNullExpr:
		v, err := c.eval(x.Expr)
		if err != nil {
			return executor.NullValue(), err
		}
		is := v.IsNull()
		if x.Not {
			is = !is
		}
		return executor.BoolValue(is), nil

	case *parser.BinaryExpr:
		return c.evalBinary(x)

	case *parser.FuncExpr:
		return c.evalFunc(x)

	default:
		return executor.NullValue(), fmt.Errorf("unsupported expression %T", ex)
	}
}

func (c *evalCtx) evalBinary(x *parser.BinaryExpr) (executor.Value, error) {
	switch x.Op {
	case "AND", "OR":
		l, err := c.evalBool(x.Left)
		if err != nil {
			return executor.NullValue(), err
		}
		if x.Op == "AND" && !l {
			return executor.BoolValue(false), nil
		}
		if x.Op == "OR" && l {
			return executor.BoolValue(true), nil
		}
		r, err := c.evalBool(x.Right)
		if err != nil {
			return executor.NullValue(), err
		}
		return executor.BoolValue(r), nil
	}

	lv, err := c.eval(x.Left)
	if err != nil {
		return executor.NullValue(), err
	}
	rv, err := c.eval(x.Right)
	if err != nil {
		return executor.NullValue(), err
	}

	switch x.Op {
	case "=", "<>", "<", "<=", ">", ">=":
		return compareOp(x.Op, lv, rv)
	case "STARTS WITH", "ENDS WITH", "CONTAINS":
		return stringOp(x.Op, lv, rv)
	case "+", "-", "*", "/", "%":
		return arithmetic(x.Op, lv, rv)
	}
	return executor.NullValue(), fmt.Errorf("unsupported operator %q", x.Op)
}

func (c *evalCtx) evalBool(ex parser.Expr) (bool, error) {
	v, err := c.eval(ex)
	if err != nil {
		return false, err
	}
	if v.IsNull() {
		return false, nil
	}
	if v.Type() != executor.BoolType {
		return false, typeError("boolean", v)
	}
	return v.AsBool(), nil
}

func (c *evalCtx) evalEntity(ex parser.Expr) (executor.Entity, error) {
	switch x := ex.(type) {
	case *parser.VariableExpr:
		ent, ok := c.sol[x.Name]
		if !ok {
			return executor.Entity{}, fmt.Errorf("unknown variable %q", x.Name)
		}
		return ent, nil
	default:
		return executor.Entity{}, fmt.Errorf("expression %q does not evaluate to an entity", parser.ExprText(ex))
	}
}

func (c *evalCtx) property(ent executor.Entity, prop string) (executor.Value, error) {
	switch ent.Kind {
	case executor.NodeKind:
		n, err := c.e.GetNode(ent.Id)
		if err != nil {
			return executor.NullValue(), nil // deleted entity reads as null (D12)
		}
		if v, ok := n.Props[prop]; ok {
			return v, nil
		}
		return executor.NullValue(), nil
	case executor.EdgeKind:
		ed, err := c.e.GetEdge(ent.Id)
		if err != nil {
			return executor.NullValue(), nil
		}
		if v, ok := ed.Props[prop]; ok {
			return v, nil
		}
		return executor.NullValue(), nil
	case executor.PathKind:
		return executor.NullValue(), fmt.Errorf("paths have no property %q; use length(p)", prop)
	}
	return executor.NullValue(), fmt.Errorf("unknown entity kind")
}

func (c *evalCtx) evalFunc(x *parser.FuncExpr) (executor.Value, error) {
	name := strings.ToLower(x.Name)
	if x.Star {
		return executor.NullValue(), fmt.Errorf("count(*) is only valid in RETURN")
	}
	switch name {
	case "length":
		if len(x.Args) != 1 {
			return executor.NullValue(), fmt.Errorf("length() takes one argument")
		}
		ent, err := c.evalEntity(x.Args[0])
		if err != nil {
			return executor.NullValue(), err
		}
		if ent.Kind == executor.PathKind {
			return executor.IntValue(int64(ent.Path.Length())), nil
		}
		return executor.NullValue(), fmt.Errorf("length() requires a path")
	case "id":
		if len(x.Args) != 1 {
			return executor.NullValue(), fmt.Errorf("id() takes one argument")
		}
		ent, err := c.evalEntity(x.Args[0])
		if err != nil {
			return executor.NullValue(), err
		}
		return executor.IntValue(int64(ent.Id)), nil
	case "tostring":
		if len(x.Args) != 1 {
			return executor.NullValue(), fmt.Errorf("toString() takes one argument")
		}
		v, err := c.eval(x.Args[0])
		if err != nil {
			return executor.NullValue(), err
		}
		if v.IsNull() {
			return executor.NullValue(), nil
		}
		if v.Type() == executor.StringType {
			return v, nil
		}
		b, _ := v.MarshalJSON()
		return executor.StringValue(string(b)), nil
	case "tointeger":
		if len(x.Args) != 1 {
			return executor.NullValue(), fmt.Errorf("toInteger() takes one argument")
		}
		v, err := c.eval(x.Args[0])
		if err != nil {
			return executor.NullValue(), err
		}
		if v.IsNull() {
			return executor.NullValue(), nil
		}
		if v.Type() == executor.IntType {
			return v, nil
		}
		if isNumeric(v) {
			return executor.IntValue(int64(v.AsFloat())), nil
		}
		return executor.NullValue(), nil
	case "tofloat":
		if len(x.Args) != 1 {
			return executor.NullValue(), fmt.Errorf("toFloat() takes one argument")
		}
		v, err := c.eval(x.Args[0])
		if err != nil {
			return executor.NullValue(), err
		}
		if v.IsNull() {
			return executor.NullValue(), nil
		}
		if isNumeric(v) {
			return executor.FloatValue(v.AsFloat()), nil
		}
		return executor.NullValue(), nil
	case "count", "sum", "avg", "min", "max":
		return executor.NullValue(), fmt.Errorf("%s() is only valid in RETURN", name)
	}
	return executor.NullValue(), fmt.Errorf("unknown function %q", x.Name)
}

func (c *evalCtx) evalMap(ex parser.Expr) (map[string]executor.Value, error) {
	m, ok := ex.(*parser.MapExpr)
	if !ok {
		return nil, fmt.Errorf("expected a map literal")
	}
	out := make(map[string]executor.Value, len(m.Entries))
	for _, entry := range m.Entries {
		v, err := c.eval(entry.Value)
		if err != nil {
			return nil, err
		}
		if v.IsNull() {
			continue
		}
		out[entry.Key] = v
	}
	return out, nil
}

// constValue evaluates a literal-only expression (used by patterns).
func (c *evalCtx) constValue(ex parser.Expr) (executor.Value, error) {
	switch ex.(type) {
	case *parser.LiteralExpr:
		return c.eval(ex)
	case *parser.UnaryExpr:
		u := ex.(*parser.UnaryExpr)
		if _, ok := u.Expr.(*parser.LiteralExpr); ok {
			return c.eval(ex)
		}
	}
	return executor.NullValue(), fmt.Errorf("pattern values must be literals (got %T for %s)", ex, parser.ExprText(ex))
}

func (c *evalCtx) constMap(props []*parser.Prop) (map[string]executor.Value, error) {
	out := make(map[string]executor.Value, len(props))
	for _, p := range props {
		v, err := c.constValue(p.Value)
		if err != nil {
			return nil, err
		}
		if !v.IsNull() {
			out[p.Key] = v
		}
	}
	return out, nil
}

func isNumeric(v executor.Value) bool {
	return v.Type() == executor.IntType || v.Type() == executor.FloatType
}

func typeError(ctx string, v executor.Value) error {
	return fmt.Errorf("%w: %s requires a compatible argument, got %s", executor.ErrTypeMismatch, ctx, v.Type())
}

func valuesEqual(a, b executor.Value) bool {
	if a.IsNull() || b.IsNull() {
		return false
	}
	if isNumeric(a) && isNumeric(b) {
		return a.AsFloat() == b.AsFloat()
	}
	if a.Type() != b.Type() {
		return false
	}
	switch a.Type() {
	case executor.BoolType:
		return a.AsBool() == b.AsBool()
	case executor.StringType:
		return a.AsString() == b.AsString()
	}
	return false
}

func compareOp(op string, a, b executor.Value) (executor.Value, error) {
	if a.IsNull() || b.IsNull() {
		return executor.BoolValue(false), nil // F-12 deviation: null comparisons are false
	}
	switch op {
	case "=":
		return executor.BoolValue(valuesEqual(a, b)), nil
	case "<>":
		return executor.BoolValue(!valuesEqual(a, b)), nil
	}
	var cmp int
	switch {
	case isNumeric(a) && isNumeric(b):
		x, y := a.AsFloat(), b.AsFloat()
		switch {
		case x < y:
			cmp = -1
		case x > y:
			cmp = 1
		}
	case a.Type() == executor.StringType && b.Type() == executor.StringType:
		cmp = strings.Compare(a.AsString(), b.AsString())
	default:
		return executor.NullValue(), fmt.Errorf("%w: cannot compare %s and %s", executor.ErrTypeMismatch, a.Type(), b.Type())
	}
	switch op {
	case "<":
		return executor.BoolValue(cmp < 0), nil
	case "<=":
		return executor.BoolValue(cmp <= 0), nil
	case ">":
		return executor.BoolValue(cmp > 0), nil
	case ">=":
		return executor.BoolValue(cmp >= 0), nil
	}
	return executor.NullValue(), fmt.Errorf("unsupported comparison %q", op)
}

func stringOp(op string, a, b executor.Value) (executor.Value, error) {
	if a.IsNull() || b.IsNull() {
		return executor.BoolValue(false), nil
	}
	if a.Type() != executor.StringType || b.Type() != executor.StringType {
		return executor.NullValue(), fmt.Errorf("%w: %s requires two strings", executor.ErrTypeMismatch, op)
	}
	switch op {
	case "STARTS WITH":
		return executor.BoolValue(strings.HasPrefix(a.AsString(), b.AsString())), nil
	case "ENDS WITH":
		return executor.BoolValue(strings.HasSuffix(a.AsString(), b.AsString())), nil
	case "CONTAINS":
		return executor.BoolValue(strings.Contains(a.AsString(), b.AsString())), nil
	}
	return executor.NullValue(), fmt.Errorf("unsupported string operator %q", op)
}

func arithmetic(op string, a, b executor.Value) (executor.Value, error) {
	if a.IsNull() || b.IsNull() {
		return executor.NullValue(), nil
	}
	if op == "+" && a.Type() == executor.StringType && b.Type() == executor.StringType {
		return executor.StringValue(a.AsString() + b.AsString()), nil
	}
	if !isNumeric(a) || !isNumeric(b) {
		return executor.NullValue(), fmt.Errorf("%w: %s requires numbers, got %s and %s", executor.ErrTypeMismatch, op, a.Type(), b.Type())
	}
	if op == "/" {
		d := b.AsFloat()
		if d == 0 {
			return executor.NullValue(), fmt.Errorf("division by zero")
		}
		return executor.FloatValue(a.AsFloat() / d), nil
	}
	if op == "%" {
		d := b.AsFloat()
		if d == 0 {
			return executor.NullValue(), fmt.Errorf("division by zero")
		}
		return executor.FloatValue(math.Mod(a.AsFloat(), d)), nil
	}
	if a.Type() == executor.IntType && b.Type() == executor.IntType {
		switch op {
		case "+":
			return executor.IntValue(a.AsInt() + b.AsInt()), nil
		case "-":
			return executor.IntValue(a.AsInt() - b.AsInt()), nil
		case "*":
			return executor.IntValue(a.AsInt() * b.AsInt()), nil
		}
	}
	x, y := a.AsFloat(), b.AsFloat()
	switch op {
	case "+":
		return executor.FloatValue(x + y), nil
	case "-":
		return executor.FloatValue(x - y), nil
	case "*":
		return executor.FloatValue(x * y), nil
	}
	return executor.NullValue(), fmt.Errorf("unsupported arithmetic operator %q", op)
}
