// Package lit keeps its logic in package-level function literals.
package lit

var Double = func(n int) int {
	return n * 2
}

var Unused = func(s string) string {
	s += "!"
	return s
}
