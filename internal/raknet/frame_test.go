package raknet

import (
	"bytes"
	"testing"
)

func TestConnectionRequestFrameMatchesRakNetReference(t *testing.T) {
	frame := Frame{MessageNumber: 0, Reliability: Reliable, Payload: []byte{packetConnectionRequest}, PayloadBits: 8}
	got, err := EncodeDatagram(nil, []Frame{frame})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0x00, 0x43, 0x80, 0x0b}
	if !bytes.Equal(got, want) {
		t.Fatalf("datagram = %x, want C++ RakNet fixture %x", got, want)
	}
}

func TestAuthFrameHeaderMatchesRakNetReference(t *testing.T) {
	payload := make([]byte, 42)
	payload[0], payload[1] = 12, 40
	frame := Frame{MessageNumber: 1, Reliability: Reliable, Payload: payload, PayloadBits: len(payload) * 8}
	got, err := EncodeDatagram(nil, []Frame{frame})
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := []byte{0x00, 0x80, 0x40, 0xa0, 0x02, 0x0c, 0x28}
	if len(got) < len(wantHeader) || !bytes.Equal(got[:len(wantHeader)], wantHeader) {
		t.Fatalf("datagram header = %x, want C++ RakNet fixture %x", got, wantHeader)
	}
}

func TestDatagramRoundTrip(t *testing.T) {
	input := Frame{MessageNumber: 42, Reliability: ReliableOrdered, OrderingChannel: 3, OrderingIndex: 9, Payload: []byte{1, 2, 3}, PayloadBits: 24}
	data, e := EncodeDatagram([]Range{{1, 3}, {8, 8}}, []Frame{input})
	if e != nil {
		t.Fatal(e)
	}
	acks, frames, e := DecodeDatagram(data)
	if e != nil {
		t.Fatal(e)
	}
	if len(acks) != 2 || len(frames) != 1 || frames[0].MessageNumber != 42 || !bytes.Equal(frames[0].Payload, input.Payload) {
		t.Fatalf("acks=%+v frames=%+v", acks, frames)
	}
}
func TestSplitFrameRoundTrip(t *testing.T) {
	input := Frame{MessageNumber: 7, Reliability: Reliable, Split: &Split{ID: 2, Index: 1, Count: 3}, Payload: []byte("part"), PayloadBits: 32}
	data, _, e := EncodeFrame(input)
	if e != nil {
		t.Fatal(e)
	}
	got, e := DecodeFrame(NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	if got.Split == nil || got.Split.Index != 1 || string(got.Payload) != "part" {
		t.Fatalf("%+v", got)
	}
}
func TestFrameValidation(t *testing.T) {
	_, _, e := EncodeFrame(Frame{Reliability: Reliable, Payload: []byte{1}, PayloadBits: 0})
	if e == nil {
		t.Fatal("expected validation error")
	}
}
