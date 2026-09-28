// Package megatiny exists so that "tiny" is ambiguous as a substring but not as
// a path suffix, which exercises heft why's module lookup.
package megatiny

func Shout(s string) string { return s + "!" }
