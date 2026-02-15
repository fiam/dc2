package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

const (
	// AWSLikeHexIDLength matches the modern EC2-style hexadecimal suffix width
	// used by IDs like i-*, vol-*, and lt-*.
	AWSLikeHexIDLength = 17
)

func Hex(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("invalid hex id length %d", length)
	}

	byteLen := (length + 1) / 2
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}

	return hex.EncodeToString(buf)[:length], nil
}

func WithPrefix(prefix string, length int) (string, error) {
	suffix, err := Hex(length)
	if err != nil {
		return "", err
	}
	return prefix + suffix, nil
}
