// Package big is a framework-sized dependency: the app calls one function,
// which pulls in an engine and a chain of transitive modules.
package big

import (
	"example.com/big/internal/engine"
	"example.com/shared"
)

// Greet is the only function the app calls.
func Greet(name string) string {
	return engine.Render("hello, " + shared.Upper(name))
}

// Serve, Configure and Shutdown are the rest of the framework.
func Serve(addr string) error       { return engine.Listen(addr) }
func Configure(opts map[string]any) { engine.Apply(opts) }
func Shutdown()                     { engine.Stop() }
