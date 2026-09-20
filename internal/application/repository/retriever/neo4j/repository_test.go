package neo4j

import "testing"

// TestPropToString guards the page_id/page_slug reader: the same property may
// come back as a plain string or as a 1-element list (the backend writes props
// as []string, but nodes created before that change carry scalars), and a
// missing property must yield "" rather than a panic or a "<nil>" string.
func TestPropToString(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want string
	}{
		{"nil", nil, ""},
		{"scalar string", "entity-b-84313f21b5", "entity-b-84313f21b5"},
		{"one-element list", []interface{}{"3956c88c-9650-4f37-aa18-71ca12b0138f"}, "3956c88c-9650-4f37-aa18-71ca12b0138f"},
		{"string slice", []string{"summary/abc"}, "summary/abc"},
		{"empty list", []interface{}{}, ""},
	}
	for _, c := range cases {
		if got := propToString(c.in); got != c.want {
			t.Errorf("%s: propToString(%#v) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
