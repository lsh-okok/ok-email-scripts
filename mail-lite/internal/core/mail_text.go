package core

import "strings"

// Trim field boundaries without changing spaces inside credentials or links.
func mailTXTLine(fields ...string) string {
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return strings.Join(fields, "----")
}
