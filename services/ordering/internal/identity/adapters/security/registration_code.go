package security

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

type SixDigitCodeGenerator struct{}

func (SixDigitCodeGenerator) NewCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("generate verification code: %w", err)
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}
