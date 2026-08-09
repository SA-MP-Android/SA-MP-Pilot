package samp

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestQueryParsesInfo(t *testing.T) {
	pc, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	go func() {
		b := make([]byte, 64)
		n, a, _ := pc.ReadFrom(b)
		r := append([]byte{}, b[:11]...)
		r = append(r, 0)
		r = append16(r, 12)
		r = append16(r, 100)
		r = appendStr(r, "Test Server")
		r = appendStr(r, "Freeroam")
		r = appendStr(r, "English")
		_, _ = pc.WriteTo(r[:n-n+len(r)], a)
	}()
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	got, e := Query(ctx, "127.0.0.1", port)
	if e != nil {
		t.Fatal(e)
	}
	if got.Hostname != "Test Server" || got.Players != 12 || got.MaxPlayers != 100 {
		t.Fatalf("unexpected: %+v", got)
	}
}
func append16(b []byte, n int) []byte {
	var x [2]byte
	binary.LittleEndian.PutUint16(x[:], uint16(n))
	return append(b, x[:]...)
}
func appendStr(b []byte, s string) []byte {
	var x [4]byte
	binary.LittleEndian.PutUint32(x[:], uint32(len(s)))
	return append(append(b, x[:]...), s...)
}
func TestReaderRejectsTruncatedString(t *testing.T) {
	r := reader{b: []byte{9, 0, 0, 0, 'a'}}
	var s string
	if r.str(&s) {
		t.Fatal("accepted truncated string")
	}
}
