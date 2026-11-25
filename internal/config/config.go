// Package config reads the service settings from the environment.
package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port         string
	DatabaseURL  string
	JWTSecret    string
	APIURL       string
	RegisterDays int
	MaxEvents    int
	MaxBodyBytes int64
}

// Load reads settings from the environment. It accepts the same DATABASE_URL as eduos-api, including Prisma's
// ?schema= parameter, which it turns into a search_path.
func Load() (Config, error) {
	c := Config{
		Port:         env("PORT", "4000"),
		JWTSecret:    os.Getenv("JWT_ACCESS_SECRET"),
		APIURL:       APIURL(env("API_URL", "http://localhost:3000")),
		RegisterDays: envInt("SNAPSHOT_REGISTER_DAYS", 14),
		MaxEvents:    envInt("SYNC_MAX_EVENTS", 200),
		MaxBodyBytes: int64(envInt("SYNC_MAX_BODY_BYTES", 2<<20)),
	}
	if len(c.JWTSecret) < 32 {
		return c, errors.New("JWT_ACCESS_SECRET must be set to the same value as eduos-api (at least 32 characters)")
	}
	db, err := PgxURL(os.Getenv("DATABASE_URL"))
	if err != nil {
		return c, err
	}
	c.DatabaseURL = db
	return c, nil
}

// APIURL accepts a full URL or a private-network host:port (as Render's hostport gives), which gets http://.
func APIURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return raw
}

// PgxURL converts a Prisma connection string into one pgx accepts.
func PgxURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("DATABASE_URL must be set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("DATABASE_URL is not a valid URL")
	}
	q := u.Query()
	if s := q.Get("schema"); s != "" {
		q.Del("schema")
		q.Set("search_path", s)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}
