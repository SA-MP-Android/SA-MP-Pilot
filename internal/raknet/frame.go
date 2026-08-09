package raknet

import "errors"

type Reliability uint8

const (
	Unreliable Reliability = 6 + iota
	UnreliableSequenced
	Reliable
	ReliableOrdered
	ReliableSequenced
)
const (
	maxOrderingChannel      = 31
	reliabilityBitCount     = 4
	orderingChannelBitCount = 5
	maxAckRanges            = 512
	maxSplitParts           = 4096
	maxFramePayloadBits     = 1 << 20
)

var ErrInvalidFrame = errors.New("raknet: invalid reliability frame")

type Frame struct {
	MessageNumber   uint16
	Reliability     Reliability
	OrderingChannel uint8
	OrderingIndex   uint16
	Split           *Split
	Payload         []byte
	PayloadBits     int
}
type Split struct {
	ID           uint16
	Index, Count uint32
}

func (r Reliability) ordered() bool {
	return r == UnreliableSequenced || r == ReliableOrdered || r == ReliableSequenced
}
func (r Reliability) reliable() bool {
	return r == Reliable || r == ReliableOrdered || r == ReliableSequenced
}
func EncodeFrame(f Frame) ([]byte, int, error) {
	w := Writer{}
	if err := writeFrame(&w, f); err != nil {
		return nil, 0, err
	}
	return w.Bytes(), w.LenBits(), nil
}
func writeFrame(w *Writer, f Frame) error {
	if f.Reliability < Unreliable || f.Reliability > ReliableSequenced || f.OrderingChannel > maxOrderingChannel || f.PayloadBits <= 0 || f.PayloadBits > len(f.Payload)*8 || f.PayloadBits > maxFramePayloadBits {
		return ErrInvalidFrame
	}
	w.Uint16(f.MessageNumber)
	w.Bits([]byte{byte(f.Reliability)}, reliabilityBitCount, true)
	if f.Reliability.ordered() {
		w.Bits([]byte{f.OrderingChannel}, orderingChannelBitCount, true)
		w.Uint16(f.OrderingIndex)
	}
	w.Bit(f.Split != nil)
	if f.Split != nil {
		if f.Split.Count == 0 || f.Split.Count > maxSplitParts || f.Split.Index >= f.Split.Count {
			return ErrInvalidFrame
		}
		w.Uint16(f.Split.ID)
		w.CompressedUint32(f.Split.Index)
		w.CompressedUint32(f.Split.Count)
	}
	w.CompressedUint16(uint16(f.PayloadBits))
	w.Align()
	w.Bits(f.Payload, (f.PayloadBits+7)&^7, true)
	return nil
}
func DecodeFrame(r *Reader) (Frame, error) {
	var f Frame
	var e error
	if f.MessageNumber, e = r.Uint16(); e != nil {
		return f, e
	}
	v, e := r.Bits(reliabilityBitCount, true)
	if e != nil {
		return f, e
	}
	f.Reliability = Reliability(v[0])
	if f.Reliability < Unreliable || f.Reliability > ReliableSequenced {
		return f, ErrInvalidFrame
	}
	if f.Reliability.ordered() {
		v, e = r.Bits(orderingChannelBitCount, true)
		if e != nil {
			return f, e
		}
		f.OrderingChannel = v[0]
		f.OrderingIndex, e = r.Uint16()
		if e != nil {
			return f, e
		}
	}
	split, e := r.Bit()
	if e != nil {
		return f, e
	}
	if split {
		s := &Split{}
		s.ID, e = r.Uint16()
		if e != nil {
			return f, e
		}
		s.Index, e = r.CompressedUint32()
		if e != nil {
			return f, e
		}
		s.Count, e = r.CompressedUint32()
		if e != nil || s.Count == 0 || s.Count > maxSplitParts || s.Index >= s.Count {
			return f, ErrInvalidFrame
		}
		f.Split = s
	}
	length, e := r.CompressedUint16()
	if e != nil || length == 0 || uint32(length) > maxFramePayloadBits {
		return f, ErrInvalidFrame
	}
	f.PayloadBits = int(length)
	if e = r.Align(); e != nil {
		return f, e
	}
	f.Payload, e = r.Bits((f.PayloadBits+7)&^7, true)
	return f, e
}

type Range struct{ Min, Max uint16 }

func EncodeDatagram(acks []Range, frames []Frame) ([]byte, error) {
	w := Writer{}
	w.Bit(len(acks) > 0)
	if len(acks) > 0 {
		w.CompressedUint16(uint16(len(acks)))
		for _, v := range acks {
			equal := v.Min == v.Max
			if v.Max < v.Min {
				return nil, ErrInvalidFrame
			}
			w.Bit(equal)
			w.Uint16(v.Min)
			if !equal {
				w.Uint16(v.Max)
			}
		}
	}
	for _, f := range frames {
		if e := writeFrame(&w, f); e != nil {
			return nil, e
		}
	}
	return w.Bytes(), nil
}
func DecodeDatagram(data []byte) ([]Range, []Frame, error) {
	r := NewReader(data)
	has, e := r.Bit()
	if e != nil {
		return nil, nil, e
	}
	var acks []Range
	if has {
		count, e := r.CompressedUint16()
		if e != nil {
			return nil, nil, e
		}
		if count > maxAckRanges {
			return nil, nil, ErrInvalidFrame
		}
		acks = make([]Range, 0, count)
		for i := 0; i < int(count); i++ {
			equal, e := r.Bit()
			if e != nil {
				return nil, nil, e
			}
			min, e := r.Uint16()
			if e != nil {
				return nil, nil, e
			}
			max := min
			if !equal {
				max, e = r.Uint16()
				if e != nil || max < min {
					return nil, nil, ErrInvalidFrame
				}
			}
			acks = append(acks, Range{min, max})
		}
	}
	frames := []Frame{}
	for r.Remaining() >= 16 {
		f, e := DecodeFrame(r)
		if e != nil {
			return nil, nil, e
		}
		frames = append(frames, f)
	}
	return acks, frames, nil
}
