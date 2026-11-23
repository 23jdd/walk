package executor

import (
	"errors"
	"strconv"
)

var ErrUnknownDataType = errors.New("unknown data type")

// DataType include [NULL],[INT],[FLOAT],[BOOL],[STRING]
type DataType uint8

const (
	NULL DataType = iota
	// INT i64
	INT
	// FLOAT f64
	FLOAT
	BOOL
	STRING
)

type Value struct {
	Dt      DataType `json:"dt"`
	Content []byte   `json:"content"`
}

func (v Value) MarshalJSON() ([]byte, error) {
	switch v.Dt {
	case INT, FLOAT, NULL, BOOL, STRING:
		return v.Content, nil
	default:
		return nil, ErrUnknownDataType
	}
}

// TODO
func (v *Value) UnMarshalJson(data []byte) error {
	return nil
}
func valueNew(dt DataType, content []byte) Value {
	return Value{Dt: dt, Content: content}
}
func ValueNull() Value {
	return valueNew(NULL, []byte("null"))
}
func ValueInt(content int64) Value {
	return valueNew(INT, []byte(strconv.FormatInt(content, 10)))
}
func ValueFloat(content float64) Value {
	return valueNew(FLOAT, []byte(strconv.FormatFloat(content, 'E', -1, 64)))
}
func ValueBool(content bool) Value {
	return valueNew(BOOL, []byte(strconv.FormatBool(content)))
}
func ValueString(content string) Value {
	return valueNew(STRING, []byte("\""+content+"\""))
}
