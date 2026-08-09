package raknet

import "errors"

const (
	PacketRPC       uint8 = 20
	PacketTimestamp uint8 = 40
	timestampBytes        = 4
)

var ErrNotRPC = errors.New("raknet: packet is not an RPC")

type RPC struct {
	ID          uint8
	Payload     []byte
	PayloadBits int
}

func EncodeRPC(id uint8, payload []byte, payloadBits int) []byte {
	w := Writer{}
	w.Uint8(PacketRPC)
	w.Uint8(id)
	w.CompressedUint32(uint32(payloadBits))
	w.Bits(payload, payloadBits, false)
	return w.Bytes()
}
func DecodeRPC(packet []byte) (RPC, error) {
	if len(packet) > 1+timestampBytes && packet[0] == PacketTimestamp {
		packet = packet[1+timestampBytes:]
	}
	r := NewReader(packet)
	kind, e := r.Uint8()
	if e != nil {
		return RPC{}, e
	}
	if kind != PacketRPC {
		return RPC{}, ErrNotRPC
	}
	id, e := r.Uint8()
	if e != nil {
		return RPC{}, e
	}
	bits, e := r.CompressedUint32()
	if e != nil {
		return RPC{}, e
	}
	payload, e := r.Bits(int(bits), false)
	if e != nil {
		return RPC{}, e
	}
	return RPC{ID: id, Payload: payload, PayloadBits: int(bits)}, nil
}
