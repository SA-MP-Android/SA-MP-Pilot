package gpci

import (
	cryptorand "crypto/rand"
	"math/big"
	"strings"
)

const (
	sourceLength = 20
	alphanumeric = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

var alphabetSize = big.NewInt(int64(len(alphanumeric)))

// Generate returns a SA-MP-compatible GPCI value. The source is a random
// 20-character alphanumeric value, multiplied by 1001 and encoded as
// uppercase hexadecimal, matching the Android client implementation.
func Generate() (string, error) {
	source := make([]byte, sourceLength)
	for index := range source {
		digit, err := cryptorand.Int(cryptorand.Reader, alphabetSize)
		if err != nil {
			return "", err
		}
		source[index] = alphanumeric[digit.Int64()]
	}
	return encode(source), nil
}

func encode(source []byte) string {
	value := new(big.Int).SetBytes(source)
	value.Mul(value, big.NewInt(1001))
	return strings.ToUpper(value.Text(16))
}
