package deep2

var store = map[string]any{}

func Store(k string, v any) { store[k] = v }
func Sync()                 { store = map[string]any{} }
