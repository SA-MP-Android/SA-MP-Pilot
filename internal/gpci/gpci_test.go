package gpci

import (
	"math/big"
	"regexp"
	"strings"
	"testing"
)

func TestGenerateMatchesSAMPFormat(t *testing.T) {
	value, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if value == "" || len(value) > 43 {
		t.Fatalf("unexpected GPCI length: %q", value)
	}
	if !regexp.MustCompile(`^[0-9A-F]+$`).MatchString(value) {
		t.Fatalf("GPCI is not uppercase hexadecimal: %q", value)
	}
}

func TestEncodeMatchesAndroidAlgorithm(t *testing.T) {
	source := []byte("0123456789ABCDEFGHIJKLMNOP")
	wantValue := new(big.Int).SetBytes(source)
	wantValue.Mul(wantValue, big.NewInt(1001))
	want := strings.ToUpper(wantValue.Text(16))
	if got := encode(source); got != want {
		t.Fatalf("encode() = %q, want %q", got, want)
	}
}
