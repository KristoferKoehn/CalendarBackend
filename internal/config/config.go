// Package config loads server configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime settings for the server.
type Config struct {
	DatabaseURL string
	Port        string
	TokenTTL    time.Duration
	CORSOrigins []string
	Seed        bool
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Port:        envOr("PORT", "4000"),
		TokenTTL:    time.Duration(envIntOr("TOKEN_TTL_HOURS", 24)) * time.Hour,
		CORSOrigins: corsOrigins(),
		Seed:        os.Getenv("SEED") == "true",
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return fallback
}

func corsOrigins() []string {
	raw := os.Getenv("CORS_ORIGINS")
	if raw == "" {
		return []string{
			"http://localhost:3000",
			"http://couchcollective.software",
			"http://192.168.1.169",
		}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
