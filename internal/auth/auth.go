package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	saltLen    = 32
	iterations = 100_000
	keyLen     = 32
)

// HashPassword returns "salt:hash" using PBKDF2-HMAC-SHA256.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	dk := pbkdf2([]byte(password), salt, iterations, keyLen)
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(dk), nil
}

// CheckPassword verifies a password against a "salt:hash" string.
func CheckPassword(password, stored string) bool {
	// split "salt:hash"
	var saltHex, hashHex string
	for i := range stored {
		if stored[i] == ':' {
			saltHex = stored[:i]
			hashHex = stored[i+1:]
			break
		}
	}
	if saltHex == "" || hashHex == "" {
		return false
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	dk := pbkdf2([]byte(password), salt, iterations, keyLen)
	return hmac.Equal(dk, expected)
}

// GenerateToken creates a cryptographically random session token.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// pbkdf2 implements PBKDF2 with HMAC-SHA256 (RFC 2898).
func pbkdf2(password, salt []byte, iter, keyLen int) []byte {
	numBlocks := (keyLen + sha256.Size - 1) / sha256.Size
	dk := make([]byte, 0, numBlocks*sha256.Size)

	for block := 1; block <= numBlocks; block++ {
		dk = append(dk, pbkdf2Block(password, salt, iter, block)...)
	}
	return dk[:keyLen]
}

func pbkdf2Block(password, salt []byte, iter, blockNum int) []byte {
	// U1 = PRF(password, salt || INT_32_BE(blockNum))
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write([]byte{byte(blockNum >> 24), byte(blockNum >> 16), byte(blockNum >> 8), byte(blockNum)})
	u := mac.Sum(nil)

	result := make([]byte, len(u))
	copy(result, u)

	for i := 1; i < iter; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(nil)
		for j := range result {
			result[j] ^= u[j]
		}
	}
	return result
}
