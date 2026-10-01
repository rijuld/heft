module example.com/edgeapp

go 1.26

require (
	example.com/gen v0.0.0
	example.com/lit v0.0.0
)

replace (
	example.com/gen => ../gen
	example.com/lit => ../lit
)
