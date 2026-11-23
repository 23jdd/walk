package parser

type TokenType uint8

const (
	Invalid TokenType = iota
	Match
	Where
	Return
)
