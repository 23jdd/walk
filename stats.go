package walk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/23jdd/walk/executor"
	"go.etcd.io/bbolt"
)

// BucketStat reports physical bucket usage (F-21).
type BucketStat struct {
	Name        string `json:"name"`
	Keys        int    `json:"keys"`
	LeafPages   int    `json:"leaf_pages"`
	LeafBytes   int    `json:"leaf_bytes"`
	BranchPages int    `json:"branch_pages"`
	BranchBytes int    `json:"branch_bytes"`
}

// BoltStats is the JSON-friendly subset of bbolt.Stats.
type BoltStats struct {
	FreePageN     int           `json:"free_pages"`
	PendingPageN  int           `json:"pending_pages"`
	FreeAlloc     int           `json:"free_alloc"`
	FreelistInuse int           `json:"freelist_inuse"`
	TxN           int           `json:"tx_n"`
	OpenTxN       int           `json:"open_tx_n"`
	TxStats       bbolt.TxStats `json:"tx_stats"`
}

// GraphStats is the shared CLI/HTTP statistics structure (F-07/F-21).
type GraphStats struct {
	Path       string                 `json:"path"`
	FileSize   int64                  `json:"file_size"`
	Nodes      int                    `json:"nodes"`
	Edges      int                    `json:"edges"`
	NodeSeq    uint64                 `json:"node_seq"`
	EdgeSeq    uint64                 `json:"edge_seq"`
	Labels     []executor.LabelCount  `json:"labels"`
	Types      []executor.TypeCount   `json:"types"`
	Buckets    []BucketStat           `json:"buckets"`
	Indexes    map[string][]string    `json:"indexes"`
	Warnings   []string               `json:"warnings,omitempty"`
	Bolt       BoltStats              `json:"bolt"`
}

// Stats collects live monitoring data inside one read transaction.
func (db *DB) Stats(ctx context.Context) (*GraphStats, error) {
	out := &GraphStats{Path: db.path}
	info, err := os.Stat(db.path)
	if err == nil {
		out.FileSize = info.Size()
	}
	err = db.View(ctx, func(e *executor.Exec) error {
		out.Nodes = e.NodeCount()
		out.Edges = e.EdgeCount()
		out.Labels = e.LabelCounts()
		if len(out.Labels) > 20 {
			out.Labels = out.Labels[:20]
		}
		out.Types = e.TypeCounts()
		if len(out.Types) > 20 {
			out.Types = out.Types[:20]
		}
		out.Indexes = e.Indexes()
		out.NodeSeq = e.NodeSequence()
		out.EdgeSeq = e.EdgeSequence()
		for _, name := range executor.AllBuckets {
			u := e.BucketStats(name)
			out.Buckets = append(out.Buckets, BucketStat{
				Name:        u.Name,
				Keys:        u.Keys,
				LeafPages:   u.LeafPages,
				LeafBytes:   u.LeafBytes,
				BranchPages: u.BranchPages,
				BranchBytes: u.BranchBytes,
			})
		}
		out.Warnings = append(out.Warnings, e.CheckInvariants()...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	db.mu.RLock()
	if db.bolt != nil {
		bs := db.bolt.Stats()
		out.Bolt = BoltStats{
			FreePageN:     bs.FreePageN,
			PendingPageN:  bs.PendingPageN,
			FreeAlloc:     bs.FreeAlloc,
			FreelistInuse: bs.FreelistInuse,
			TxN:           bs.TxN,
			OpenTxN:       bs.OpenTxN,
			TxStats:       bs.TxStats,
		}
	}
	db.mu.RUnlock()
	sort.Slice(out.Buckets, func(i, j int) bool { return out.Buckets[i].Name < out.Buckets[j].Name })
	return out, nil
}

func (s *GraphStats) JSON() ([]byte, error) { return json.MarshalIndent(s, "", "  ") }

// WriteTable renders the human-readable CLI table.
func (s *GraphStats) WriteTable(w io.Writer) error {
	fmt.Fprintf(w, "database: %s (%s)\n", s.Path, FormatSize(s.FileSize))
	fmt.Fprintf(w, "nodes: %d   edges: %d\n", s.Nodes, s.Edges)
	fmt.Fprintf(w, "node id sequence: %d   edge id sequence: %d\n\n", s.NodeSeq, s.EdgeSeq)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LABEL\tCOUNT")
	for _, l := range s.Labels {
		fmt.Fprintf(tw, "%s\t%d\n", l.Label, l.Count)
	}
	fmt.Fprintln(tw, "\nTYPE\tCOUNT")
	for _, t := range s.Types {
		fmt.Fprintf(tw, "%s\t%d\n", t.Type, t.Count)
	}
	fmt.Fprintln(tw, "\nBUCKET\tKEYS\tLEAF BYTES\tBRANCH BYTES")
	for _, b := range s.Buckets {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\n", b.Name, b.Keys, b.LeafBytes, b.BranchBytes)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(s.Warnings) > 0 {
		fmt.Fprintln(w, "\nWARNINGS:")
		for _, warning := range s.Warnings {
			fmt.Fprintf(w, "  - %s\n", warning)
		}
	}
	return nil
}

// CheckInvariants is implemented in the executor package (see exec_stats.go).

