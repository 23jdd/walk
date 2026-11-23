// Package httpapi exposes a walk graph over HTTP.
//
// It lives outside the walk package on purpose: walk itself is only the graph
// database/query engine, while the HTTP transport is an optional adapter used
// by cmd/walk and the single-file demo.
package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/23jdd/walk"
	"github.com/23jdd/walk/parser"
)

// Options configures the HTTP adapter.
type Options struct {
	Timeout      time.Duration
	MaxSolutions int
	MaxDepth     int
	MaxDetach    int
	Logger       *slog.Logger
}

// Server is the HTTP adapter around a walk database.
type Server struct {
	db   *walk.DB
	opts Options
}

func New(db *walk.DB, opts Options) *Server {
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{db: db, opts: opts}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/query", s.handleQuery)
	mux.HandleFunc("/update", s.handleUpdate)
	mux.HandleFunc("/backup", s.handleBackup)
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return s.corsMiddleware(s.recoverMiddleware(mux))
}

// corsMiddleware lets the single-file demo (opened via file:// or another
// origin) call the HTTP service directly.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			if rec := recover(); rec != nil {
				s.opts.Logger.Error("http panic", "panic", rec, "stack", string(debug.Stack()), "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
			}
			s.opts.Logger.Info("http", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) readCypher(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return "", false
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return "", false
	}
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "{") {
		var payload struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(body, &payload); err == nil && payload.Query != "" {
			text = payload.Query
		}
	}
	if text == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("empty query"))
		return "", false
	}
	return text, true
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	cypher, ok := s.readCypher(w, r)
	if !ok {
		return
	}
	q, err := parser.Parse(cypher)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if q.HasWrites() && !q.Explain {
		writeError(w, http.StatusBadRequest, fmt.Errorf("/query only accepts read-only queries; use /update"))
		return
	}
	s.execute(w, r, cypher)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	cypher, ok := s.readCypher(w, r)
	if !ok {
		return
	}
	q, err := parser.Parse(cypher)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !q.HasWrites() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("/update requires a write query"))
		return
	}
	s.execute(w, r, cypher)
}

func (s *Server) execute(w http.ResponseWriter, r *http.Request, cypher string) {
	ctx, cancel := context.WithTimeout(r.Context(), s.opts.Timeout)
	defer cancel()
	start := time.Now()
	res, err := s.db.Query(ctx, cypher, walk.QueryOptions{
		Timeout:      s.opts.Timeout,
		MaxSolutions: s.opts.MaxSolutions,
		MaxDepth:     s.opts.MaxDepth,
		MaxDetach:    s.opts.MaxDetach,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sum := sha256.Sum256([]byte(cypher))
	hash := hex.EncodeToString(sum[:])[:8]
	var nodesWritten, edgesWritten int64
	if res.Summary != nil {
		nodesWritten = res.Summary.NodesCreated
		edgesWritten = res.Summary.EdgesCreated
	}
	s.opts.Logger.Info("query",
		"query_hash", hash,
		"duration_ms", time.Since(start).Milliseconds(),
		"solution_count", len(res.Rows),
		"timeout_hit", ctx.Err() == context.DeadlineExceeded,
		"nodes_written", nodesWritten,
		"edges_written", edgesWritten,
	)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="graph.db"`)
	if err := s.db.BackupTo(w); err != nil {
		s.opts.Logger.Warn("backup stream failed", "error", err)
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.opts.Timeout)
	defer cancel()
	stats, err := s.db.Stats(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
