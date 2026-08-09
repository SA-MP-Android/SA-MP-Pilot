package raknet

import (
	"errors"
	"testing"
)

func TestBitStreamRoundTrip(t *testing.T) {
	w := Writer{}
	w.Bit(true)
	w.Bit(false)
	w.Uint16(0x1234)
	w.Float32(42.5)
	r := NewReaderBits(w.Bytes(), w.LenBits())
	a, e := r.Bit()
	if e != nil || !a {
		t.Fatal(a, e)
	}
	b, e := r.Bit()
	if e != nil || b {
		t.Fatal(b, e)
	}
	u, e := r.Uint16()
	if e != nil || u != 0x1234 {
		t.Fatalf("%x %v", u, e)
	}
	f, e := r.Float32()
	if e != nil || f != 42.5 {
		t.Fatal(f, e)
	}
}
func TestCompressedUint32Vectors(t *testing.T) {
	for _, v := range []uint32{0, 1, 15, 16, 255, 256, 65535, 1 << 24, 0xffffffff} {
		w := Writer{}
		w.CompressedUint32(v)
		r := NewReaderBits(w.Bytes(), w.LenBits())
		got, e := r.CompressedUint32()
		if e != nil || got != v {
			t.Fatalf("value=%d got=%d err=%v bits=%d", v, got, e, w.LenBits())
		}
	}
}
func TestReaderBounds(t *testing.T) {
	_, e := NewReader(nil).Uint8()
	if !errors.Is(e, ErrEndOfBitStream) {
		t.Fatalf("got %v", e)
	}
}
func TestRPCRoundTrip(t *testing.T) {
	payload := []byte{0xab, 0xc0}
	got, e := DecodeRPC(EncodeRPC(101, payload, 10))
	if e != nil {
		t.Fatal(e)
	}
	if got.ID != 101 || got.PayloadBits != 10 || got.Payload[0] != 0xab || got.Payload[1] != 0xc0 {
		t.Fatalf("%+v", got)
	}
}

func TestRPCUsesLegacyRakNetPacketID(t *testing.T) {
	packet := EncodeRPC(25, nil, 0)
	if len(packet) == 0 || packet[0] != PacketRPC || PacketRPC != 20 {
		t.Fatalf("RPC packet ID = %d, want 20", packet[0])
	}
}

func TestDecodeTimestampedRPC(t *testing.T) {
	packet := append([]byte{PacketTimestamp, 0x78, 0x56, 0x34, 0x12}, EncodeRPC(61, []byte{0xab}, 8)...)
	rpc, err := DecodeRPC(packet)
	if err != nil {
		t.Fatal(err)
	}
	if rpc.ID != 61 || rpc.PayloadBits != 8 || len(rpc.Payload) != 1 || rpc.Payload[0] != 0xab {
		t.Fatalf("unexpected timestamped RPC: %+v", rpc)
	}
}
