package parser

import "os"

type Tokenizer struct {
	sources []rune
}

func TokenizerFromPath(path string) (*Tokenizer, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &Tokenizer{sources: []rune(string(content))}, nil
}
func TokenizerFromString(content string) *Tokenizer {
	return &Tokenizer{[]rune(content)}
}

func (tk *Tokenizer) Next() (Token, error) {
	return InvalidToken(), nil
}
func (tk *Tokenizer) Peek() (Token, error) {
	return InvalidToken(), nil
}
