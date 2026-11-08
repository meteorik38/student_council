package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestPasswordHashesAndLegacyUpgrade(t *testing.T) {
	password := strings.Repeat("длинный-пароль", 8)
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if hash == password || !strings.HasPrefix(hash, hashPrefix) {
		t.Fatal("password was not hashed")
	}
	if ok, legacy := VerifyPassword(password, hash, false); !ok || legacy {
		t.Fatal("hash verification failed")
	}
	if ok, _ := VerifyPassword("wrong", hash, true); ok {
		t.Fatal("wrong password accepted")
	}
	if ok, _ := VerifyPassword(password, password, false); ok {
		t.Fatal("plaintext accepted without opt-in")
	}
	if ok, legacy := VerifyPassword(password, password, true); !ok || !legacy {
		t.Fatal("legacy upgrade not detected")
	}
	for _, bad := range []string{"$argon2id$garbage", hashPrefix + "!!!$!!!", "$argon2id$v=19$m=999999999,t=1,p=4$a$b"} {
		if ok, _ := VerifyPassword(bad, bad, true); ok {
			t.Fatal("malformed hash accepted as plaintext")
		}
	}
	if _, err := HashPassword(strings.Repeat("x", 1025)); err == nil {
		t.Fatal("oversized password accepted")
	}
}
func TestJWTValidation(t *testing.T) {
	secret := "test-secret-longer-than-thirty-two-bytes"
	tokens := NewTokens(secret)
	raw, err := tokens.Issue("resident")
	if err != nil {
		t.Fatal(err)
	}
	if login, err := tokens.Verify(raw); err != nil || login != "resident" {
		t.Fatalf("valid token: %q %v", login, err)
	}
	cases := []struct {
		name   string
		method jwt.SigningMethod
		claims jwt.RegisteredClaims
		key    string
	}{
		{"expired", jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: "resident", ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour))}, secret},
		{"no expiry", jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: "resident"}, secret},
		{"no subject", jwt.SigningMethodHS256, jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, secret},
		{"wrong algorithm", jwt.SigningMethodHS384, jwt.RegisteredClaims{Subject: "resident", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, secret},
		{"wrong key", jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: "resident", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, "another key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := jwt.NewWithClaims(tc.method, tc.claims).SignedString([]byte(tc.key))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tokens.Verify(raw); err == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
}
