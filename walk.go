package walk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/23jdd/walk/executor"
	"go.etcd.io/bbolt"
)

var ErrClosed = errors.New("database is closed")

// Config holds process-wide defaults (F-24).
type Config struct {
	Timeout      time.Duration
	MaxSolutions int
	MaxDepth     int
	MaxDetach    int
	BatchSize    int
}

func DefaultConfig() Config {
	return Config{
		Timeout:      5 * time.Second,
		MaxSolutions: 1_000_000,
		MaxDepth:     8,
		MaxDetach:    100_000,
		BatchSize:    5_000,
	}
}

func (c Config) execConfig() executor.Config {
	return executor.Config{
		MaxSolutions: c.MaxSolutions,
		MaxDepth:     c.MaxDepth,
		MaxDetach:    c.MaxDetach,
	}
}

// DB is a bbolt-backed property graph.
type DB struct {
	mu   sync.RWMutex
	bolt *bbolt.DB
	path string
	cfg  Config
}

// Open opens (or creates) a graph database at path.
func Open(path string) (*DB, error) {
	return OpenWithConfig(path, DefaultConfig())
}

func OpenWithConfig(path string, cfg Config) (*DB, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxSolutions <= 0 {
		cfg.MaxSolutions = 1_000_000
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 8
	}
	if cfg.MaxDetach <= 0 {
		cfg.MaxDetach = 100_000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 5_000
	}
	bolt, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	db := &DB{bolt: bolt, path: path, cfg: cfg}
	if err := db.initBuckets(); err != nil {
		_ = bolt.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) initBuckets() error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		for _, name := range executor.AllBuckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		meta := tx.Bucket([]byte(executor.BucketMeta))
		if meta.Get([]byte("version")) == nil {
			if err := meta.Put([]byte("version"), []byte("1")); err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) Path() string    { return db.path }
func (db *DB) Config() Config  { return db.cfg }
func (db *DB) Bolt() *bbolt.DB { return db.bolt }

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.bolt == nil {
		return ErrClosed
	}
	err := db.bolt.Close()
	db.bolt = nil
	return err
}

// View runs a read-only transaction. The context aborts long scans through
// executor.Check; bbolt itself is never force-killed (F-04).
func (db *DB) View(ctx context.Context, fn func(*executor.Exec) error) error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.bolt == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return db.bolt.View(func(tx *bbolt.Tx) error {
		e := executor.NewExec(tx, ctx, db.cfg.execConfig())
		err := fn(e)
		if err != nil {
			return err
		}
		return e.Err()
	})
}

// Update runs an atomic read-write transaction. Any returned error rolls the
// transaction back, giving write queries all-query atomicity (F-04/F-14).
func (db *DB) Update(ctx context.Context, fn func(*executor.Exec) error) error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.bolt == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		e := executor.NewExec(tx, ctx, db.cfg.execConfig())
		if err := fn(e); err != nil {
			return err
		}
		return e.Err()
	})
}

// BackupTo streams a consistent snapshot using a read-only transaction
// (F-19).
func (db *DB) BackupTo(w io.Writer) error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.bolt == nil {
		return ErrClosed
	}
	return db.bolt.View(func(tx *bbolt.Tx) error {
		_, err := tx.WriteTo(w)
		return err
	})
}

// BackupFile writes a hot snapshot to path.
func (db *DB) BackupFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := db.BackupTo(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Compact copies live data into a fresh file, verifies it, then atomically
// replaces the database (F-20).
func (db *DB) Compact() (before, after int64, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.bolt == nil {
		return 0, 0, ErrClosed
	}
	if err := db.bolt.Sync(); err != nil {
		return 0, 0, err
	}
	info, err := os.Stat(db.path)
	if err != nil {
		return 0, 0, err
	}
	before = info.Size()

	tmp := db.path + ".compact.tmp"
	_ = os.Remove(tmp)
	dst, err := bbolt.Open(tmp, 0o600, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return before, 0, err
	}
	if err := bbolt.Compact(dst, db.bolt, 0); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmp)
		return before, 0, err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmp)
		return before, 0, err
	}
	if err := verifyCompact(db.bolt, tmp); err != nil {
		_ = os.Remove(tmp)
		return before, 0, err
	}
	if err := db.bolt.Close(); err != nil {
		return before, 0, err
	}
	db.bolt = nil
	old := db.path + ".old"
	_ = os.Remove(old)
	if err := os.Rename(db.path, old); err != nil {
		_ = db.reopen()
		_ = os.Remove(tmp)
		return before, 0, err
	}
	if err := os.Rename(tmp, db.path); err != nil {
		_ = os.Rename(old, db.path)
		_ = db.reopen()
		return before, 0, err
	}
	_ = os.Remove(old)
	if err := db.reopen(); err != nil {
		return before, 0, err
	}
	info, err = os.Stat(db.path)
	if err != nil {
		return before, 0, err
	}
	after = info.Size()
	return before, after, nil
}

func (db *DB) reopen() error {
	bolt, err := bbolt.Open(db.path, 0o600, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return err
	}
	db.bolt = bolt
	return nil
}

// verifyCompact checks readability and record counts before replacing the
// original file.
func verifyCompact(src *bbolt.DB, path string) error {
	dst, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 5 * time.Second, ReadOnly: true})
	if err != nil {
		return err
	}
	defer dst.Close()
	var srcNodes, srcEdges, dstNodes, dstEdges int
	if err := src.View(func(tx *bbolt.Tx) error {
		nb := tx.Bucket([]byte(executor.BucketNodes))
		eb := tx.Bucket([]byte(executor.BucketEdges))
		if nb == nil || eb == nil {
			return errors.New("source database is missing core buckets")
		}
		srcNodes = nb.Stats().KeyN
		srcEdges = eb.Stats().KeyN
		return nil
	}); err != nil {
		return err
	}
	if err := dst.View(func(tx *bbolt.Tx) error {
		nb := tx.Bucket([]byte(executor.BucketNodes))
		eb := tx.Bucket([]byte(executor.BucketEdges))
		if nb == nil || eb == nil {
			return errors.New("compacted database is missing core buckets")
		}
		dstNodes = nb.Stats().KeyN
		dstEdges = eb.Stats().KeyN
		return nil
	}); err != nil {
		return err
	}
	if srcNodes != dstNodes || srcEdges != dstEdges {
		return fmt.Errorf("compact verification failed: nodes %d/%d edges %d/%d", srcNodes, dstNodes, srcEdges, dstEdges)
	}
	return nil
}

// EnsureDir makes sure the parent directory exists.
func EnsureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// FormatSize renders bytes for CLI output.
func FormatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
