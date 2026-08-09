package samp

import (
	"encoding/hex"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
	"testing"
)

func TestDecodeHuffmanStringCXXFixture(t *testing.T) {
	data, e := hex.DecodeString("8630252840")
	if e != nil {
		t.Fatal(e)
	}
	got, e := decodeHuffmanString(raknet.NewReaderBits(data, 34), 256)
	if e != nil {
		t.Fatal(e)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}
