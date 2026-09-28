// Package sidefx is imported only for its init side effect.
package sidefx

var registry []string

func init() { registry = append(registry, "sidefx") }

// Register is never called by the app.
func Register(name string) { registry = append(registry, name) }
