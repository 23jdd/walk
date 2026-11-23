package walk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/23jdd/walk/parser"
)

// ServerOptions configures the HTTP interface (F-18).
type ServerOptions struct {
	Timeout      time.Duration
	MaxSolutions int
	MaxDepth     int
	MaxDetach    int
	Logger       *slog.Logger
}

// Server exposes the graph over HTTP.
type Server struct {
	db   *DB
	opts ServerOptions
}

func NewServer(db *DB, opts ServerOptions) *Server {
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
	return s.recoverMiddleware(mux)
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
	if q.HasWrites() {
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
	res, err := s.db.Query(ctx, cypher, QueryOptions{
		Timeout:      s.opts.Timeout,
		MaxSolutions: s.opts.MaxSolutions,
		MaxDepth:     s.opts.MaxDepth,
		MaxDetach:    s.opts.MaxDetach,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.opts.Logger.Info("query",
		"duration_ms", time.Since(start).Milliseconds(),
		"solutions", len(res.Rows),
		"timeout_hit", ctx.Err() == context.DeadlineExceeded,
		"query", cypher,
	)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	if s.db.bolt == nil {
		writeError(w, http.StatusServiceUnavailable, ErrClosed)
		return
	}
	tx, err := s.db.bolt.Begin(false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="graph.db"`)
	w.Header().Set("Content-Length", strconv.FormatInt(tx.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if _, err := tx.WriteTo(w); err != nil {
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
