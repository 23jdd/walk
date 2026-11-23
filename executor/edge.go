package executor

import (
	"encoding/json"
	"fmt"
)

// Edge is a directed, typed relationship between two nodes.
type Edge struct {
	ID    uint64
	From  uint64           `json:"from"`
	To    uint64           `json:"to"`
	Type  string           `json:"type"`
	Props map[string]Value `json:"props"`
}

func NewEdge() *Edge {
	return &Edge{Props: make(map[string]Value)}
}

func NewEdgeWithLink(from, to uint64, typ string) *Edge {
	e := NewEdge()
	e.From, e.To, e.Type = from, to, typ
	return e
}

func (e *Edge) Marshal() ([]byte, error) {
	if e.Props == nil {
		e.Props = make(map[string]Value)
	}
	return json.Marshal(e)
}

func (e *Edge) Unmarshal(data []byte) error {
	type edgeJSON struct {
		From       uint64           `json:"from"`
		To         uint64           `json:"to"`
		Type       string           `json:"type"`
		Relation   string           `json:"relation"`
		Props      map[string]Value `json:"props"`
		Properties map[string]Value `json:"properties"`
	}
	var v edgeJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Type == "" {
		v.Type = v.Relation
	}
	if v.Props == nil {
		v.Props = v.Properties
	}
	e.From, e.To, e.Type = v.From, v.To, v.Type
	e.Props = v.Props
	if e.Props == nil {
		e.Props = make(map[string]Value)
	}
	return nil
}

func (e *Edge) marshal() ([]byte, error) { return e.Marshal() }
func (e *Edge) unMarshal(b []byte) error { return e.Unmarshal(b) }

func (e *Edge) WithProperties(props map[string]Value) *Edge {
	if e.Props == nil {
		e.Props = make(map[string]Value)
	}
	for k, v := range props {
		e.Props[k] = v
	}
	return e
}

func (e *Edge) String() string {
	return fmt.Sprintf("Edge(%d)-[%s]->(%d)", e.From, e.Type, e.To)
}
