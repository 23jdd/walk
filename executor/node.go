package executor

import (
	"encoding/json"
	"fmt"
)

// Node is a labelled property-graph vertex.
type Node struct {
	ID     uint64
	Labels []string         `json:"labels"`
	Props  map[string]Value `json:"props"`
}

func NewNode() *Node {
	return &Node{Labels: make([]string, 0), Props: make(map[string]Value)}
}

func NewNodeWithID(id uint64) *Node {
	n := NewNode()
	n.ID = id
	return n
}

// Legacy names kept for compatibility with the scaffold.
func NewNodes() *Node                { return NewNode() }
func NewNodesWithId(id uint64) *Node { return NewNodeWithID(id) }

func (n *Node) Marshal() ([]byte, error) {
	if n.Props == nil {
		n.Props = make(map[string]Value)
	}
	return json.Marshal(n)
}

func (n *Node) Unmarshal(data []byte) error {
	type nodeJSON struct {
		Labels []string         `json:"labels"`
		Props  map[string]Value `json:"props"`
	}
	var v nodeJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	n.Labels = v.Labels
	if n.Labels == nil {
		n.Labels = make([]string, 0)
	}
	n.Props = v.Props
	if n.Props == nil {
		n.Props = make(map[string]Value)
	}
	return nil
}

func (n *Node) marshal() ([]byte, error)   { return n.Marshal() }
func (n *Node) unMarshal(b []byte) error  { return n.Unmarshal(b) }

func (n *Node) WithLabels(labels ...string) *Node {
	n.Labels = unionStrings(n.Labels, labels...)
	return n
}

func (n *Node) WithProperties(props map[string]Value) *Node {
	if n.Props == nil {
		n.Props = make(map[string]Value)
	}
	for k, v := range props {
		n.Props[k] = v
	}
	return n
}

func (n *Node) HasLabel(label string) bool {
	for _, l := range n.Labels {
		if l == label {
			return true
		}
	}
	return false
}

func (n *Node) String() string {
	return fmt.Sprintf("Node(%d:%v)", n.ID, n.Labels)
}

func unionStrings(a []string, b ...string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
