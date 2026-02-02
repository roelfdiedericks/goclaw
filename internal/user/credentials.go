package user

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrNoCredentials      = errors.New("no credentials provided")
)

// VerifyCredential finds a user whose credential matches the provided secret
// Returns the user if found, or an error if no match
func (r *Registry) VerifyCredential(method, secret string) (*User, error) {
	if secret == "" {
		return nil, ErrNoCredentials
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, user := range r.users {
		for _, cred := range user.Credentials {
			if cred.Type == method && verifyHash(cred.Hash, secret) {
				return user, nil
			}
		}
	}

	return nil, ErrInvalidCredentials
}

// verifyHash checks if a secret matches a stored hash
// Supports bcrypt format (starts with $2a$, $2b$, or $2y$)
// TODO: Add argon2 support
func verifyHash(hash, secret string) bool {
	if len(hash) == 0 {
		return false
	}

	// Check for bcrypt format
	if hash[0] == '$' && len(hash) > 4 {
		prefix := hash[:4]
		if prefix == "$2a$" || prefix == "$2b$" || prefix == "$2y$" {
			err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret))
			return err == nil
		}
	}

	// TODO: Add argon2id support ($argon2id$ prefix)
	// For now, just do a direct comparison (NOT SECURE - only for development)
	// In production, all credentials should be properly hashed
	return hash == secret
}

// HashPassword creates a bcrypt hash of a password
// Useful for generating hashes for config files
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}
