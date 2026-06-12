package config

import (
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                string
	DatabaseURL         string
	JWTSecret           string
	GoogleClientID      string
	GoogleSecret        string
	GoogleCallbackURL   string
	AllowedEmails       []string
	AllowedRedirectURLs []string
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	return &Config{
		Port:                getEnv("PORT", "8080"),
		DatabaseURL:         mustGetEnv("DATABASE_URL"),
		JWTSecret:           mustGetEnv("JWT_SECRET"),
		GoogleClientID:      mustGetEnv("GOOGLE_CLIENT_ID"),
		GoogleSecret:        mustGetEnv("GOOGLE_CLIENT_SECRET"),
		GoogleCallbackURL:   mustGetEnv("GOOGLE_CALLBACK_URL"),
		AllowedEmails:       parseList(getEnv("ALLOWED_EMAILS", "")),
		AllowedRedirectURLs: parseList(getEnv("ALLOWED_REDIRECT_URLS", "")),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
