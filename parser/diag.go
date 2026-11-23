package parser

import (
	"fmt"
	"strings"
)

func formatDiagnostic(source string, span Span, msg string) string {
	var sb strings.Builder
	line := span.Start.Line
	col := span.Start.Column
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	sb.WriteString(fmt.Sprintf("line %d, column %d: %s", line, col, msg))
	lines := strings.Split(source, "\n")
	if line-1 < len(lines) {
		text := strings.TrimSuffix(lines[line-1], "\r")
		sb.WriteString("\n")
		sb.WriteString(text)
		sb.WriteString("\n")
		for i := 1; i < col && i <= len(text); i++ {
			if text[i-1] == '\t' {
				sb.WriteByte('\t')
			} else {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString("^")
	}
	return sb.String()
}
