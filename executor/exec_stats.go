package executor

import "fmt"

// BucketUsage is the physical usage summary of one bucket (F-21).
type BucketUsage struct {
	Name        string
	Keys        int
	LeafPages   int
	LeafBytes   int
	BranchPages int
	BranchBytes int
}

// BucketKeyCount returns the number of key/value pairs in a bucket.
func (e *Exec) BucketKeyCount(name string) int {
	return e.bucket(name).Stats().KeyN
}

// BucketStats exposes bbolt's physical page statistics.
func (e *Exec) BucketStats(name string) BucketUsage {
	st := e.bucket(name).Stats()
	return BucketUsage{
		Name:        name,
		Keys:        st.KeyN,
		LeafPages:   st.LeafPageN,
		LeafBytes:   st.LeafInuse,
		BranchPages: st.BranchPageN,
		BranchBytes: st.BranchInuse,
	}
}

func (e *Exec) NodeSequence() uint64 { return e.bucket(BucketNodes).Sequence() }
func (e *Exec) EdgeSequence() uint64 { return e.bucket(BucketEdges).Sequence() }

// CheckInvariants performs the F-21 cross-checks and returns warnings.
func (e *Exec) CheckInvariants() []string {
	var warnings []string
	expectedPostings := 0
	for id := range e.AllNodeIDs() {
		n, err := e.GetNode(id)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("unreadable node %d: %v", id, err))
			continue
		}
		expectedPostings += len(n.Labels)
	}
	actualPostings := e.BucketKeyCount(BucketNodeLabel)
	if expectedPostings != actualPostings {
		warnings = append(warnings, fmt.Sprintf("node_label postings %d != expected %d", actualPostings, expectedPostings))
	}
	edges := e.EdgeCount()
	if out := e.BucketKeyCount(BucketAdjOut); out != edges {
		warnings = append(warnings, fmt.Sprintf("adj_out entries %d != edges %d", out, edges))
	}
	if in := e.BucketKeyCount(BucketAdjIn); in != edges {
		warnings = append(warnings, fmt.Sprintf("adj_in entries %d != edges %d", in, edges))
	}
	if types := e.BucketKeyCount(BucketEdgeType); types != edges {
		warnings = append(warnings, fmt.Sprintf("edge_type entries %d != edges %d", types, edges))
	}
	return warnings
}
