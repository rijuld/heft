// Edge cases kept apart from the main fixture so its exact counts stay put.
package main

import (
	"example.com/gen"
	"example.com/lit"
)

func main() {
	println(gen.Big())
	println(lit.Double(2))
}
