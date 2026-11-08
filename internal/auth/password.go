package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const hashPrefix = "$argon2id$v=19$m=65536,t=1,p=4$"

func HashPassword(password string) (string, error) {
	if len(password) == 0 || len(password) > 1024 {
		return "", errors.New("password must contain 1 to 1024 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return hashPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}

func VerifyPassword(password, stored string, allowLegacy bool) (valid, legacy bool) {
	if len(password) == 0 || len(password) > 1024 {
		return false, false
	}
	if !strings.HasPrefix(stored, hashPrefix) {
		if !allowLegacy || strings.HasPrefix(stored, "$argon2") {
			return false, false
		}
		a, b := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(stored))
		ok := subtle.ConstantTimeCompare(a[:], b[:]) == 1
		return ok, ok
	}
	parts := strings.Split(strings.TrimPrefix(stored, hashPrefix), "$")
	if len(parts) != 2 {
		return false, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil || len(salt) != 16 {
		return false, false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(expected) != 32 {
		return false, false
	}
	actual := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1, false
}

func RandomString(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
