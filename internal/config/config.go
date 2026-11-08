package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Address              string
	DatabaseURL          string
	Secret               string
	AutoMigrate          bool
	AllowLegacyPasswords bool
}

func Load() (Config, error) {
	c := Config{Address: env("HTTP_ADDR", ":5002"), Secret: os.Getenv("SECRET_KEY")}
	if len(c.Secret) < 32 || strings.HasPrefix(c.Secret, "replace-") {
		return c, errors.New("SECRET_KEY must be a non-placeholder secret containing at least 32 bytes")
	}
	if env("ALGHO", "HS256") != "HS256" {
		return c, errors.New("only ALGHO=HS256 is supported")
	}
	var err error
	if c.AutoMigrate, err = boolean("AUTO_MIGRATE"); err != nil {
		return c, err
	}
	if c.AllowLegacyPasswords, err = boolean("ALLOW_LEGACY_PASSWORDS"); err != nil {
		return c, err
	}
	c.DatabaseURL = os.Getenv("DATABASE_URL")
	if c.DatabaseURL == "" {
		for _, key := range []string{"DB_HOST", "DB_NAME", "DB_USER", "DB_PASS"} {
			if os.Getenv(key) == "" {
				return c, fmt.Errorf("%s is required when DATABASE_URL is unset", key)
			}
		}
		port := env("DB_PORT", "5432")
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return c, errors.New("invalid DB_PORT")
		}
		u := url.URL{Scheme: "postgresql", User: url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASS")), Host: net.JoinHostPort(os.Getenv("DB_HOST"), port), Path: "/" + os.Getenv("DB_NAME")}
		q := u.Query()
		q.Set("sslmode", env("DB_SSLMODE", "prefer"))
		u.RawQuery = q.Encode()
		c.DatabaseURL = u.String()
	}
	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func boolean(key string) (bool, error) {
	v := env(key, "false")
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return b, nil
}
