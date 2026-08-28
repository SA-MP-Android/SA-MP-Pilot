package raknet

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	packetConnectionRequest       uint8  = 11
	packetInternalPing            uint8  = 6
	packetConnectedPong           uint8  = 9
	packetOpenConnectionRequest   uint8  = 24
	packetOpenConnectionReply     uint8  = 25
	packetOpenConnectionCookie    uint8  = 26
	packetConnectionAttemptFailed uint8  = 29
	packetNoFreeConnections       uint8  = 31
	packetDisconnection           uint8  = 32
	packetConnectionLost          uint8  = 33
	PacketConnectionAccepted      uint8  = 34
	packetFailedEncryption        uint8  = 35
	packetConnectionBanned        uint8  = 36
	packetInvalidPassword         uint8  = 37
	packetNewIncomingConnection   uint8  = 30
	connectionCookieXOR           uint16 = 0x6969
	offlinePacketSize                    = 3
	connectedPingSize                    = 5
	offlineRetryInterval                 = 500 * time.Millisecond
	defaultDialTimeout                   = 10 * time.Second
	readPollInterval                     = 100 * time.Millisecond
	resendInterval                       = 400 * time.Millisecond
	// RakNet's normal client Disconnect(500) gives the server time to receive
	// the ID_DISCONNECTION_NOTIFICATION before the UDP socket is closed.
	disconnectWait        = 500 * time.Millisecond
	maxDatagramSize       = 64 * 1024
	outboundQueueSize     = 128
	inboundQueueSize      = 256
	maxPendingReliable    = 1024
	maxReceivedWindow     = 4096
	maxOrderedFrames      = 1024
	maxSplitAssemblies    = 64
	maxSplitAssemblyBytes = 1 << 20
	maxSplitBufferedBytes = 4 << 20
	maxResendAttempts     = 20
	splitAssemblyTTL      = 10 * time.Second
)

var (
	ErrClosed          = errors.New("raknet: connection closed")
	ErrQueueFull       = errors.New("raknet: queue is full")
	ErrHandshake       = errors.New("raknet: handshake failed")
	ErrAttemptFailed   = errors.New("raknet: connection attempt failed")
	ErrServerFull      = errors.New("raknet: server is full")
	ErrServerClosed    = errors.New("raknet: server closed the connection")
	ErrConnectionLost  = errors.New("raknet: connection lost")
	ErrEncryption      = errors.New("raknet: failed to initialize encryption")
	ErrBanned          = errors.New("raknet: connection banned")
	ErrInvalidPassword = errors.New("raknet: invalid password")
)

type outbound struct {
	payload     []byte
	reliability Reliability
	channel     uint8
	result      chan error
}
type pendingFrame struct {
	frame    Frame
	sent     time.Time
	attempts int
}
type splitAssembly struct {
	parts   map[uint32]Frame
	count   uint32
	created time.Time
	bytes   int
}
type receiverState struct {
	splits       map[uint16]*splitAssembly
	ordered      [maxOrderingChannel + 1]map[uint16]Frame
	expected     [maxOrderingChannel + 1]uint16
	sequenced    [maxOrderingChannel + 1]uint16
	sequencedSet [maxOrderingChannel + 1]bool
	orderedCount int
	splitBytes   int
}

func newReceiverState() *receiverState { return &receiverState{splits: map[uint16]*splitAssembly{}} }

type Conn struct {
	udp        *net.UDPConn
	ctx        context.Context
	cancel     context.CancelFunc
	disconnect chan struct{}
	send       chan outbound
	recv       chan []byte
	ready      chan error
	done       chan struct{}
	accepted   []byte
	remotePort uint16
	remoteIPv4 [4]byte
	// RakNet maintains independent ordering-index streams for
	// RELIABLE_ORDERED and the sequenced reliabilities.  They share the same
	// ordering channel number on the wire, but a sequenced packet must not
	// advance the RELIABLE_ORDERED stream (or the peer will wait forever for
	// ordered index 0).
	nextOrdered   [maxOrderingChannel + 1]uint16
	nextSequenced [maxOrderingChannel + 1]uint16
	closeOnce     sync.Once
	terminalMu    sync.RWMutex
	terminalErr   error
}

func Dial(ctx context.Context, address, password string) (*Conn, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultDialTimeout)
		defer cancel()
	}
	remote, e := net.ResolveUDPAddr("udp", address)
	if e != nil {
		return nil, e
	}
	udp, e := net.DialUDP("udp", nil, remote)
	if e != nil {
		return nil, e
	}
	if e = offlineHandshake(ctx, udp, uint16(remote.Port)); e != nil {
		udp.Close()
		return nil, e
	}
	runCtx, cancel := context.WithCancel(context.Background())
	c := &Conn{udp: udp, ctx: runCtx, cancel: cancel, disconnect: make(chan struct{}), send: make(chan outbound, outboundQueueSize), recv: make(chan []byte, inboundQueueSize), ready: make(chan error, 1), done: make(chan struct{}), remotePort: uint16(remote.Port)}
	copy(c.remoteIPv4[:], remote.IP.To4())
	go c.run(password)
	return c, nil
}
func offlineHandshake(ctx context.Context, udp *net.UDPConn, port uint16) error {
	request := []byte{packetOpenConnectionRequest, 0, 0}
	buffer := make([]byte, offlinePacketSize)
	ticker := time.NewTicker(offlineRetryInterval)
	defer ticker.Stop()
	for {
		_ = udp.SetReadDeadline(time.Now().Add(offlineRetryInterval))
		if _, e := udp.Write(encodeSAMPDatagram(request, port)); e != nil {
			return e
		}
		n, e := udp.Read(buffer)
		if e == nil && n >= 2 {
			switch buffer[0] {
			case packetOpenConnectionReply:
				return nil
			case packetOpenConnectionCookie:
				if n != offlinePacketSize {
					return ErrHandshake
				}
				cookie := binary.LittleEndian.Uint16(buffer[1:]) ^ connectionCookieXOR
				binary.LittleEndian.PutUint16(request[1:], cookie)
				continue
			case packetConnectionAttemptFailed:
				return ErrAttemptFailed
			case packetNoFreeConnections:
				return ErrServerFull
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (c *Conn) Write(ctx context.Context, payload []byte, reliability Reliability) error {
	return c.WriteChannel(ctx, payload, reliability, 0)
}
func (c *Conn) WriteChannel(ctx context.Context, payload []byte, reliability Reliability, channel uint8) error {
	if len(payload) == 0 {
		return ErrInvalidFrame
	}
	if channel > maxOrderingChannel {
		return ErrInvalidFrame
	}
	r := make(chan error, 1)
	request := outbound{payload: append([]byte(nil), payload...), reliability: reliability, channel: channel, result: r}
	select {
	case c.send <- request:
	case <-c.disconnect:
		return ErrClosed
	case <-c.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case e := <-r:
		return e
	case <-c.disconnect:
		return ErrClosed
	case <-c.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *Conn) Read(ctx context.Context) ([]byte, error) {
	select {
	case p, ok := <-c.recv:
		if !ok {
			return nil, c.closeReason()
		}
		return p, nil
	case <-c.done:
		return nil, c.closeReason()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close mirrors RakPeer::Disconnect(500) from the native SA-MP client. The
// network loop owns the UDP socket and must send the reliable ordered
// ID_DISCONNECTION_NOTIFICATION before it exits; closing the socket first is
// indistinguishable from a timeout to the server.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		close(c.disconnect)
		<-c.done
		c.cancel()
	})
	return nil
}
func (c *Conn) run(password string) {
	defer close(c.done)
	defer close(c.recv)
	defer c.udp.Close()
	pending := map[uint16]*pendingFrame{}
	received := map[uint16]struct{}{}
	receiver := newReceiverState()
	var nextMessage uint16
	readySent := false
	gracefulDisconnect := func() {
		if !readySent {
			return
		}
		c.drainGracefulDisconnect(&nextMessage, pending, received, receiver)
	}
	connectionPayload := append([]byte{packetConnectionRequest}, []byte(password)...)
	_ = c.queueFrame(connectionPayload, Reliable, 0, &nextMessage, pending)
	ticker := time.NewTicker(readPollInterval)
	defer ticker.Stop()
	buffer := make([]byte, maxDatagramSize)
	for {
		_ = c.udp.SetReadDeadline(time.Now().Add(readPollInterval))
		n, e := c.udp.Read(buffer)
		if e == nil && n > 0 {
			acks, frames, decodeErr := DecodeDatagram(buffer[:n])
			if decodeErr != nil && !readySent {
				c.ready <- fmt.Errorf("%w: invalid connected response: %v", ErrHandshake, decodeErr)
				return
			}
			if decodeErr == nil {
				for _, a := range acks {
					for id := a.Min; ; id++ {
						delete(pending, id)
						if id == a.Max {
							break
						}
					}
				}
				for _, f := range frames {
					if f.Reliability.reliable() {
						ack, _ := EncodeDatagram([]Range{{f.MessageNumber, f.MessageNumber}}, nil)
						_, _ = c.writeDatagram(ack)
					}
					if _, duplicate := received[f.MessageNumber]; duplicate {
						continue
					}
					received[f.MessageNumber] = struct{}{}
					if len(received) > maxReceivedWindow {
						received = map[uint16]struct{}{f.MessageNumber: {}}
					}
					for _, payload := range receiver.accept(f, time.Now()) {
						if len(payload) > 0 {
							switch payload[0] {
							case PacketConnectionAccepted:
								if !readySent {
									c.accepted = append([]byte(nil), payload...)
									confirmation := make([]byte, 1+len(c.remoteIPv4)+2)
									confirmation[0] = packetNewIncomingConnection
									copy(confirmation[1:], c.remoteIPv4[:])
									binary.LittleEndian.PutUint16(confirmation[1+len(c.remoteIPv4):], c.remotePort)
									if confirmErr := c.queueFrame(confirmation, Reliable, 0, &nextMessage, pending); confirmErr != nil {
										c.ready <- confirmErr
										return
									}
									c.ready <- nil
									readySent = true
								}
								if !c.deliver(payload) {
									if c.ctx.Err() != nil || c.disconnectRequested() {
										gracefulDisconnect()
									}
									return
								}
							case packetDisconnection:
								if !readySent {
									c.ready <- ErrServerClosed
								}
								c.setTerminalError(ErrServerClosed)
								return
							case packetConnectionLost:
								if !readySent {
									c.ready <- ErrConnectionLost
								}
								c.setTerminalError(ErrConnectionLost)
								return
							case packetFailedEncryption, packetConnectionBanned, packetInvalidPassword:
								reason := connectionRejection(payload[0])
								if !readySent {
									c.ready <- reason
								}
								c.setTerminalError(reason)
								return
							case packetInternalPing:
								pong, pongErr := connectedPong(payload, time.Now())
								if pongErr == nil {
									_ = c.queueFrame(pong, Unreliable, 0, &nextMessage, pending)
								}
							case packetConnectedPong:
								// RakNet transport control packet; do not expose it as application data.
							default:
								if !c.deliver(payload) {
									if c.ctx.Err() != nil || c.disconnectRequested() {
										gracefulDisconnect()
									}
									return
								}
							}
						}
					}
				}
			}
		}
		select {
		case <-c.disconnect:
			gracefulDisconnect()
			return
		case <-c.ctx.Done():
			gracefulDisconnect()
			return
		case req := <-c.send:
			if len(pending) >= maxPendingReliable && req.reliability.reliable() {
				req.result <- ErrQueueFull
				break
			}
			req.result <- c.queueFrame(req.payload, req.reliability, req.channel, &nextMessage, pending)
		case now := <-ticker.C:
			receiver.expire(now)
			for _, p := range pending {
				if now.Sub(p.sent) >= resendInterval {
					if p.attempts >= maxResendAttempts {
						if !readySent {
							c.ready <- fmt.Errorf("%w: reliable packet retry limit", ErrHandshake)
						}
						return
					}
					data, err := EncodeDatagram(nil, []Frame{p.frame})
					if err == nil {
						_, err = c.writeDatagram(data)
					}
					if err != nil {
						if !readySent {
							c.ready <- err
						}
						return
					}
					p.sent = now
					p.attempts++
				}
			}
		default:
		}
	}
}

func connectedPong(ping []byte, now time.Time) ([]byte, error) {
	if len(ping) != connectedPingSize || ping[0] != packetInternalPing {
		return nil, ErrInvalidFrame
	}
	pong := make([]byte, 1+2*(connectedPingSize-1))
	pong[0] = packetConnectedPong
	copy(pong[1:connectedPingSize], ping[1:])
	binary.LittleEndian.PutUint32(pong[connectedPingSize:], uint32(now.UnixMilli()))
	return pong, nil
}

// drainGracefulDisconnect mirrors RakPeer::Disconnect(500). The disconnect
// notification is a reliable ordered RakNet message, but sending it once and
// sleeping is not enough: the server may still have reliable messages waiting
// for an ACK. The normal client keeps its network loop alive during the grace
// period, so do the same here.
func (c *Conn) drainGracefulDisconnect(next *uint16, pending map[uint16]*pendingFrame, received map[uint16]struct{}, receiver *receiverState) {
	if err := c.queueFrame([]byte{packetDisconnection}, ReliableOrdered, 0, next, pending); err != nil {
		return
	}
	deadline := time.Now().Add(disconnectWait)
	nextResend := time.Now().Add(resendInterval)
	buffer := make([]byte, maxDatagramSize)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		poll := readPollInterval
		if remaining < poll {
			poll = remaining
		}
		_ = c.udp.SetReadDeadline(time.Now().Add(poll))
		n, err := c.udp.Read(buffer)
		if err == nil && n > 0 {
			c.handleDisconnectDatagram(buffer[:n], next, pending, received, receiver)
		} else if err != nil {
			if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
				return
			}
		}
		now := time.Now()
		if !now.Before(nextResend) {
			c.resendPending(pending, now)
			nextResend = now.Add(resendInterval)
		}
	}
}

func (c *Conn) handleDisconnectDatagram(data []byte, next *uint16, pending map[uint16]*pendingFrame, received map[uint16]struct{}, receiver *receiverState) {
	acks, frames, err := DecodeDatagram(data)
	if err != nil {
		return
	}
	for _, ack := range acks {
		for id := ack.Min; ; id++ {
			delete(pending, id)
			if id == ack.Max {
				break
			}
		}
	}
	for _, frame := range frames {
		if frame.Reliability.reliable() {
			ack, encodeErr := EncodeDatagram([]Range{{frame.MessageNumber, frame.MessageNumber}}, nil)
			if encodeErr == nil {
				_, _ = c.writeDatagram(ack)
			}
		}
		if _, duplicate := received[frame.MessageNumber]; duplicate {
			continue
		}
		received[frame.MessageNumber] = struct{}{}
		if len(received) > maxReceivedWindow {
			clear(received)
			received[frame.MessageNumber] = struct{}{}
		}
		for _, payload := range receiver.accept(frame, time.Now()) {
			if len(payload) == 0 || payload[0] != packetInternalPing {
				continue
			}
			pong, pongErr := connectedPong(payload, time.Now())
			if pongErr == nil {
				_ = c.queueFrame(pong, Unreliable, 0, next, pending)
			}
		}
	}
}

func (c *Conn) resendPending(pending map[uint16]*pendingFrame, now time.Time) {
	for _, frame := range pending {
		if now.Sub(frame.sent) < resendInterval || frame.attempts >= maxResendAttempts {
			continue
		}
		data, err := EncodeDatagram(nil, []Frame{frame.frame})
		if err != nil {
			continue
		}
		if _, err = c.writeDatagram(data); err != nil {
			continue
		}
		frame.sent = now
		frame.attempts++
	}
}

func connectionRejection(id uint8) error {
	switch id {
	case packetFailedEncryption:
		return ErrEncryption
	case packetConnectionBanned:
		return ErrBanned
	case packetInvalidPassword:
		return ErrInvalidPassword
	default:
		return ErrHandshake
	}
}
func (c *Conn) setTerminalError(err error) {
	c.terminalMu.Lock()
	c.terminalErr = err
	c.terminalMu.Unlock()
}
func (c *Conn) closeReason() error {
	c.terminalMu.RLock()
	defer c.terminalMu.RUnlock()
	if c.terminalErr != nil {
		return c.terminalErr
	}
	return ErrClosed
}
func (c *Conn) deliver(payload []byte) bool {
	select {
	case c.recv <- append([]byte(nil), payload...):
		return true
	case <-c.disconnect:
		return false
	case <-c.ctx.Done():
		return false
	}
}

func (c *Conn) disconnectRequested() bool {
	select {
	case <-c.disconnect:
		return true
	default:
		return false
	}
}
func (c *Conn) AcceptedPacket() []byte { return append([]byte(nil), c.accepted...) }
func (c *Conn) queueFrame(payload []byte, reliability Reliability, channel uint8, next *uint16, pending map[uint16]*pendingFrame) error {
	frame := Frame{MessageNumber: *next, Reliability: reliability, OrderingChannel: channel, Payload: payload, PayloadBits: len(payload) * 8}
	if reliability.ordered() {
		frame.OrderingIndex = c.nextOrderingIndex(reliability, channel)
	}
	*next++
	data, e := EncodeDatagram(nil, []Frame{frame})
	if e != nil {
		return e
	}
	if _, e = c.writeDatagram(data); e != nil {
		return e
	}
	if reliability.reliable() {
		pending[frame.MessageNumber] = &pendingFrame{frame: frame, sent: time.Now(), attempts: 1}
	}
	return nil
}

func (c *Conn) nextOrderingIndex(reliability Reliability, channel uint8) uint16 {
	if reliability == ReliableOrdered {
		index := c.nextOrdered[channel]
		c.nextOrdered[channel]++
		return index
	}
	index := c.nextSequenced[channel]
	c.nextSequenced[channel]++
	return index
}
func (c *Conn) writeDatagram(data []byte) (int, error) {
	n, err := c.udp.Write(encodeSAMPDatagram(data, c.remotePort))
	if n > 0 {
		n--
	}
	return n, err
}
func (s *receiverState) accept(f Frame, now time.Time) [][]byte {
	if f.Split != nil {
		a := s.splits[f.Split.ID]
		if a == nil {
			if len(s.splits) >= maxSplitAssemblies {
				return nil
			}
			a = &splitAssembly{parts: map[uint32]Frame{}, count: f.Split.Count, created: now}
			s.splits[f.Split.ID] = a
		}
		if a.count != f.Split.Count {
			return nil
		}
		if _, duplicate := a.parts[f.Split.Index]; !duplicate {
			partBytes := len(f.Payload)
			if a.bytes+partBytes > maxSplitAssemblyBytes || s.splitBytes+partBytes > maxSplitBufferedBytes {
				s.dropSplit(f.Split.ID)
				return nil
			}
			a.parts[f.Split.Index] = f
			a.bytes += partBytes
			s.splitBytes += partBytes
		}
		if uint32(len(a.parts)) != a.count {
			return nil
		}
		combined := f
		combined.Split = nil
		combined.Payload = make([]byte, 0, a.bytes)
		combined.PayloadBits = 0
		for index := uint32(0); index < a.count; index++ {
			part, ok := a.parts[index]
			if !ok {
				return nil
			}
			combined.Payload = append(combined.Payload, part.Payload...)
			combined.PayloadBits += part.PayloadBits
		}
		s.dropSplit(f.Split.ID)
		f = combined
	}
	channel := int(f.OrderingChannel)
	switch f.Reliability {
	case ReliableOrdered:
		expected := s.expected[channel]
		if f.OrderingIndex == expected {
			out := [][]byte{f.Payload}
			s.expected[channel]++
			queue := s.ordered[channel]
			for queue != nil {
				next, ok := queue[s.expected[channel]]
				if !ok {
					break
				}
				delete(queue, s.expected[channel])
				s.orderedCount--
				out = append(out, next.Payload)
				s.expected[channel]++
			}
			return out
		}
		if sequenceNewer(f.OrderingIndex, expected) && s.orderedCount < maxOrderedFrames {
			if s.ordered[channel] == nil {
				s.ordered[channel] = map[uint16]Frame{}
			}
			if _, ok := s.ordered[channel][f.OrderingIndex]; !ok {
				s.ordered[channel][f.OrderingIndex] = f
				s.orderedCount++
			}
		}
		return nil
	case UnreliableSequenced, ReliableSequenced:
		if s.sequencedSet[channel] && !sequenceNewer(f.OrderingIndex, s.sequenced[channel]) {
			return nil
		}
		s.sequenced[channel] = f.OrderingIndex
		s.sequencedSet[channel] = true
	}
	return [][]byte{f.Payload}
}
func (s *receiverState) expire(now time.Time) {
	for id, a := range s.splits {
		if now.Sub(a.created) > splitAssemblyTTL {
			s.dropSplit(id)
		}
	}
}

func (s *receiverState) dropSplit(id uint16) {
	assembly, ok := s.splits[id]
	if !ok {
		return
	}
	s.splitBytes -= assembly.bytes
	delete(s.splits, id)
}
func sequenceNewer(value, reference uint16) bool { return int16(value-reference) > 0 }
