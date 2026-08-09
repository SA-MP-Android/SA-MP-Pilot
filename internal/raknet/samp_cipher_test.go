package raknet

import (
	"bytes"
	"testing"
)

func TestEncodeSAMPDatagram(t *testing.T) {
	plain := []byte{packetOpenConnectionRequest, 0, 0}
	want := []byte{0x08, 0x1e, 0x8a, 0x27}
	if got := encodeSAMPDatagram(plain, 7777); !bytes.Equal(got, want) {
		t.Fatalf("encoded datagram = %x, want %x", got, want)
	}
}
