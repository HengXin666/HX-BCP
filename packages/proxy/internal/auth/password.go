package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters follow the OWASP password storage recommendation for
// memory-constrained servers: 19 MiB memory, 2 iterations, 1 lane.
const (
	argonMemoryKiB  = 19 * 1024
	argonIterations = 2
	argonLanes      = 1
	argonSaltBytes  = 16
	argonKeyBytes   = 32
)

var errMalformedHash = errors.New("malformed password hash")

// HashPassword derives an Argon2id hash in the standard PHC string format:
// $argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonLanes, argonKeyBytes)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemoryKiB,
		argonIterations,
		argonLanes,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against a stored PHC hash in constant
// time. The stored parameters are honored so parameter upgrades keep old
// hashes verifiable.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errMalformedHash
	}
	var memoryKiB, iterations uint32
	var lanes uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memoryKiB, &iterations, &lanes); err != nil {
		return false, errMalformedHash
	}
	if memoryKiB == 0 || memoryKiB > 1<<20 || iterations == 0 || iterations > 16 || lanes == 0 {
		return false, errMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errMalformedHash
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) == 0 {
		return false, errMalformedHash
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memoryKiB, lanes, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
