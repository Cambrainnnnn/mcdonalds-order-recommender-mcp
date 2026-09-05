package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

type RandomGenerator struct{}

func NewRandomGenerator() RandomGenerator {
	return RandomGenerator{}
}

func (RandomGenerator) NewID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
