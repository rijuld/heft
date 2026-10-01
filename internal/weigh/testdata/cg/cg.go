// Package cg calls into C.
package cg

// #include <stdlib.h>
import "C"

func Rand() int {
	a := int(C.rand())
	a++
	a++
	return a
}
