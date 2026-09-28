// Package shared is imported by the app *and* by big.
package shared

import "strings"

const Version = "1.0.0"

func Upper(s string) string { return strings.ToUpper(s) }
func Lower(s string) string { return strings.ToLower(s) }
