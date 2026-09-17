package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

const argonVersion = 19

type Argon2id struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
	saltBytes   uint32
	keyBytes    uint32
}

func NewArgon2id(memoryKiB, iterations uint32, parallelism uint8) *Argon2id {
	return &Argon2id{
		memoryKiB: memoryKiB, iterations: iterations, parallelism: parallelism,
		saltBytes: 16, keyBytes: 32,
	}
}

func (hasher *Argon2id) Hash(password string) (string, error) {
	if err := domain.ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, hasher.saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password), salt, hasher.iterations, hasher.memoryKiB,
		hasher.parallelism, hasher.keyBytes,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argonVersion,
		hasher.memoryKiB,
		hasher.iterations,
		hasher.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func (hasher *Argon2id) Verify(password, encoded string) (bool, bool, error) {
	parameters, salt, expected, err := parsePHC(encoded)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey(
		[]byte(password), salt, parameters.iterations, parameters.memoryKiB,
		parameters.parallelism, uint32(len(expected)),
	)
	matches := subtle.ConstantTimeCompare(actual, expected) == 1
	needsRehash := parameters.memoryKiB != hasher.memoryKiB ||
		parameters.iterations != hasher.iterations ||
		parameters.parallelism != hasher.parallelism ||
		uint32(len(expected)) != hasher.keyBytes
	return matches, needsRehash, nil
}

type phcParameters struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
}

func parsePHC(encoded string) (phcParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return phcParameters{}, nil, nil, errors.New("invalid Argon2id PHC string")
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return phcParameters{}, nil, nil, errors.New("invalid Argon2id parameters")
	}
	if memory < 8 || iterations < 1 || parallelism < 1 {
		return phcParameters{}, nil, nil, errors.New("unsafe Argon2id parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 {
		return phcParameters{}, nil, nil, errors.New("invalid Argon2id salt")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 {
		return phcParameters{}, nil, nil, errors.New("invalid Argon2id key")
	}
	return phcParameters{memoryKiB: memory, iterations: iterations, parallelism: parallelism}, salt, key, nil
}
