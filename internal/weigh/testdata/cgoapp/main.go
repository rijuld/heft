// A program whose only dependency uses cgo.
package main

import "example.com/cg"

func main() { println(cg.Rand()) }
