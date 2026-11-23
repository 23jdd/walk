package executor

import (
	"encoding/binary"
)

// Bucket names. The layout follows F-01/F-02/F-03/F-25.
const (
	BucketNodes       = "nodes"
	BucketEdges       = "edges"
	BucketAdjOut      = "adj_out"
	BucketAdjIn       = "adj_in"
	BucketLabels      = "labels"
	BucketRelTypes    = "reltypes"
	BucketRelTypeByID = "reltype_by_id"
	BucketNodeLabel   = "node_label"
	BucketEdgeType    = "edge_type"
	BucketPropIndex   = "prop_idx"
	BucketMeta        = "meta"
)

// AllBuckets is the canonical bucket creation list.
var AllBuckets = []string{
	BucketNodes, BucketEdges, BucketAdjOut, BucketAdjIn,
	BucketLabels, BucketRelTypes, BucketRelTypeByID,
	BucketNodeLabel, BucketEdgeType, BucketPropIndex, BucketMeta,
}

func utob(v uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	return buf
}

func btou(b []byte) uint64 { return binary.BigEndian.Uint64(b) }

func key2(a, b uint64) []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[:8], a)
	binary.BigEndian.PutUint64(buf[8:], b)
	return buf
}

func key3(a, b, c uint64) []byte {
	buf := make([]byte, 24)
	binary.BigEndian.PutUint64(buf[:8], a)
	binary.BigEndian.PutUint64(buf[8:16], b)
	binary.BigEndian.PutUint64(buf[16:], c)
	return buf
}

func bytesKey(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func nodeLabelKey(labelID, nodeID uint64) []byte { return key2(labelID, nodeID) }
func edgeTypeKey(typeID, edgeID uint64) []byte   { return key2(typeID, edgeID) }

// propIndexPrefix returns labelID(8) | propLen(2) | prop.
func propIndexPrefix(labelID uint64, prop string) []byte {
	out := make([]byte, 10+len(prop))
	binary.BigEndian.PutUint64(out[:8], labelID)
	binary.BigEndian.PutUint16(out[8:10], uint16(len(prop)))
	copy(out[10:], prop)
	return out
}

// propIndexKey returns the full F-25 index key.
func propIndexKey(labelID uint64, prop string, value Value, nodeID uint64) ([]byte, error) {
	enc, err := encodeIndexValue(value)
	if err != nil {
		return nil, err
	}
	prefix := propIndexPrefix(labelID, prop)
	out := make([]byte, 0, len(prefix)+len(enc)+8)
	out = append(out, prefix...)
	out = append(out, enc...)
	out = append(out, utob(nodeID)...)
	return out, nil
}

// indexDefKey is stored in the meta bucket.
func indexDefKey(label, prop string) []byte {
	return bytesKey([]byte("idx\x00"), []byte(label), []byte{0}, []byte(prop))
}
