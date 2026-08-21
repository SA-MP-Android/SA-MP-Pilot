package raknet

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

const testCookie uint16 = 0x1234

func TestConnectionHandshakeAndReliableExchange(t *testing.T) {
	server, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	serverReady := make(chan error, 1)
	serverClosed := make(chan error, 1)
	go func() {
		buffer := make([]byte, maxDatagramSize)
		n, client, e := server.ReadFromUDP(buffer)
		if e != nil {
			serverReady <- e
			return
		}
		packet := decodeTestDatagram(buffer[:n], uint16(server.LocalAddr().(*net.UDPAddr).Port))
		if len(packet) != offlinePacketSize || packet[0] != packetOpenConnectionRequest {
			serverReady <- ErrHandshake
			return
		}
		cookie := []byte{packetOpenConnectionCookie, 0, 0}
		binary.LittleEndian.PutUint16(cookie[1:], testCookie)
		_, _ = server.WriteToUDP(cookie, client)
		n, client, e = server.ReadFromUDP(buffer)
		if e != nil {
			serverReady <- e
			return
		}
		packet = decodeTestDatagram(buffer[:n], uint16(server.LocalAddr().(*net.UDPAddr).Port))
		if len(packet) != offlinePacketSize || binary.LittleEndian.Uint16(packet[1:]) != testCookie^connectionCookieXOR {
			serverReady <- ErrHandshake
			return
		}
		_, _ = server.WriteToUDP([]byte{packetOpenConnectionReply, 0}, client)
		n, client, e = server.ReadFromUDP(buffer)
		if e != nil {
			serverReady <- e
			return
		}
		packet = decodeTestDatagram(buffer[:n], uint16(server.LocalAddr().(*net.UDPAddr).Port))
		_, frames, e := DecodeDatagram(packet)
		if e != nil || len(frames) != 1 || string(frames[0].Payload) != string([]byte{packetConnectionRequest})+"secret" {
			serverReady <- ErrHandshake
			return
		}
		accepted := Frame{MessageNumber: 0, Reliability: Reliable, Payload: []byte{PacketConnectionAccepted}, PayloadBits: 8}
		response, e := EncodeDatagram([]Range{{frames[0].MessageNumber, frames[0].MessageNumber}}, []Frame{accepted})
		if e == nil {
			_, e = server.WriteToUDP(response, client)
		}
		serverReady <- e
		if e != nil {
			return
		}
		_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
		const serverDrainMessage uint16 = 7
		for {
			n, _, e = server.ReadFromUDP(buffer)
			if e != nil {
				serverClosed <- e
				return
			}
			packet = decodeTestDatagram(buffer[:n], uint16(server.LocalAddr().(*net.UDPAddr).Port))
			acks, frames, decodeErr := DecodeDatagram(packet)
			if decodeErr != nil {
				continue
			}
			for _, ack := range acks {
				if ack.Min <= serverDrainMessage && serverDrainMessage <= ack.Max {
					serverClosed <- nil
					return
				}
			}
			for _, frame := range frames {
				if len(frame.Payload) == 1 && frame.Payload[0] == packetDisconnection {
					drainFrame := Frame{
						MessageNumber: serverDrainMessage,
						Reliability:   Reliable,
						Payload:       []byte{packetInternalPing, 0, 0, 0, 0},
						PayloadBits:   40,
					}
					response, encodeErr := EncodeDatagram([]Range{{frame.MessageNumber, frame.MessageNumber}}, []Frame{drainFrame})
					if encodeErr == nil {
						_, _ = server.WriteToUDP(response, client)
					}
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, e := Dial(ctx, server.LocalAddr().String(), "secret")
	if e != nil {
		t.Fatal(e)
	}
	if e = <-serverReady; e != nil {
		t.Fatal(e)
	}
	if e = conn.Close(); e != nil {
		t.Fatal(e)
	}
	if e = <-serverClosed; e != nil {
		t.Fatalf("server did not receive graceful disconnect: %v", e)
	}
}
func decodeTestDatagram(encoded []byte, port uint16) []byte {
	if len(encoded) < 2 {
		return nil
	}
	inverse := [256]byte{}
	for plain, cipher := range sampEncryptionTable {
		inverse[cipher] = byte(plain)
	}
	decoded := make([]byte, len(encoded)-1)
	portMask := byte(port) ^ portXOR
	for index, value := range encoded[1:] {
		if index%2 == 1 {
			value ^= portMask
		}
		decoded[index] = inverse[value]
	}
	return decoded
}
func TestDialCancellation(t *testing.T) {
	server, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, e = Dial(ctx, server.LocalAddr().String(), ""); e == nil {
		t.Fatal("expected timeout")
	}
}

func TestConnectedPongEchoesServerTimestamp(t *testing.T) {
	ping := []byte{packetInternalPing, 0x78, 0x56, 0x34, 0x12}
	now := time.UnixMilli(123456)
	pong, err := connectedPong(ping, now)
	if err != nil {
		t.Fatal(err)
	}
	if pong[0] != packetConnectedPong {
		t.Fatalf("packet ID = %d", pong[0])
	}
	if got := binary.LittleEndian.Uint32(pong[1:5]); got != 0x12345678 {
		t.Fatalf("echo = %#x", got)
	}
	if got := binary.LittleEndian.Uint32(pong[5:9]); got != uint32(now.UnixMilli()) {
		t.Fatalf("time = %d", got)
	}
}
func TestReceiverOrdersAndReassembles(t *testing.T) {
	state := newReceiverState()
	now := time.Now()
	second := Frame{Reliability: ReliableOrdered, OrderingIndex: 1, Payload: []byte("second")}
	if got := state.accept(second, now); len(got) != 0 {
		t.Fatalf("released out of order: %q", got)
	}
	first := Frame{Reliability: ReliableOrdered, OrderingIndex: 0, Payload: []byte("first")}
	got := state.accept(first, now)
	if len(got) != 2 || string(got[0]) != "first" || string(got[1]) != "second" {
		t.Fatalf("unexpected order: %q", got)
	}
	part1 := Frame{Reliability: Reliable, Split: &Split{ID: 4, Index: 1, Count: 2}, Payload: []byte("bar"), PayloadBits: 24}
	part0 := Frame{Reliability: Reliable, Split: &Split{ID: 4, Index: 0, Count: 2}, Payload: []byte("foo"), PayloadBits: 24}
	if got = state.accept(part1, now); got != nil {
		t.Fatalf("released partial split: %q", got)
	}
	got = state.accept(part0, now)
	if len(got) != 1 || string(got[0]) != "foobar" {
		t.Fatalf("unexpected split: %q", got)
	}
}

func TestReceiverBoundsSplitAssembliesAndBytes(t *testing.T) {
	state := newReceiverState()
	now := time.Now()
	for id := 0; id < maxSplitAssemblies+1; id++ {
		frame := Frame{
			Reliability: Reliable,
			Split:       &Split{ID: uint16(id), Index: 0, Count: 2},
			Payload:     []byte("part"),
			PayloadBits: 32,
		}
		state.accept(frame, now)
	}
	if len(state.splits) != maxSplitAssemblies {
		t.Fatalf("split assemblies = %d, want %d", len(state.splits), maxSplitAssemblies)
	}
	if state.splitBytes != maxSplitAssemblies*len("part") {
		t.Fatalf("split bytes = %d", state.splitBytes)
	}

	oversized := newReceiverState()
	frame := Frame{
		Reliability: Reliable,
		Split:       &Split{ID: 1, Index: 0, Count: 2},
		Payload:     make([]byte, maxSplitAssemblyBytes+1),
		PayloadBits: (maxSplitAssemblyBytes + 1) * 8,
	}
	oversized.accept(frame, now)
	if len(oversized.splits) != 0 || oversized.splitBytes != 0 {
		t.Fatalf("oversized split retained: assemblies=%d bytes=%d", len(oversized.splits), oversized.splitBytes)
	}
}

func TestReceiverExpirationReleasesSplitBytes(t *testing.T) {
	state := newReceiverState()
	created := time.Now()
	state.accept(Frame{Reliability: Reliable, Split: &Split{ID: 1, Index: 0, Count: 2}, Payload: []byte("part"), PayloadBits: 32}, created)
	state.expire(created.Add(splitAssemblyTTL + time.Second))
	if len(state.splits) != 0 || state.splitBytes != 0 {
		t.Fatalf("expired split retained: assemblies=%d bytes=%d", len(state.splits), state.splitBytes)
	}
}
func TestDeliverWaitsInsteadOfDroppingWhenInboundQueueIsFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Conn{ctx: ctx, recv: make(chan []byte, 1)}
	c.recv <- []byte("queued")
	delivered := make(chan bool, 1)
	go func() { delivered <- c.deliver([]byte("dialog")) }()
	select {
	case <-delivered:
		t.Fatal("deliver returned while the inbound queue was still full")
	case <-time.After(10 * time.Millisecond):
	}
	if got := string(<-c.recv); got != "queued" {
		t.Fatalf("first payload = %q", got)
	}
	if ok := <-delivered; !ok {
		t.Fatal("deliver failed after queue space became available")
	}
	if got := string(<-c.recv); got != "dialog" {
		t.Fatalf("second payload = %q", got)
	}
}
func TestSequenceComparisonWraps(t *testing.T) {
	if !sequenceNewer(0, 0xffff) || sequenceNewer(0xffff, 0) {
		t.Fatal("sequence wrap comparison is incorrect")
	}
}
