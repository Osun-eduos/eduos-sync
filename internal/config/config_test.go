package config

import "testing"

func TestPgxURL(t *testing.T) {
	got, err := PgxURL("postgresql://postgres@localhost:5432/eduos?schema=public")
	if err != nil || got != "postgresql://postgres@localhost:5432/eduos?search_path=public" {
		t.Errorf("got %q %v", got, err)
	}
	if _, err := PgxURL(""); err == nil {
		t.Error("empty URL accepted")
	}
}

func TestAPIURL(t *testing.T) {
	for in, want := range map[string]string{
		"osun-eduos-api:10000":    "http://osun-eduos-api:10000",
		"https://api.example.ng/": "https://api.example.ng",
		" http://localhost:3000 ": "http://localhost:3000",
	} {
		if got := APIURL(in); got != want {
			t.Errorf("APIURL(%q) = %q, want %q", in, got, want)
		}
	}
}
