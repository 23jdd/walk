package executor

import (
	"fmt"
	"strings"
)

// Size limits from F-01. Violations are reported explicitly, never truncated.
const (
	MaxPropsPerObject  = 128
	MaxPropNameBytes   = 256
	MaxPropValueBytes  = 64 * 1024
	MaxObjectValueSize = 1 * 1024 * 1024
	MaxNameBytes       = 256
)

func verifyName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s must not be empty", kind)
	}
	if len(name) > MaxNameBytes {
		return fmt.Errorf("%s %q exceeds %d bytes", kind, name, MaxNameBytes)
	}
	return nil
}

func verifyProps(props map[string]Value) error {
	if len(props) > MaxPropsPerObject {
		return fmt.Errorf("property count %d exceeds limit %d", len(props), MaxPropsPerObject)
	}
	for k, v := range props {
		if k == "" {
			return fmt.Errorf("property name must not be empty")
		}
		if len(k) > MaxPropNameBytes {
			return fmt.Errorf("property name %q exceeds %d bytes", k, MaxPropNameBytes)
		}
		if v.IsNull() {
			continue
		}
		enc, err := v.MarshalJSON()
		if err != nil {
			return err
		}
		if len(enc) > MaxPropValueBytes {
			return fmt.Errorf("property %q value size %d exceeds %d bytes", k, len(enc), MaxPropValueBytes)
		}
	}
	return nil
}

func verifyNode(n *Node) error {
	if n == nil {
		return fmt.Errorf("nil node")
	}
	for _, l := range n.Labels {
		if err := verifyName("label", l); err != nil {
			return err
		}
	}
	if err := verifyProps(n.Props); err != nil {
		return err
	}
	b, err := n.Marshal()
	if err != nil {
		return err
	}
	if len(b) > MaxObjectValueSize {
		return fmt.Errorf("node value size %d exceeds %d bytes", len(b), MaxObjectValueSize)
	}
	return nil
}

func verifyEdge(e *Edge) error {
	if e == nil {
		return fmt.Errorf("nil edge")
	}
	if strings.TrimSpace(e.Type) == "" {
		return fmt.Errorf("relationship type must not be empty")
	}
	if err := verifyName("relationship type", e.Type); err != nil {
		return err
	}
	if err := verifyProps(e.Props); err != nil {
		return err
	}
	b, err := e.Marshal()
	if err != nil {
		return err
	}
	if len(b) > MaxObjectValueSize {
		return fmt.Errorf("edge value size %d exceeds %d bytes", len(b), MaxObjectValueSize)
	}
	return nil
}

// cleanNullProps removes null-valued properties (F-14: null is not written).
func cleanNullProps(props map[string]Value) map[string]Value {
	if len(props) == 0 {
		return props
	}
	for k, v := range props {
		if v.IsNull() {
			delete(props, k)
		}
	}
	return props
}
