// A program in a go.work workspace that imports a sibling module.
package main

import "example.com/wslib"

func main() { println(wslib.A()) }
