// Package tiny is a left-pad-sized dependency: the app uses one small function.
package tiny

// PadLeft pads s with spaces on the left to width n.
func PadLeft(s string, n int) string {
	for len(s) < n {
		s = " " + s
	}
	return s
}

// PadRight pads s with spaces on the right to width n.
func PadRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

// Center centers s in a field of width n.
func Center(s string, n int) string {
	return PadRight(PadLeft(s, (n+len(s))/2), n)
}
