package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Tokens struct{ key []byte }

func NewTokens(secret string) *Tokens { return &Tokens{key: []byte(secret)} }
func (t *Tokens) Issue(login string) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   login,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(48 * time.Hour)),
	}).SignedString(t.key)
}
func (t *Tokens) Verify(raw string) (string, error) {
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) { return t.key, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !token.Valid || claims.Subject == "" {
		return "", errors.New("invalid access token")
	}
	return claims.Subject, nil
}
