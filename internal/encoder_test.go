package internal

import (
	"testing"
)

// A Go map has no order, so its JSON form must not depend on map iteration order.
// Keys are sorted, as encoding/json does and as BigQuery renders JSON objects.
func TestValueFromGoValue_MapKeysAreSorted(t *testing.T) {
	input := map[string]interface{}{
		"nested": map[string]interface{}{"b": 2, "a": 1},
		"any":    "object",
		"zzz":    true,
		"mid":    []interface{}{map[string]interface{}{"y": 1, "x": 2}},
	}
	want := `{"any":"object","mid":[{"x":2,"y":1}],"nested":{"a":1,"b":2},"zzz":true}`
	// Go randomises map iteration per range loop, so repeat to make an ordering bug surface.
	for i := 0; i < 50; i++ {
		value, err := ValueFromGoValue(input)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.ToJSON()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("iteration %d: got %s, want %s", i, got, want)
		}
	}
}
