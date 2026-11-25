package names

import "testing"

func TestClass(t *testing.T) {
	cases := map[[2]string]string{
		{"JSS_2", "A"}:        "JSS 2A",
		{"SSS_1", "B"}:        "SSS 1B",
		{"PRIMARY_3", "A"}:    "Primary 3A",
		{"PRIMARY_3", "Gold"}: "Primary 3 Gold",
	}
	for in, want := range cases {
		if got := Class(in[0], in[1]); got != want {
			t.Errorf("Class(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	if Term(2) != "Second term" {
		t.Error("Term(2)")
	}
}
