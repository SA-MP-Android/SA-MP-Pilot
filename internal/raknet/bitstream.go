package raknet

import (
	"encoding/binary"
	"errors"
	"math"
)

var ErrEndOfBitStream = errors.New("raknet bitstream: insufficient data")

type Writer struct {
	data []byte
	bits int
}

func (w *Writer) LenBits() int  { return w.bits }
func (w *Writer) Bytes() []byte { return append([]byte(nil), w.data...) }
func (w *Writer) Bit(v bool) {
	i := w.bits >> 3
	if i == len(w.data) {
		w.data = append(w.data, 0)
	}
	if v {
		w.data[i] |= byte(0x80 >> uint(w.bits&7))
	}
	w.bits++
}
func (w *Writer) Align() {
	for w.bits&7 != 0 {
		w.Bit(false)
	}
}
func (w *Writer) Bits(data []byte, count int, right bool) {
	if count <= 0 {
		return
	}
	shift := 0
	if right && count&7 != 0 {
		shift = 8 - (count & 7)
	}
	for i := 0; i < count; i++ {
		s := i + shift
		w.Bit(data[s>>3]&(0x80>>uint(s&7)) != 0)
	}
}
func (w *Writer) Uint8(v uint8) { w.Bits([]byte{v}, 8, true) }
func (w *Writer) Uint16(v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.Bits(b[:], 16, true)
}
func (w *Writer) Uint32(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.Bits(b[:], 32, true)
}
func (w *Writer) Int16(v int16)     { w.Uint16(uint16(v)) }
func (w *Writer) Float32(v float32) { w.Uint32(math.Float32bits(v)) }
func (w *Writer) String8(v string)  { w.Uint8(uint8(len(v))); w.Bits([]byte(v), len(v)*8, true) }
func (w *Writer) CompressedUint32(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.compressed(b[:])
}
func (w *Writer) CompressedUint16(v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.compressed(b[:])
}
func (w *Writer) compressed(input []byte) {
	current := len(input) - 1
	for current > 0 {
		if input[current] == 0 {
			w.Bit(true)
			current--
			continue
		}
		w.Bit(false)
		w.Bits(input[:current+1], (current+1)*8, true)
		return
	}
	if input[0]&0xf0 == 0 {
		w.Bit(true)
		w.Bits(input[:1], 4, true)
	} else {
		w.Bit(false)
		w.Bits(input[:1], 8, true)
	}
}

type Reader struct {
	data         []byte
	bits, offset int
}

func NewReader(data []byte) *Reader { return &Reader{data: data, bits: len(data) * 8} }
func NewReaderBits(data []byte, bits int) *Reader {
	if bits > len(data)*8 {
		bits = len(data) * 8
	}
	return &Reader{data: data, bits: bits}
}
func (r *Reader) Remaining() int { return r.bits - r.offset }
func (r *Reader) Align() error {
	next := (r.offset + 7) &^ 7
	if next > r.bits {
		return ErrEndOfBitStream
	}
	r.offset = next
	return nil
}
func (r *Reader) Bit() (bool, error) {
	if r.offset >= r.bits {
		return false, ErrEndOfBitStream
	}
	v := r.data[r.offset>>3]&(0x80>>uint(r.offset&7)) != 0
	r.offset++
	return v, nil
}
func (r *Reader) Bits(count int, right bool) ([]byte, error) {
	if count < 0 || r.offset+count > r.bits {
		return nil, ErrEndOfBitStream
	}
	out := make([]byte, (count+7)/8)
	shift := 0
	if right && count&7 != 0 {
		shift = 8 - (count & 7)
	}
	for i := 0; i < count; i++ {
		v, _ := r.Bit()
		if v {
			t := i + shift
			out[t>>3] |= 0x80 >> uint(t&7)
		}
	}
	return out, nil
}
func (r *Reader) Uint8() (uint8, error) {
	b, e := r.Bits(8, true)
	if e != nil {
		return 0, e
	}
	return b[0], nil
}
func (r *Reader) Uint16() (uint16, error) {
	b, e := r.Bits(16, true)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint16(b), nil
}
func (r *Reader) Uint32() (uint32, error) {
	b, e := r.Bits(32, true)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint32(b), nil
}
func (r *Reader) Int16() (int16, error)     { v, e := r.Uint16(); return int16(v), e }
func (r *Reader) Float32() (float32, error) { v, e := r.Uint32(); return math.Float32frombits(v), e }
func (r *Reader) String8() (string, error) {
	n, e := r.Uint8()
	if e != nil {
		return "", e
	}
	b, e := r.Bits(int(n)*8, true)
	return string(b), e
}
func (r *Reader) CompressedUint32() (uint32, error) {
	b, e := r.compressed(4)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint32(b), nil
}
func (r *Reader) CompressedUint16() (uint16, error) {
	b, e := r.compressed(2)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint16(b), nil
}
func (r *Reader) compressed(size int) ([]byte, error) {
	out := make([]byte, size)
	current := size - 1
	for current > 0 {
		match, e := r.Bit()
		if e != nil {
			return nil, e
		}
		if match {
			current--
			continue
		}
		b, e := r.Bits((current+1)*8, true)
		copy(out, b)
		return out, e
	}
	half, e := r.Bit()
	if e != nil {
		return nil, e
	}
	count := 8
	if half {
		count = 4
	}
	b, e := r.Bits(count, true)
	if e != nil {
		return nil, e
	}
	out[0] = b[0]
	return out, nil
}
