package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const encryptedPrefix = "enc:v1:"

func DecodeEncryptionKey(value string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("TARGET_API_KEY_ENCRYPTION_KEY must be base64-encoded 32 bytes")
	}
	return key, nil
}

func protectSnapshot(data, key []byte) ([]byte, error) {
	return transformSnapshot(data, key, encryptAPIKey)
}
func unprotectSnapshot(data, key []byte) ([]byte, error) {
	return transformSnapshot(data, key, decryptAPIKey)
}

func transformSnapshot(data, key []byte, transform func(string, []byte) (string, error)) ([]byte, error) {
	var state map[string]json.RawMessage
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	var targets map[string]map[string]json.RawMessage
	if raw, ok := state["targets"]; ok {
		if err := json.Unmarshal(raw, &targets); err != nil {
			return nil, err
		}
		for id, target := range targets {
			var apiKey string
			if rawKey, ok := target["api_key"]; ok && json.Unmarshal(rawKey, &apiKey) == nil && apiKey != "" {
				value, err := transform(apiKey, key)
				if err != nil {
					return nil, err
				}
				target["api_key"], _ = json.Marshal(value)
				targets[id] = target
			}
		}
		state["targets"], _ = json.Marshal(targets)
	}
	return json.Marshal(state)
}

func encryptAPIKey(value string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return encryptedPrefix + base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(value), nil)), nil
}

func decryptAPIKey(value string, key []byte) (string, error) {
	if !strings.HasPrefix(value, encryptedPrefix) {
		return value, nil
	}
	encoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encryptedPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(encoded) < gcm.NonceSize() {
		return "", fmt.Errorf("encrypted API key is too short")
	}
	plain, err := gcm.Open(nil, encoded[:gcm.NonceSize()], encoded[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
