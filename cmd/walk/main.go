package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/23jdd/walk"
	"github.com/23jdd/walk/executor"
	"github.com/23jdd/walk/parser"
)

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:]))
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `walk %s - bbolt property graph with a Cypher subset

usage:
  walk [--db=./graph.db] <command> [options]

commands:
  load     --nodes <file> [--edges <file>] [--format=auto|jsonl|csv] [--batch-size=N]
  query    '<cypher>' [--explain] [--json] [--timeout=5s]
  write    '<cypher>'
  stats    [--json]
  backup   <path>
  compact
  index    add|drop|list [label [property]]
  serve    [--addr=:7474]

global options:
  --db <path>            database file (default ./graph.db)
  --timeout <duration>   query timeout (default 5s)
  --max-solutions <n>    solution cap (default 1000000)
  --max-depth <n>        variable-length path cap (default 8)
  --max-detach <n>       DETACH DELETE edge cap (default 100000)
  --batch-size <n>       import batch size (default 5000)
  --log-level <level>    debug|info|warn|error (default warn)
`, version)
}

type commonFlags struct {
	db           string
	timeout      time.Duration
	maxSolutions int
	maxDepth     int
	maxDetach    int
	batchSize    int
	logLevel     string
}

func addCommon(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.db, "db", envDefault("WALK_DB", "./graph.db"), "database file")
	fs.DurationVar(&c.timeout, "timeout", 5*time.Second, "query timeout")
	fs.IntVar(&c.maxSolutions, "max-solutions", 1_000_000, "maximum solutions")
	fs.IntVar(&c.maxDepth, "max-depth", 8, "variable-length path maximum depth")
	fs.IntVar(&c.maxDetach, "max-detach", 100_000, "DETACH DELETE maximum edges")
	fs.IntVar(&c.batchSize, "batch-size", 5_000, "import batch size")
	fs.StringVar(&c.logLevel, "log-level", "warn", "log level")
	return c
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c *commonFlags) config() walk.Config {
	cfg := walk.DefaultConfig()
	cfg.Timeout = c.timeout
	cfg.MaxSolutions = c.maxSolutions
	cfg.MaxDepth = c.maxDepth
	cfg.MaxDetach = c.maxDetach
	cfg.BatchSize = c.batchSize
	return cfg
}

func (c *commonFlags) open() (*walk.DB, error) {
	if err := walk.EnsureDir(c.db); err != nil {
		return nil, err
	}
	return walk.OpenWithConfig(c.db, c.config())
}

func setupLogger(level string) {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "info":
		lv = slog.LevelInfo
	case "warn", "":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelWarn
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lv})))
}

func run(args []string) int {
	if len(args) == 0 {
		usage(os.Stdout)
		return 1
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return 0
	case "version":
		fmt.Println("walk " + version)
		return 0
	case "load":
		return cmdLoad(args[1:])
	case "query":
		return cmdQuery(args[1:], false)
	case "write":
		return cmdQuery(args[1:], true)
	case "stats":
		return cmdStats(args[1:])
	case "backup":
		return cmdBackup(args[1:])
	case "compact":
		return cmdCompact(args[1:])
	case "index":
		return cmdIndex(args[1:])
	case "serve":
		return cmdServe(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage(os.Stderr)
		return 1
	}
}

func cmdLoad(args []string) int {
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	common := addCommon(fs)
	nodes := fs.String("nodes", "", "nodes JSONL/CSV file")
	edges := fs.String("edges", "", "edges JSONL/CSV file")
	format := fs.String("format", "auto", "auto|jsonl|csv")
	quiet := fs.Bool("quiet", false, "suppress progress output")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	setupLogger(common.logLevel)
	if *nodes == "" {
		fmt.Fprintln(os.Stderr, "load: --nodes is required")
		return 1
	}
	db, err := common.open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return 3
	}
	defer db.Close()
	var progress io.Writer = os.Stderr
	if *quiet {
		progress = nil
	}
	summary, err := db.Load(*nodes, *edges, walk.LoadOptions{Format: *format, BatchSize: common.batchSize, Progress: progress})
	if err != nil {
		fmt.Fprintf(os.Stderr, "load failed: %v\n", err)
		return 2
	}
	fmt.Printf("imported nodes=%d edges=%d batches=%d duration=%s\n", summary.Nodes, summary.Edges, summary.Batches, summary.Duration.Round(time.Millisecond))
	for _, warning := range summary.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}
	return 0
}

func cmdQuery(args []string, write bool) int {
	name := "query"
	if write {
		name = "write"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	common := addCommon(fs)
	explain := fs.Bool("explain", false, "print the execution plan")
	jsonOut := fs.Bool("json", false, "print JSON instead of TSV")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	setupLogger(common.logLevel)
	cypher := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if cypher == "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			return 1
		}
		cypher = strings.TrimSpace(string(data))
	}
	if cypher == "" {
		fmt.Fprintf(os.Stderr, "%s: empty Cypher statement\n", name)
		return 1
	}
	q, err := parser.Parse(cypher)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if write && !q.HasWrites() {
		fmt.Fprintf(os.Stderr, "write: statement has no write clause (use query)\n")
		return 1
	}
	if !write && q.HasWrites() {
		fmt.Fprintf(os.Stderr, "query: statement modifies the graph (use write)\n")
		return 1
	}
	db, err := common.open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 3
	}
	defer db.Close()
	res, err := db.Query(context.Background(), cypher, walk.QueryOptions{
		Explain:      *explain,
		Timeout:      common.timeout,
		MaxSolutions: common.maxSolutions,
		MaxDepth:     common.maxDepth,
		MaxDetach:    common.maxDetach,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return 3
		}
		return 0
	}
	if len(res.Plan) > 0 {
		for _, line := range res.Plan {
			fmt.Println(line)
		}
		return 0
	}
	if res.Summary != nil && len(res.Columns) == 0 {
		printSummary(res)
		return 0
	}
	w := csv.NewWriter(os.Stdout)
	w.Comma = '\t'
	if len(res.Columns) > 0 {
		_ = w.Write(res.Columns)
	}
	for _, row := range res.Rows {
		fields := make([]string, len(row))
		for i, v := range row {
			fields[i] = walk.FormatValue(v)
		}
		_ = w.Write(fields)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 3
	}
	if res.Summary != nil {
		printSummary(res)
	}
	return 0
}

func printSummary(res *walk.Result) {
	s := res.Summary
	fmt.Fprintf(os.Stderr, "nodes_created=%d nodes_deleted=%d edges_created=%d edges_deleted=%d properties_set=%d labels_added=%d labels_removed=%d\n",
		s.NodesCreated, s.NodesDeleted, s.EdgesCreated, s.EdgesDeleted, s.PropertiesSet, s.LabelsAdded, s.LabelsRemoved)
}

func cmdStats(args []string) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	common := addCommon(fs)
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	setupLogger(common.logLevel)
	db, err := common.open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "stats: %v\n", err)
		return 3
	}
	defer db.Close()
	stats, err := db.Stats(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "stats: %v\n", err)
		return 3
	}
	if *jsonOut {
		b, err := stats.JSON()
		if err != nil {
			return 3
		}
		fmt.Println(string(b))
		return 0
	}
	if err := stats.WriteTable(os.Stdout); err != nil {
		return 3
	}
	return 0
}

func cmdBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	common := addCommon(fs)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	setupLogger(common.logLevel)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "backup: exactly one destination path is required")
		return 1
	}
	db, err := common.open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		return 3
	}
	defer db.Close()
	if err := db.BackupFile(fs.Arg(0)); err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		return 3
	}
	fmt.Printf("backup written to %s\n", fs.Arg(0))
	return 0
}

func cmdCompact(args []string) int {
	fs := flag.NewFlagSet("compact", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	common := addCommon(fs)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	setupLogger(common.logLevel)
	db, err := common.open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "compact: %v\n", err)
		return 3
	}
	before, after, err := db.Compact()
	if err != nil {
		_ = db.Close()
		fmt.Fprintf(os.Stderr, "compact: %v\n", err)
		return 3
	}
	if err := db.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "compact: %v\n", err)
		return 3
	}
	fmt.Printf("compacted %s -> %s (saved %s)\n", walk.FormatSize(before), walk.FormatSize(after), walk.FormatSize(before-after))
	return 0
}

func cmdIndex(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "index: expected add|drop|list")
		return 1
	}
	sub := args[0]
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	common := addCommon(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	setupLogger(common.logLevel)
	db, err := common.open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "index: %v\n", err)
		return 3
	}
	defer db.Close()
	switch sub {
	case "add", "drop":
		if fs.NArg() != 2 {
			fmt.Fprintf(os.Stderr, "index %s: label and property are required\n", sub)
			return 1
		}
		label, prop := fs.Arg(0), fs.Arg(1)
		err := db.Update(context.Background(), func(e *executor.Exec) error {
			if sub == "add" {
				return e.CreateIndex(label, prop)
			}
			return e.DropIndex(label, prop)
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "index %s: %v\n", sub, err)
			return 2
		}
		fmt.Printf("index %s %s(%s)\n", sub, label, prop)
		return 0
	case "list":
		stats, err := db.Stats(context.Background())
		if err != nil {
			return 3
		}
		if len(stats.Indexes) == 0 {
			fmt.Println("no indexes")
			return 0
		}
		for label, props := range stats.Indexes {
			for _, prop := range props {
				fmt.Printf("%s(%s)\n", label, prop)
			}
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "index: unknown subcommand %q\n", sub)
		return 1
	}
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	common := addCommon(fs)
	addr := fs.String("addr", envDefault("WALK_ADDR", ":7474"), "listen address")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	setupLogger(common.logLevel)
	db, err := common.open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 3
	}
	defer db.Close()
	srv := walk.NewServer(db, walk.ServerOptions{
		Timeout:      common.timeout,
		MaxSolutions: common.maxSolutions,
		MaxDepth:     common.maxDepth,
		MaxDetach:    common.maxDetach,
		Logger:       slog.Default(),
	})
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	slog.Info("listening", "addr", *addr, "db", db.Path())
	err = httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 3
	}
	return 0
}
