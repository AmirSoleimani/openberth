package install

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"

	"golang.org/x/crypto/bcrypt"
)

// randomHex returns a cryptographically random hex string of length 2n.
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// generateKey produces a new admin API key (sc_ prefix + 48 hex chars).
func generateKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return "sc_" + hex.EncodeToString(b)
}

// generatePassword returns a 16-character alphanumeric password.
func generatePassword() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 16)
	for i := range result {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		result[i] = chars[n.Int64()]
	}
	return string(result)
}

// hashPassword bcrypt-hashes a password for storage.
func hashPassword(password string) string {
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash)
}
