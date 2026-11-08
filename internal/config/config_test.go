package config

import (
	"net/url"
	"strings"
	"testing"
)

func TestConfiguration(t *testing.T) {
	for _, key := range []string{"DATABASE_URL", "ALGHO", "AUTO_MIGRATE", "ALLOW_LEGACY_PASSWORDS", "HTTP_ADDR"} {
		t.Setenv(key, "")
	}
	t.Setenv("SECRET_KEY", strings.Repeat("s", 32))
	t.Setenv("DB_HOST", "::1")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_NAME", "kan db")
	t.Setenv("DB_USER", "user@tenant")
	t.Setenv("DB_PASS", "p@ss:/?#&")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := u.User.Password()
	if p != "p@ss:/?#&" || u.User.Username() != "user@tenant" || u.Host != "[::1]:5432" {
		t.Fatal("connection values were not safely escaped")
	}
	if c.AutoMigrate || c.AllowLegacyPasswords {
		t.Fatal("unsafe default options")
	}
	t.Setenv("ALLOW_LEGACY_PASSWORDS", "not-a-bool")
	if _, err := Load(); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	t.Setenv("ALLOW_LEGACY_PASSWORDS", "false")
	t.Setenv("SECRET_KEY", "short")
	if _, err := Load(); err == nil {
		t.Fatal("short secret accepted")
	}
}
