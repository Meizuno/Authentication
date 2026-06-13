package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// minJWTSecretLen is the floor for an HS256 signing secret. Anything shorter is
// brute-forceable and refused at boot.
const minJWTSecretLen = 32

type Config struct {
	Port                string
	DatabaseURL         string
	JWTSecret           string
	GoogleClientID      string
	GoogleSecret        string
	GoogleCallbackURL   string
	AllowedEmails       []string
	AllowedRedirectURLs []string
	CookieSecure        bool
	CookieDomain        string
	RateLimitRPS        float64
	RateLimitBurst      int
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	cfg := &Config{
		Port:                getEnv("PORT", "8080"),
		DatabaseURL:         mustGetEnv("DATABASE_URL"),
		JWTSecret:           mustGetEnv("JWT_SECRET"),
		GoogleClientID:      mustGetEnv("GOOGLE_CLIENT_ID"),
		GoogleSecret:        mustGetEnv("GOOGLE_CLIENT_SECRET"),
		GoogleCallbackURL:   mustGetEnv("GOOGLE_CALLBACK_URL"),
		AllowedEmails:       parseList(getEnv("ALLOWED_EMAILS", "")),
		AllowedRedirectURLs: parseList(getEnv("ALLOWED_REDIRECT_URLS", "")),
		CookieSecure:        getBoolEnv("COOKIE_SECURE", true),
		CookieDomain:        getEnv("COOKIE_DOMAIN", ""),
		RateLimitRPS:        getFloatEnv("RATE_LIMIT_RPS", 5),
		RateLimitBurst:      getIntEnv("RATE_LIMIT_BURST", 10),
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	return cfg
}

// Validate enforces invariants that must hold for the service to be safe to run.
func (c *Config) Validate() error {
	if len(c.JWTSecret) < minJWTSecretLen {
		return fmt.Errorf("JWT_SECRET must be at least %d bytes, got %d", minJWTSecretLen, len(c.JWTSecret))
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getBoolEnv(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getIntEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getFloatEnv(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}

func parseList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
