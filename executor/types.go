package executor

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

var ErrUnknownDataType = errors.New("unknown data type")
var ErrTypeMismatch = errors.New("type mismatch")

// DataType enumerates the five scalar types supported by the property graph.
type DataType uint8

const (
	NullType DataType = iota
	BoolType
	IntType
	FloatType
	StringType
)

// Compatibility aliases used by the original scaffold.
const (
	NULL   = NullType
	BOOL   = BoolType
	INT    = IntType
	FLOAT  = FloatType
	STRING = StringType
)

func (d DataType) String() string {
	switch d {
	case NullType:
		return "null"
	case BoolType:
		return "bool"
	case IntType:
		return "int"
	case FloatType:
		return "float"
	case StringType:
		return "string"
	default:
		return "unknown"
	}
}

// Value is a tagged scalar. It marshals to a plain JSON scalar, matching the
// on-disk representation required by F-01.
type Value struct {
	kind DataType
	i    int64
	f    float64
	s    string
	b    bool
}

func NullValue() Value           { return Value{kind: NullType} }
func BoolValue(v bool) Value     { return Value{kind: BoolType, b: v} }
func IntValue(v int64) Value     { return Value{kind: IntType, i: v} }
func FloatValue(v float64) Value { return Value{kind: FloatType, f: v} }
func StringValue(v string) Value { return Value{kind: StringType, s: v} }

// Legacy constructors kept for source compatibility.
func ValueNull() Value           { return NullValue() }
func ValueBool(v bool) Value     { return BoolValue(v) }
func ValueInt(v int64) Value     { return IntValue(v) }
func ValueFloat(v float64) Value { return FloatValue(v) }
func ValueString(v string) Value { return StringValue(v) }

func (v Value) Type() DataType   { return v.kind }
func (v Value) IsNull() bool     { return v.kind == NullType }
func (v Value) AsInt() int64     { return v.i }
func (v Value) AsBool() bool     { return v.b }
func (v Value) AsString() string { return v.s }
func (v Value) AsFloat() float64 {
	if v.kind == IntType {
		return float64(v.i)
	}
	return v.f
}

// Any returns the natural Go representation of the value.
func (v Value) Any() any {
	switch v.kind {
	case BoolType:
		return v.b
	case IntType:
		return v.i
	case FloatType:
		return v.f
	case StringType:
		return v.s
	default:
		return nil
	}
}

func (v Value) MarshalJSON() ([]byte, error) {
	switch v.kind {
	case NullType:
		return []byte("null"), nil
	case BoolType:
		return json.Marshal(v.b)
	case IntType:
		return []byte(strconv.FormatInt(v.i, 10)), nil
	case FloatType:
		return json.Marshal(v.f)
	case StringType:
		return json.Marshal(v.s)
	default:
		return nil, ErrUnknownDataType
	}
}

func (v *Value) UnmarshalJSON(data []byte) error {
	*v = NullValue()
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil
	}
	switch data[0] {
	case 't', 'f':
		var b bool
		if err := json.Unmarshal(data, &b); err != nil {
			return err
		}
		*v = BoolValue(b)
		return nil
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*v = StringValue(s)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(data, &num); err != nil {
		return fmt.Errorf("invalid property value %q: %w", string(data), err)
	}
	s := num.String()
	if len(s) > 0 && !bytes.ContainsAny([]byte(s), ".eE") {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			*v = IntValue(i)
			return nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid numeric property %q: %w", s, err)
	}
	*v = FloatValue(f)
	return nil
}

// Equal implements Cypher comparison equality for property matching. Numbers
// compare numerically across int/float; null never equals anything.
func (v Value) Equal(o Value) bool {
	if v.kind == NullType || o.kind == NullType {
		return false
	}
	if v.kind == o.kind {
		switch v.kind {
		case BoolType:
			return v.b == o.b
		case IntType:
			return v.i == o.i
		case FloatType:
			return v.f == o.f
		case StringType:
			return v.s == o.s
		}
	}
	if isNumber(v) && isNumber(o) {
		return v.AsFloat() == o.AsFloat()
	}
	return false
}

func isNumber(v Value) bool { return v.kind == IntType || v.kind == FloatType }

// Compare orders values using the global order null < bool < number < string.
// The second result is false when a null participates.
func (v Value) Compare(o Value) (int, bool) {
	if v.kind == NullType || o.kind == NullType {
		return 0, false
	}
	if v.kind == o.kind {
		switch v.kind {
		case BoolType:
			switch {
			case v.b == o.b:
				return 0, true
			case !v.b:
				return -1, true
			default:
				return 1, true
			}
		case IntType:
			switch {
			case v.i < o.i:
				return -1, true
			case v.i > o.i:
				return 1, true
			default:
				return 0, true
			}
		case FloatType:
			switch {
			case v.f < o.f:
				return -1, true
			case v.f > o.f:
				return 1, true
			case v.f == o.f:
				return 0, true
			default:
				// NaN: deterministic total order (after all numbers).
				return 1, true
			}
		case StringType:
			return bytes.Compare([]byte(v.s), []byte(o.s)), true
		}
	}
	if isNumber(v) && isNumber(o) {
		a, b := v.AsFloat(), o.AsFloat()
		switch {
		case a < b:
			return -1, true
		case a > b:
			return 1, true
		case a == b:
			return 0, true
		default:
			return 1, true
		}
	}
	ra, rb := orderRank(v.kind), orderRank(o.kind)
	switch {
	case ra < rb:
		return -1, true
	case ra > rb:
		return 1, true
	default:
		return 0, true
	}
}

func orderRank(d DataType) int {
	switch d {
	case NullType:
		return 0
	case BoolType:
		return 1
	case IntType, FloatType:
		return 2
	case StringType:
		return 3
	default:
		return 4
	}
}

// canonicalKey returns a type-strict byte encoding used for DISTINCT/GROUP BY.
// int(1) and float(1.0) intentionally differ.
func canonicalKey(v Value) []byte {
	var b bytes.Buffer
	switch v.kind {
	case NullType:
		b.WriteByte('N')
	case BoolType:
		b.WriteByte('B')
		if v.b {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
	case IntType:
		b.WriteByte('I')
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(v.i))
		b.Write(buf[:])
	case FloatType:
		b.WriteByte('F')
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(v.f))
		b.Write(buf[:])
	case StringType:
		b.WriteByte('S')
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], uint32(len(v.s)))
		b.Write(buf[:])
		b.WriteString(v.s)
	}
	return b.Bytes()
}

// encodeIndexValue returns an order-preserving encoding for the property
// index (F-25). Int64 uses sign-bit flipping; float64 uses the IEEE total
// order mapping; strings are raw UTF-8.
func encodeIndexValue(v Value) ([]byte, error) {
	switch v.kind {
	case IntType:
		buf := make([]byte, 9)
		buf[0] = 'I'
		binary.BigEndian.PutUint64(buf[1:], uint64(v.i)^(1<<63))
		return buf, nil
	case FloatType:
		bits := math.Float64bits(v.f)
		if bits&(1<<63) != 0 {
			bits = ^bits
		} else {
			bits |= 1 << 63
		}
		buf := make([]byte, 9)
		buf[0] = 'F'
		binary.BigEndian.PutUint64(buf[1:], bits)
		return buf, nil
	case StringType:
		buf := make([]byte, 1+len(v.s))
		buf[0] = 'S'
		copy(buf[1:], v.s)
		return buf, nil
	case BoolType:
		buf := []byte{'B', 0}
		if v.b {
			buf[1] = 1
		}
		return buf, nil
	default:
		return nil, fmt.Errorf("cannot index null value")
	}
}

// decodeIndexValue is the inverse of encodeIndexValue.
func decodeIndexValue(b []byte) (Value, error) {
	if len(b) < 1 {
		return NullValue(), ErrUnknownDataType
	}
	switch b[0] {
	case 'I':
		if len(b) != 9 {
			return NullValue(), ErrUnknownDataType
		}
		return IntValue(int64(binary.BigEndian.Uint64(b[1:]) ^ (1 << 63))), nil
	case 'F':
		if len(b) != 9 {
			return NullValue(), ErrUnknownDataType
		}
		bits := binary.BigEndian.Uint64(b[1:])
		if bits&(1<<63) != 0 {
			bits &^= 1 << 63
		} else {
			bits = ^bits
		}
		return FloatValue(math.Float64frombits(bits)), nil
	case 'S':
		return StringValue(string(b[1:])), nil
	case 'B':
		if len(b) != 2 {
			return NullValue(), ErrUnknownDataType
		}
		return BoolValue(b[1] != 0), nil
	}
	return NullValue(), ErrUnknownDataType
}
