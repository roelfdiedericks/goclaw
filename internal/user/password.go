// Package user provides user identity, roles, and permission management.
package user

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP recommendations)
const (
	argon2Time    = 3      // iterations
	argon2Memory  = 64 * 1024 // 64MB
	argon2Threads = 4
	argon2KeyLen  = 32
	argon2SaltLen = 16
)

// HashPassword creates an Argon2id hash of the password
func HashPassword(password string) (string, error) {
	// Generate random salt
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	// Generate hash
	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	// Encode as $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argon2Memory,
		argon2Time,
		argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)

	return encoded, nil
}

// VerifyPassword checks if password matches the encoded hash
func VerifyPassword(password, encoded string) bool {
	// Parse encoded string
	params, salt, hash, err := parseArgon2Hash(encoded)
	if err != nil {
		return false
	}

	// Recompute hash with same parameters
	computed := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, params.keyLen)

	// Constant-time comparison
	return subtle.ConstantTimeCompare(hash, computed) == 1
}

type argon2Params struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
}

// parseArgon2Hash parses an Argon2id encoded hash string
func parseArgon2Hash(encoded string) (*argon2Params, []byte, []byte, error) {
	// Format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return nil, nil, nil, fmt.Errorf("invalid hash format: expected 6 parts, got %d", len(parts))
	}

	if parts[1] != "argon2id" {
		return nil, nil, nil, fmt.Errorf("unsupported algorithm: %s", parts[1])
	}

	// Parse version (v=19)
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid version: %s", parts[2])
	}
	if version != argon2.Version {
		return nil, nil, nil, fmt.Errorf("unsupported version: %d", version)
	}

	// Parse parameters (m=65536,t=3,p=4)
	params := &argon2Params{}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.time, &params.threads); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid parameters: %s", parts[3])
	}

	// Decode salt
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid salt encoding: %w", err)
	}

	// Decode hash
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid hash encoding: %w", err)
	}

	params.keyLen = uint32(len(hash))

	return params, salt, hash, nil
}
