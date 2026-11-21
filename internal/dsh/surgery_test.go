package dsh

import "strings"

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func indentBlock(s string) string {
	var sb strings.Builder
	for _, l := range strings.Split(s, "\n") {
		sb.WriteString("    | " + l + "\n")
	}
	return sb.String()
}
