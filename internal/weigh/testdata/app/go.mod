module example.com/app

go 1.26

require (
	example.com/big v0.0.0
	example.com/mega-tiny v0.0.0
	example.com/shared v0.0.0
	example.com/sidefx v0.0.0
	example.com/tiny v0.0.0
)

require (
	example.com/deep1 v0.0.0 // indirect
	example.com/deep2 v0.0.0 // indirect
	example.com/huge v0.0.0 // indirect
)

replace (
	example.com/big => ../big
	example.com/deep1 => ../deep1
	example.com/deep2 => ../deep2
	example.com/huge => ../huge
	example.com/mega-tiny => ../mega-tiny
	example.com/shared => ../shared
	example.com/sidefx => ../sidefx
	example.com/tiny => ../tiny
)
