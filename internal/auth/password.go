// Package auth provides password hashing and opaque token handling.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024 // 64 MiB, in KiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// HashPassword returns a PHC-style argon2id encoded hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against a PHC-style argon2id hash.
func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, key, err := parseEncoded(encoded)
	if err != nil {
		return false, err
	}

	computed := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(computed, key) == 1, nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parseEncoded(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// parts: ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, errors.New("invalid argon2id hash")
	}

	var p argonParams
	for _, kv := range strings.Split(parts[3], ",") {
		key, val, ok := strings.Cut(kv, "=")
		if !ok {
			return argonParams{}, nil, nil, errors.New("invalid argon2id parameters")
		}
		switch key {
		case "m":
			v, err := strconv.ParseUint(val, 10, 32)
			if err != nil {
				return argonParams{}, nil, nil, errors.New("invalid argon2id memory parameter")
			}
			p.memory = uint32(v)
		case "t":
			v, err := strconv.ParseUint(val, 10, 32)
			if err != nil {
				return argonParams{}, nil, nil, errors.New("invalid argon2id time parameter")
			}
			p.time = uint32(v)
		case "p":
			v, err := strconv.ParseUint(val, 10, 8)
			if err != nil {
				return argonParams{}, nil, nil, errors.New("invalid argon2id threads parameter")
			}
			p.threads = uint8(v)
		}
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, errors.New("invalid argon2id salt")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, errors.New("invalid argon2id hash")
	}

	return p, salt, key, nil
}
