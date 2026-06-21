package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AccountNo           string
	DiscordWebhookURL   string
	LowBalanceThreshold float64
	DBPath              string
	NescoBaseURL        string
	HTTPTimeout         time.Duration
}

func LoadConfig() (*Config, error) {
	// Best-effort .env load — production should set real env vars.
	_ = godotenv.Load()

	c := &Config{
		AccountNo:           os.Getenv("NESCO_ACCOUNT_NO"),
		DiscordWebhookURL:   os.Getenv("DISCORD_WEBHOOK_URL"),
		LowBalanceThreshold: parseFloatEnv("LOW_BALANCE_THRESHOLD", 500),
		DBPath:              envDefault("DB_PATH", "./nesco.db"),
		NescoBaseURL:        envDefault("NESCO_BASE_URL", "https://customer.nesco.gov.bd"),
		HTTPTimeout:         parseDurationEnv("HTTP_TIMEOUT", 15*time.Second),
	}

	if c.AccountNo == "" {
		return nil, fmt.Errorf("NESCO_ACCOUNT_NO is required")
	}
	if c.DiscordWebhookURL == "" {
		return nil, fmt.Errorf("DISCORD_WEBHOOK_URL is required")
	}
	return c, nil
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseFloatEnv(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

