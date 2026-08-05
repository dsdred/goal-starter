package security

import (
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const (
	// bcryptCost is the cost parameter for bcrypt.
	// 12 is a good default: fast enough for UX, secure enough for local apps.
	bcryptCost = 12
)

// PasswordHash stores password hashes for admin users.
type PasswordHash struct {
	mu        sync.RWMutex
	hashStore map[string]string // username -> bcrypt hash
}

// NewPasswordHash creates a password hash store.
func NewPasswordHash() *PasswordHash {
	return &PasswordHash{
		hashStore: make(map[string]string),
	}
}

// HashPassword generates a bcrypt hash from plaintext password.
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// CheckPasswordHash compares plaintext password against bcrypt hash.
func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// SetHash stores a bcrypt hash for a user.
func (ph *PasswordHash) SetHash(username, hash string) error {
	ph.mu.Lock()
	defer ph.mu.Unlock()
	ph.hashStore[username] = hash
	return nil
}

// GetHash retrieves the bcrypt hash for a user.
func (ph *PasswordHash) GetHash(username string) (string, bool) {
	hash, ok := ph.hashStore[username]
	return hash, ok
}

// HasUser checks if a user exists in the hash store.
func (ph *PasswordHash) HasUser(username string) bool {
	_, ok := ph.hashStore[username]
	return ok
}
