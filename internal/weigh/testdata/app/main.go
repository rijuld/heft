// A tiny program with a deliberately lopsided set of dependencies.
package main

import (
	"fmt"
	"os"

	"example.com/big"
	megatiny "example.com/mega-tiny"
	"example.com/shared"
	_ "example.com/sidefx"
	"example.com/tiny"
)

func main() {
	name := "world"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	fmt.Println(tiny.PadLeft(name, 12))
	fmt.Println(big.Greet(name))
	fmt.Println("shared", shared.Version)
	fmt.Println(megatiny.Shout("done"))
}
