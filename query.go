package walk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/23jdd/walk/executor"
	"github.com/23jdd/walk/parser"
	"go.etcd.io/bbolt"
)

// QueryOptions overrides per-query limits and timeout (F-17/F-24).
type QueryOptions struct {
	Explain      bool
	Timeout      time.Duration
	MaxSolutions int
	MaxDepth     int
	MaxDetach    int
}

// Result is the dual-shape query result described by the operator spec.
type Result struct {
	Columns  []string                `json:"columns"`
	Rows     [][]any                 `json:"rows"`
	Summary  *executor.WriteSummary  `json:"summary,omitempty"`
	Warnings []string                `json:"warnings,omitempty"`
	Plan     []string                `json:"plan,omitempty"`
}

// Query parses, plans and executes one Cypher statement. Write statements run
// in an atomic Update transaction; read statements run in a View transaction.
func (db *DB) Query(ctx context.Context, cypher string, opts QueryOptions) (*Result, error) {
	q, err := parser.Parse(cypher)
	if err != nil {
		return nil, err
	}
	cfg := db.queryConfig(opts)
	opts = db.fillOptions(opts)
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = db.cfg.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	write := q.HasWrites()
	explain := opts.Explain || q.Explain
	if explain {
		write = false
	}
	var result *Result
	run := func(e *executor.Exec) error {
		eng := newEngine(e, q, opts)
		res, err := eng.run()
		if err != nil {
			return err
		}
		result = res
		return nil
	}
	if write {
		err = db.runWithConfig(ctx, true, cfg, run)
	} else {
		err = db.runWithConfig(ctx, false, cfg, run)
	}
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = &Result{Columns: []string{}, Rows: [][]any{}}
	}
	if result.Rows == nil {
		result.Rows = [][]any{}
	}
	if result.Columns == nil {
		result.Columns = []string{}
	}
	return result, nil
}

// Exec is an alias for Query using the database's default timeout.
func (db *DB) Exec(ctx context.Context, cypher string) (*Result, error) {
	return db.Query(ctx, cypher, QueryOptions{})
}

// Explain returns the formatted plan without executing the query.
func (db *DB) Explain(ctx context.Context, cypher string) ([]string, error) {
	res, err := db.Query(ctx, cypher, QueryOptions{Explain: true})
	if err != nil {
		return nil, err
	}
	if len(res.Plan) > 0 {
		return res.Plan, nil
	}
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) > 0 {
			if s, ok := row[0].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func (db *DB) fillOptions(opts QueryOptions) QueryOptions {
	if opts.Timeout <= 0 {
		opts.Timeout = db.cfg.Timeout
	}
	if opts.MaxSolutions <= 0 {
		opts.MaxSolutions = db.cfg.MaxSolutions
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = db.cfg.MaxDepth
	}
	if opts.MaxDetach <= 0 {
		opts.MaxDetach = db.cfg.MaxDetach
	}
	return opts
}

func (db *DB) queryConfig(opts QueryOptions) executor.Config {
	cfg := db.cfg.execConfig()
	if opts.MaxSolutions > 0 {
		cfg.MaxSolutions = opts.MaxSolutions
	}
	if opts.MaxDepth > 0 {
		cfg.MaxDepth = opts.MaxDepth
	}
	if opts.MaxDetach > 0 {
		cfg.MaxDetach = opts.MaxDetach
	}
	return cfg
}

func (db *DB) runWithConfig(ctx context.Context, write bool, cfg executor.Config, fn func(*executor.Exec) error) error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.bolt == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	run := func(tx *bbolt.Tx) error {
		e := executor.NewExec(tx, ctx, cfg)
		if err := fn(e); err != nil {
			return err
		}
		return e.Err()
	}
	if write {
		return db.bolt.Update(run)
	}
	return db.bolt.View(run)
}

func jsonMarshalAny(v any) ([]byte, error) { return json.Marshal(v) }

// FormatValue renders a projected value for the CLI (TSV) output.
func FormatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case executor.Value:
		if x.IsNull() {
			return "null"
		}
		b, err := x.MarshalJSON()
		if err != nil {
			return fmt.Sprint(x.Any())
		}
		if x.Type() == executor.StringType {
			return x.AsString()
		}
		return string(b)
	case executor.NodeSummary:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("(%d", x.ID))
		for _, l := range x.Labels {
			sb.WriteString(":" + l)
		}
		sb.WriteString(")")
		return sb.String()
	case executor.EdgeSummary:
		return fmt.Sprintf("-[%s]->", x.Type)
	case executor.PathSummary:
		return fmt.Sprintf("path(len=%d)", x.Length)
	default:
		b, err := jsonMarshalAny(x)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}
