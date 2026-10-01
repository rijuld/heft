// Package gen mimics generated code (goyacc, ragel) whose //line directives
// point back at a grammar file.
package gen

//line grammar.y:500
func Big() int {
	x := 1
	x++
	x++
	x++
	x++
	x++
//line grammar.y:10
	return x
}

func Small() int { return 1 }
