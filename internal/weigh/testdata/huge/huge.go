package huge

import (
	"example.com/deep1"
	"example.com/deep2"
)

// Decorate is the only function reached.
func Decorate(s string) string { return "*" + s + "*" }

func Retry(addr string)      { deep1.Dial(addr) }
func Set(k string, v any)    { deep2.Store(k, v) }
func Flush()                 { deep2.Sync() }
func unusedHelper(n int) int { return n * 2 }
