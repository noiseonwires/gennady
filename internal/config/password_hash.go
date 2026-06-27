// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	WebUIPasswordHashPrefix     = "hashed:pbkdf2-sha256:"
	webUIPasswordHashIterations = 210000
	webUIPasswordSaltBytes      = 16
	webUIPasswordKeyBytes       = 32
)

func IsHashedWebUIPassword(password string) bool {
	return strings.HasPrefix(password, WebUIPasswordHashPrefix)
}

func HashWebUIPasswordForStorage(password string) (string, error) {
	if password == "" || IsHashedWebUIPassword(password) {
		return password, nil
	}

	salt := make([]byte, webUIPasswordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	key, err := pbkdf2.Key(sha256.New, password, salt, webUIPasswordHashIterations, webUIPasswordKeyBytes)
	if err != nil {
		return "", fmt.Errorf("derive password key: %w", err)
	}
	return fmt.Sprintf("%s%d:%s:%s",
		WebUIPasswordHashPrefix,
		webUIPasswordHashIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyWebUIPassword(password, expected string) bool {
	if !IsHashedWebUIPassword(expected) {
		return subtle.ConstantTimeCompare([]byte(password), []byte(expected)) == 1
	}

	parts := strings.Split(expected, ":")
	if len(parts) != 5 || parts[0] != "hashed" || parts[1] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[2])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) == 0 {
		return false
	}
	storedKey, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(storedKey) == 0 {
		return false
	}

	actualKey, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(storedKey))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(actualKey, storedKey) == 1
}

func HashWebUIPasswordInConfigValues(values map[string]string) (bool, error) {
	password := values["web_ui.password"]
	if password == "" || IsHashedWebUIPassword(password) {
		return false, nil
	}
	hashed, err := HashWebUIPasswordForStorage(password)
	if err != nil {
		return false, err
	}
	values["web_ui.password"] = hashed
	return true, nil
}
