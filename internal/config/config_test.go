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
