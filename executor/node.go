package executor

import (
	"encoding/json"
	"maps"

	"github.com/23jdd/walk/utils"
)

type Node struct {
	id         uint64
	Labels     []string         `json:"labels"`
	Properties map[string]Value `json:"properties"`
}

func NewNodes() *Node {
	return &Node{Labels: make([]string, 0), Properties: make(map[string]Value)}
}
func NewNodesWithId(id uint64) *Node {
	n := NewNodes()
	n.id = id
	return n
}
func (n *Node) marshal() ([]byte, error) {
	return json.Marshal(n)
}
func (n *Node) unMarshal(data []byte) error {
	return json.Unmarshal(data, n)
}
func (n *Node) WithLabels(label ...string) *Node {
	n.Labels = utils.Union(n.Labels, label)
	return n
}
func (n *Node) WithProperties(properties map[string]Value) *Node {
	maps.Copy(n.Properties, properties)
	return n
}
