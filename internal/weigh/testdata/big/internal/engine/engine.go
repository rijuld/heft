package engine

import "example.com/huge"

// Render is reached through big.Greet.
func Render(s string) string { return huge.Decorate(s) }

func Listen(addr string) error {
	for i := 0; i < 3; i++ {
		huge.Retry(addr)
	}
	return nil
}

func Apply(opts map[string]any) {
	for k, v := range opts {
		huge.Set(k, v)
	}
}

func Stop() { huge.Flush() }
