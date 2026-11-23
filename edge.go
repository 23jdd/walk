package walk

import (
	"encoding/json"
	"maps"
)

type Edge struct {
	id         uint64
	From       uint64           `json:"from"`
	To         uint64           `json:"to"`
	Relation   string           `json:"relation"`
	Properties map[string]Value `json:"properties"`
}

func (e *Edge) marshal() ([]byte, error) {
	return json.Marshal(e)
}
func (e *Edge) unMarshal(data []byte) error {
	return json.Unmarshal(data, e)
}
func NewEdge() *Edge {
	return &Edge{
		Properties: make(map[string]Value),
	}
}
func (e *Edge) NewEdgeWithLink(from uint64, to uint64, relation string) *Edge {
	return &Edge{
		From: from, To: to, Relation: relation, Properties: make(map[string]Value),
	}
}
func (e *Edge) WithProperties(properties map[string]Value) *Edge {
	maps.Copy(e.Properties, properties)
	return e
}
