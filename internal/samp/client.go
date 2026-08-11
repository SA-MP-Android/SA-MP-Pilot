package samp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
	"golang.org/x/text/encoding"
	"sync"
	"time"
)

const (
	netGameVersion         uint32 = 4057
	clientMod              uint8  = 1
	clientAuth                    = "15121F6F18550C00AC4B4F8A167D0379BB0ACA99043"
	clientVersion                 = "0.3.7-R4"
	maxNicknameBytes              = 24
	maxChatBytes                  = 255
	maxCommandBytes               = 1024
	maxDialogInputBytes           = 255
	maxDialogMessageBytes         = 4096
	packetAuthKey          uint8  = 12
	RPCClientJoin          uint8  = 25
	RPCRequestClass        uint8  = 128
	RPCRequestSpawn        uint8  = 129
	RPCSpawn               uint8  = 52
	RPCWorldPlayerAdd      uint8  = 32
	RPCSetSpawnInfo        uint8  = 68
	RPCSetPlayerPos        uint8  = 12
	RPCSetPlayerPosFindZ   uint8  = 13
	RPCSetPlayerSkin       uint8  = 153
	RPCSetPlayerTeam       uint8  = 69
	RPCSetFacingAngle      uint8  = 19
	RPCPutPlayerInVehicle  uint8  = 70
	RPCRemoveFromVehicle   uint8  = 71
	RPCSetPlayerColor      uint8  = 72
	RPCSetPlayerDrunkLevel uint8  = 35
	RPCClickPlayer         uint8  = 23
	RPCEnterVehicle        uint8  = 26
	RPCDialogResponse      uint8  = 62
	RPCClickTextDraw       uint8  = 83
	RPCChat                uint8  = 101
	RPCServerCommand       uint8  = 50
	RPCExitVehicle         uint8  = 154
	RPCUpdateScores        uint8  = 155
	RPCServerJoin          uint8  = 137
	RPCServerQuit          uint8  = 138
	RPCInitGame            uint8  = 139
	RPCClientMessage       uint8  = 93
	RPCClientCheck         uint8  = 103
	RPCDialogBox           uint8  = 61
	RPCShowTextDraw        uint8  = 134
	RPCHideTextDraw        uint8  = 135
	RPCSetTextDrawString   uint8  = 105
	RPCCreateObject        uint8  = 44
	RPCDestroyObject       uint8  = 47
	RPCWorldVehicleAdd     uint8  = 164
	RPCWorldVehicleRemove  uint8  = 165
	packetPlayerSync       uint8  = 207
	packetVehicleSync      uint8  = 200
	packetPassengerSync    uint8  = 211
	packetStatsUpdate      uint8  = 205
	playerSyncChannel      uint8  = 1
	// Android's raksamp network loop runs at roughly 30 FPS and the default
	// SA-MP on-foot send rate is 30 ms. Keep the Go sync cadence close to that
	// loop so the first sync is emitted by the next tick after RPC_Spawn,
	// instead of being written from inside the spawn RPC handler.
	playerSyncInterval              = 33 * time.Millisecond
	scoreRefreshInterval            = 3 * time.Second
	statsUpdateInterval             = time.Second
	targetFramesPerSecond    uint32 = 60
	defaultPlayerMoney       int32  = 0
	gameHandshakeTimeout            = 15 * time.Second
	defaultPlayerHealth      uint8  = 100
	clientCheckMemoryType    uint8  = 0x48
	serverForcedSpawnOutcome uint8  = 2
	initialClassIndex        uint32 = 0
	// CLocalPlayer::ProcessClassSelection in Android raksamp runs on the
	// roughly 30 FPS network loop, so its first RequestClass is sent on the
	// next tick after InitGame rather than after a half-second fallback wait.
	serverInitGracePeriod = playerSyncInterval
	onFootPayloadBytes    = 69
)

// ClientOptions controls compatibility behavior that is optional in the
// Android raksamp client. In particular, PC client-check emulation is off by
// default there and must not be advertised unless explicitly requested.
type ClientOptions struct {
	EmulatePCClientCheck bool
}

var (
	ErrNicknameTooLong = errors.New("samp: nickname is too long")
	ErrMessageTooLong  = errors.New("samp: message is too long")
	ErrMalformedPacket = errors.New("samp: malformed packet")
	ErrSpawnNotReady   = errors.New("samp: spawn information is not ready")
)

type EventType string

const (
	EventJoined        EventType = "joined"
	EventChat          EventType = "chat"
	EventPlayerJoin    EventType = "player.join"
	EventPlayerQuit    EventType = "player.quit"
	EventScores        EventType = "scores"
	EventDialog        EventType = "dialog"
	EventDisconnected  EventType = "disconnected"
	EventProtocolError EventType = "protocol.error"
	EventTextDrawShow  EventType = "textdraw.show"
	EventTextDrawHide  EventType = "textdraw.hide"
	EventTextDrawText  EventType = "textdraw.text"
	EventObjectAdd     EventType = "object.add"
	EventObjectRemove  EventType = "object.remove"
	EventVehicleAdd    EventType = "vehicle.add"
	EventVehicleRemove EventType = "vehicle.remove"
	EventPlayerSync    EventType = "player.sync"
	EventPosition      EventType = "position"
	EventAppearance    EventType = "appearance"
	EventVehicleState  EventType = "vehicle.state"
	EventSpawned       EventType = "spawned"
	EventVehicleSync   EventType = "vehicle.sync"
)

type Event struct {
	Type EventType
	Data any
}
type ChatEvent struct {
	PlayerID *uint16
	Text     string
	Color    uint32
}
type PlayerEvent struct {
	ID          uint16
	Name        string
	Score, Ping int32
	X, Y, Z     float32
	Health      float32
	Armour      float32
	Skin        int32
	Team        uint8
	Rotation    float32
	HasPosition bool
	HasSkin     bool
	HasTeam     bool
	HasRotation bool
	Color       uint32
	HasColor    bool
}
type DialogEvent struct {
	ID                               int16
	Style                            uint8
	Title, Button1, Button2, Message string
	RawMessage                       []byte
}
type TextDrawEvent struct {
	ID                                                     uint16
	Text                                                   string
	Style, Flags, Shadow, Outline, Selectable              uint8
	LetterColor, BoxColor, BackgroundColor                 uint32
	X, Y, LetterWidth, LetterHeight, LineWidth, LineHeight float32
	ModelID                                                uint16
}
type ObjectEvent struct {
	ID      uint16
	ModelID int32
	X, Y, Z float32
}
type VehicleEvent struct {
	ID              uint16
	ModelID         int32
	X, Y, Z, Health float32
}
type VehicleStateEvent struct {
	InVehicle bool
	Passenger bool
	VehicleID uint16
}
type Client struct {
	conn                 *raknet.Conn
	codec                encoding.Encoding
	events               chan Event
	ctx                  context.Context
	cancel               context.CancelFunc
	closeOnce            sync.Once
	stateMu              sync.RWMutex
	position             [3]float32
	keyMask              uint32
	afk                  bool
	vehicleID            uint16
	inVehicle            bool
	passenger            bool
	spawned              bool
	localID              uint16
	skin                 int32
	team                 uint8
	rotation             float32
	drunkLevel           uint32
	drunkLevelSet        bool
	initObserved         bool
	spawnRequested       bool
	spawnInfoReady       bool
	emulatePCClientCheck bool
	clientCheckStart     time.Time
}

func DialClient(ctx context.Context, address, nickname, password, charset string) (*Client, error) {
	return DialClientWithOptions(ctx, address, nickname, password, charset, ClientOptions{})
}

func DialClientWithOptions(ctx context.Context, address, nickname, password, charset string, options ClientOptions) (*Client, error) {
	// raksamp's GetTickCount() is backed by a function-local clock that starts
	// with the native client instance. Keep the emulated ClientCheck value
	// scoped to this connection instead of the Go process/package lifetime.
	clientCheckStart := time.Now()
	codec := codecFor(charset)
	encoded, e := encodeText(codec, nickname)
	if e != nil {
		return nil, e
	}
	if len(encoded) == 0 || len(encoded) > maxNicknameBytes {
		return nil, ErrNicknameTooLong
	}
	conn, e := raknet.Dial(ctx, address, password)
	if e != nil {
		return nil, e
	}
	runCtx, cancel := context.WithCancel(context.Background())
	c := &Client{
		conn:                 conn,
		codec:                codec,
		events:               make(chan Event, 256),
		ctx:                  runCtx,
		cancel:               cancel,
		emulatePCClientCheck: options.EmulatePCClientCheck,
		clientCheckStart:     clientCheckStart,
	}
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, gameHandshakeTimeout)
	defer handshakeCancel()
	var accepted []byte
	for len(accepted) == 0 {
		packet, readErr := conn.Read(handshakeCtx)
		if readErr != nil {
			c.Close()
			return nil, readErr
		}
		switch packet[0] {
		case packetAuthKey:
			if readErr = c.handleAuth(packet); readErr != nil {
				c.Close()
				return nil, readErr
			}
		case raknet.PacketConnectionAccepted:
			accepted = packet
		}
	}
	if len(accepted) < 13 {
		c.Close()
		return nil, ErrMalformedPacket
	}
	challenge := binary.LittleEndian.Uint32(accepted[9:13]) ^ netGameVersion
	payload := raknet.Writer{}
	payload.Uint32(netGameVersion)
	payload.Uint8(clientMod)
	payload.String8(string(encoded))
	payload.Uint32(challenge)
	payload.String8(clientAuth)
	payload.String8(clientVersion)
	rpc := raknet.EncodeRPC(RPCClientJoin, payload.Bytes(), payload.LenBits())
	if e = conn.Write(ctx, rpc, raknet.Reliable); e != nil {
		conn.Close()
		return nil, e
	}
	go c.run()
	go c.syncLoop()
	go c.scoreLoop()
	go c.statsLoop()
	return c, nil
}
func (c *Client) Events() <-chan Event { return c.events }
func (c *Client) Close() error         { c.closeOnce.Do(func() { c.cancel(); c.conn.Close() }); return nil }
func (c *Client) SendChat(ctx context.Context, text string) error {
	encoded, e := encodeText(c.codec, text)
	if e != nil {
		return e
	}
	if len(encoded) == 0 || len(encoded) > maxChatBytes {
		return ErrMessageTooLong
	}
	w := raknet.Writer{}
	w.String8(string(encoded))
	return c.sendRPC(ctx, RPCChat, &w, raknet.Reliable)
}
func (c *Client) SendCommand(ctx context.Context, command string) error {
	encoded, e := encodeText(c.codec, command)
	if e != nil {
		return e
	}
	if len(encoded) == 0 || len(encoded) > maxCommandBytes {
		return ErrMessageTooLong
	}
	w := raknet.Writer{}
	w.Uint32(uint32(len(encoded)))
	w.Bits(encoded, len(encoded)*8, true)
	return c.sendRPC(ctx, RPCServerCommand, &w, raknet.Reliable)
}
func (c *Client) RespondDialog(ctx context.Context, id int16, button uint8, item int16, input string) error {
	encoded, e := encodeText(c.codec, input)
	if e != nil {
		return e
	}
	if len(encoded) > maxDialogInputBytes {
		return ErrMessageTooLong
	}
	w := raknet.Writer{}
	w.Int16(id)
	w.Uint8(button)
	w.Int16(item)
	w.String8(string(encoded))
	return c.sendRPC(ctx, RPCDialogResponse, &w, raknet.ReliableOrdered)
}
func (c *Client) RespondDialogBytes(ctx context.Context, id int16, button uint8, item int16, input []byte) error {
	if len(input) > maxDialogInputBytes {
		return ErrMessageTooLong
	}
	w := raknet.Writer{}
	w.Int16(id)
	w.Uint8(button)
	w.Int16(item)
	w.String8(string(input))
	return c.sendRPC(ctx, RPCDialogResponse, &w, raknet.ReliableOrdered)
}
func (c *Client) RefreshScores(ctx context.Context) error {
	w := raknet.Writer{}
	return c.sendRPC(ctx, RPCUpdateScores, &w, raknet.Reliable)
}

// RequestSpawn mirrors the Android raksamp spawn button. A class response
// only supplies spawn information; the client must explicitly request spawn
// unless the server sends a forced-spawn outcome.
func (c *Client) RequestSpawn(ctx context.Context) error {
	c.stateMu.Lock()
	if !c.spawnInfoReady {
		c.stateMu.Unlock()
		return ErrSpawnNotReady
	}
	if c.spawned || c.spawnRequested {
		c.stateMu.Unlock()
		return nil
	}
	c.spawnRequested = true
	c.stateMu.Unlock()
	request := raknet.Writer{}
	if err := c.sendRPC(ctx, RPCRequestSpawn, &request, raknet.Reliable); err != nil {
		c.stateMu.Lock()
		c.spawnRequested = false
		c.stateMu.Unlock()
		return err
	}
	return nil
}
func (c *Client) ClickPlayer(ctx context.Context, playerID uint16) error {
	w := raknet.Writer{}
	w.Uint16(playerID)
	w.Uint8(0)
	return c.sendRPC(ctx, RPCClickPlayer, &w, raknet.ReliableOrdered)
}
func (c *Client) ClickTextDraw(ctx context.Context, textDrawID uint16) error {
	w := raknet.Writer{}
	w.Uint16(textDrawID)
	return c.sendRPC(ctx, RPCClickTextDraw, &w, raknet.ReliableOrdered)
}
func (c *Client) SetKeys(ctx context.Context, mask uint32) error {
	c.stateMu.Lock()
	c.keyMask = mask
	c.stateMu.Unlock()
	err := c.sendSync(ctx)
	c.stateMu.Lock()
	c.keyMask = 0
	c.stateMu.Unlock()
	return err
}
func (c *Client) SetAFK(enabled bool) {
	c.stateMu.Lock()
	c.afk = enabled
	if enabled {
		c.keyMask = 0
	}
	c.stateMu.Unlock()
}
func (c *Client) Teleport(ctx context.Context, x, y, z float32) error {
	c.stateMu.Lock()
	c.position = [3]float32{x, y, z}
	c.stateMu.Unlock()
	return c.sendSync(ctx)
}
func (c *Client) EnterVehicle(ctx context.Context, vehicleID uint16, passenger bool) error {
	c.stateMu.RLock()
	inVehicle := c.inVehicle
	currentVehicleID := c.vehicleID
	currentPassenger := c.passenger
	c.stateMu.RUnlock()
	if inVehicle && currentVehicleID == vehicleID && currentPassenger == passenger {
		return nil
	}
	if needsVehicleExit(inVehicle, currentVehicleID, currentPassenger, vehicleID, passenger) {
		if err := c.ExitVehicle(ctx); err != nil {
			return err
		}
	}
	w := raknet.Writer{}
	w.Uint16(vehicleID)
	if passenger {
		w.Uint8(1)
	} else {
		w.Uint8(0)
	}
	if err := c.sendRPC(ctx, RPCEnterVehicle, &w, raknet.ReliableSequenced); err != nil {
		return err
	}
	c.stateMu.Lock()
	c.vehicleID = vehicleID
	c.inVehicle = true
	c.passenger = passenger
	c.stateMu.Unlock()
	return nil
}

func needsVehicleExit(inVehicle bool, currentVehicleID uint16, currentPassenger bool, targetVehicleID uint16, targetPassenger bool) bool {
	return inVehicle && (currentVehicleID != targetVehicleID || currentPassenger != targetPassenger)
}
func (c *Client) ExitVehicle(ctx context.Context) error {
	c.stateMu.RLock()
	vehicleID := c.vehicleID
	c.stateMu.RUnlock()
	w := raknet.Writer{}
	w.Uint16(vehicleID)
	if err := c.sendRPC(ctx, RPCExitVehicle, &w, raknet.ReliableSequenced); err != nil {
		return err
	}
	c.stateMu.Lock()
	c.vehicleID = 0
	c.inVehicle = false
	c.passenger = false
	c.stateMu.Unlock()
	return nil
}
func (c *Client) syncLoop() {
	ticker := time.NewTicker(playerSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.stateMu.RLock()
			shouldSync := c.spawned && !c.afk
			c.stateMu.RUnlock()
			if shouldSync {
				_ = c.sendSync(c.ctx)
			}
		}
	}
}
func (c *Client) scoreLoop() {
	ticker := time.NewTicker(scoreRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			_ = c.RefreshScores(c.ctx)
		}
	}
}
func (c *Client) sendSync(ctx context.Context) error {
	c.stateMu.RLock()
	spawned, inVehicle, passenger := c.spawned, c.inVehicle, c.passenger
	c.stateMu.RUnlock()
	if !spawned {
		return nil
	}
	if !inVehicle {
		return c.sendOnFoot(ctx)
	}
	return c.sendVehicle(ctx, passenger)
}
func (c *Client) sendOnFoot(ctx context.Context) error {
	c.stateMu.RLock()
	payload := encodeOnFoot(c.position, c.keyMask)
	c.stateMu.RUnlock()
	return c.conn.WriteChannel(ctx, payload, raknet.UnreliableSequenced, playerSyncChannel)
}
func (c *Client) sendVehicle(ctx context.Context, passenger bool) error {
	c.stateMu.RLock()
	vehicleID, position, mask := c.vehicleID, c.position, c.keyMask
	c.stateMu.RUnlock()
	w := raknet.Writer{}
	if passenger {
		w.Uint8(packetPassengerSync)
		w.Uint16(vehicleID)
		w.Uint8(1)
		w.Uint8(protocolAdditionalKey(mask) << 6)
		w.Uint8(defaultPlayerHealth)
		w.Uint8(0)
		w.Uint16(0)
		w.Uint16(0)
		w.Uint16(protocolVehicleKeys(mask))
		for _, value := range position {
			w.Float32(value)
		}
		return c.conn.WriteChannel(ctx, w.Bytes(), raknet.UnreliableSequenced, playerSyncChannel)
	}
	w.Uint8(packetVehicleSync)
	w.Uint16(vehicleID)
	w.Uint16(0)
	w.Uint16(0)
	w.Uint16(protocolVehicleKeys(mask))
	// Keep the vehicle quaternion identical to Android raksamp's stubbed
	// SetFromMatrix path, which leaves the zero-initialized value unchanged.
	for range 4 {
		w.Float32(0)
	}
	for _, value := range position {
		w.Float32(value)
	}
	for range 3 {
		w.Float32(0)
	}
	w.Float32(1000)
	w.Uint8(defaultPlayerHealth)
	w.Uint8(0)
	w.Uint8(protocolAdditionalKey(mask) << 6)
	w.Uint8(0)
	w.Uint8(0)
	w.Uint16(^uint16(0))
	w.Float32(0)
	return c.conn.WriteChannel(ctx, w.Bytes(), raknet.UnreliableSequenced, 0)
}
func encodeOnFoot(position [3]float32, mask uint32) []byte {
	w := raknet.Writer{}
	w.Uint8(packetPlayerSync)
	w.Uint16(0)
	w.Uint16(0)
	w.Uint16(protocolKeys(mask))
	for _, value := range position {
		w.Float32(value)
	}
	// Android raksamp currently has a stubbed SetFromMatrix(), so its
	// zero-initialized on-foot quaternion remains four zero floats.
	for range 4 {
		w.Float32(0)
	}
	w.Uint8(defaultPlayerHealth)
	w.Uint8(0)
	w.Uint8(protocolAdditionalKey(mask) << 6)
	w.Uint8(0)
	for range 6 {
		w.Float32(0)
	}
	w.Uint16(0)
	w.Uint32(0)
	return w.Bytes()
}
func protocolKeys(mask uint32) uint16 {
	var keys uint16
	if mask&(1<<2) != 0 {
		keys |= 1 << 9
	}
	if mask&(1<<3) != 0 {
		keys |= 1 << 1
	}
	if mask&(1<<5) != 0 {
		keys |= 1 << 10
	}
	if mask&(1<<6) != 0 {
		keys |= 1 << 3
	}
	return keys
}
func protocolVehicleKeys(mask uint32) uint16 {
	keys := protocolKeys(mask)
	keys &^= (1 << 10) | (1 << 3)
	if mask&(1<<4) != 0 {
		keys |= 1 << 1
	}
	if mask&(1<<5) != 0 {
		keys |= 1 << 2
	}
	if mask&(1<<6) != 0 {
		keys |= 1 << 7
	}
	return keys
}
func protocolAdditionalKey(mask uint32) uint8 {
	switch {
	case mask&(1<<4) != 0:
		return 3
	case mask&(1<<1) != 0:
		return 2
	case mask&(1<<0) != 0:
		return 1
	default:
		return 0
	}
}
func (c *Client) sendRPC(ctx context.Context, id uint8, w *raknet.Writer, reliability raknet.Reliability) error {
	return c.conn.Write(ctx, raknet.EncodeRPC(id, w.Bytes(), w.LenBits()), reliability)
}
func (c *Client) run() {
	defer close(c.events)
	for {
		packet, e := c.conn.Read(c.ctx)
		if e != nil {
			c.emit(Event{Type: EventDisconnected, Data: e})
			return
		}
		if len(packet) == 0 {
			continue
		}
		if packet[0] == packetAuthKey {
			if e = c.handleAuth(packet); e != nil {
				c.emit(Event{Type: EventProtocolError, Data: e.Error()})
			}
			continue
		}
		if packet[0] == packetPlayerSync {
			if event, decodeErr := decodePlayerSync(packet); decodeErr == nil {
				c.emit(Event{Type: EventPlayerSync, Data: event})
			} else {
				c.emit(Event{Type: EventProtocolError, Data: fmt.Sprintf("packet %d (%d bytes): %v", packet[0], len(packet), decodeErr)})
			}
			continue
		}
		if packet[0] == packetVehicleSync {
			if player, vehicle, decodeErr := decodeVehicleSync(packet); decodeErr == nil {
				c.emit(Event{Type: EventPlayerSync, Data: player})
				c.emit(Event{Type: EventVehicleSync, Data: vehicle})
			} else {
				c.emit(Event{Type: EventProtocolError, Data: fmt.Sprintf("packet %d (%d bytes): %v", packet[0], len(packet), decodeErr)})
			}
			continue
		}
		if packet[0] != raknet.PacketRPC && packet[0] != raknet.PacketTimestamp {
			continue
		}
		rpc, e := raknet.DecodeRPC(packet)
		if e != nil {
			c.emit(Event{Type: EventProtocolError, Data: fmt.Sprintf("RPC envelope (%d bytes): %v", len(packet), e)})
			continue
		}
		if event, e := c.decodeRPC(rpc); e != nil {
			c.emit(Event{Type: EventProtocolError, Data: fmt.Sprintf("RPC %d (%d bits): %v", rpc.ID, rpc.PayloadBits, e)})
		} else if event != nil {
			c.emit(*event)
		}
	}
}
func decodePlayerSync(packet []byte) (PlayerEvent, error) {
	r := raknet.NewReader(packet)
	if _, err := r.Uint8(); err != nil {
		return PlayerEvent{}, err
	}
	id, err := r.Uint16()
	if err != nil {
		return PlayerEvent{}, err
	}
	for range 2 {
		hasAnalog, readErr := r.Bit()
		if readErr != nil {
			return PlayerEvent{}, readErr
		}
		if hasAnalog {
			if _, readErr = r.Uint16(); readErr != nil {
				return PlayerEvent{}, readErr
			}
		}
	}
	if _, err = r.Uint16(); err != nil {
		return PlayerEvent{}, err
	}
	position, err := readPosition(r)
	if err != nil {
		return PlayerEvent{}, err
	}
	if err = skipNormQuaternion(r); err != nil {
		return PlayerEvent{}, err
	}
	healthArmour, err := r.Uint8()
	if err != nil {
		return PlayerEvent{}, err
	}
	return PlayerEvent{ID: id, X: position[0], Y: position[1], Z: position[2], Health: syncNibble(healthArmour >> 4), Armour: syncNibble(healthArmour & 0x0f)}, nil
}
func decodeVehicleSync(packet []byte) (PlayerEvent, VehicleEvent, error) {
	r := raknet.NewReader(packet)
	if _, err := r.Uint8(); err != nil {
		return PlayerEvent{}, VehicleEvent{}, err
	}
	playerID, err := r.Uint16()
	if err != nil {
		return PlayerEvent{}, VehicleEvent{}, err
	}
	vehicleID, err := r.Uint16()
	if err != nil {
		return PlayerEvent{}, VehicleEvent{}, err
	}
	for range 3 {
		if _, err = r.Uint16(); err != nil {
			return PlayerEvent{}, VehicleEvent{}, err
		}
	}
	if err = skipNormQuaternion(r); err != nil {
		return PlayerEvent{}, VehicleEvent{}, err
	}
	position, err := readPosition(r)
	if err != nil {
		return PlayerEvent{}, VehicleEvent{}, err
	}
	if err = skipCompressedVector(r); err != nil {
		return PlayerEvent{}, VehicleEvent{}, err
	}
	vehicleHealth, err := r.Uint16()
	if err != nil {
		return PlayerEvent{}, VehicleEvent{}, err
	}
	healthArmour, err := r.Uint8()
	if err != nil {
		return PlayerEvent{}, VehicleEvent{}, err
	}
	player := PlayerEvent{ID: playerID, X: position[0], Y: position[1], Z: position[2], Health: syncNibble(healthArmour >> 4), Armour: syncNibble(healthArmour & 0x0f)}
	vehicle := VehicleEvent{ID: vehicleID, X: position[0], Y: position[1], Z: position[2], Health: float32(vehicleHealth)}
	return player, vehicle, nil
}
func readPosition(r *raknet.Reader) ([3]float32, error) {
	var position [3]float32
	for index := range position {
		value, err := r.Float32()
		if err != nil {
			return position, err
		}
		position[index] = value
	}
	return position, nil
}
func skipNormQuaternion(r *raknet.Reader) error {
	if _, err := r.Bits(4, false); err != nil {
		return err
	}
	for range 3 {
		if _, err := r.Uint16(); err != nil {
			return err
		}
	}
	return nil
}
func skipCompressedVector(r *raknet.Reader) error {
	magnitude, err := r.Float32()
	if err != nil || magnitude == 0 {
		return err
	}
	for range 3 {
		if _, err = r.Uint16(); err != nil {
			return err
		}
	}
	return nil
}
func syncNibble(value uint8) float32 {
	switch value {
	case 0:
		return 0
	case 0x0f:
		return 100
	default:
		return float32(value * 7)
	}
}
func (c *Client) handleAuth(packet []byte) error {
	if len(packet) < 2 || int(packet[1])+2 > len(packet) {
		return ErrMalformedPacket
	}
	key := AuthKey(string(packet[2 : 2+int(packet[1])]))
	if len(key) > maxChatBytes {
		return ErrMalformedPacket
	}
	response := append([]byte{packetAuthKey, uint8(len(key))}, key...)
	return c.conn.Write(c.ctx, response, raknet.Reliable)
}
func (c *Client) decodeRPC(rpc raknet.RPC) (*Event, error) {
	r := raknet.NewReaderBits(rpc.Payload, rpc.PayloadBits)
	switch rpc.ID {
	case RPCInitGame:
		c.setSpawned(false)
		localPlayerID, e := decodeInitGameLocalPlayerID(r)
		if e != nil {
			return nil, e
		}
		c.stateMu.Lock()
		c.localID = localPlayerID
		c.initObserved = false
		c.spawnRequested = false
		c.spawnInfoReady = false
		c.stateMu.Unlock()
		go c.requestInitialClassFallback()
		return &Event{Type: EventJoined, Data: PlayerEvent{ID: localPlayerID}}, nil
	case RPCRequestClass:
		outcome, e := r.Uint8()
		if e != nil {
			return nil, e
		}
		if outcome == 0 {
			return nil, nil
		}
		spawnInfo, e := decodeSpawnInfo(r)
		if e != nil {
			return nil, e
		}
		c.setSpawnInfo(spawnInfo)
		appearance := spawnInfo.PlayerEvent()
		c.stateMu.RLock()
		appearance.ID = c.localID
		c.stateMu.RUnlock()
		return &Event{Type: EventAppearance, Data: appearance}, nil
	case RPCRequestSpawn:
		c.observeServerInitialization()
		outcome, e := r.Uint8()
		if e != nil {
			return nil, e
		}
		c.stateMu.RLock()
		spawnRequested := c.spawnRequested
		c.stateMu.RUnlock()
		spawnApproved := outcome == serverForcedSpawnOutcome || (outcome != 0 && spawnRequested)
		if spawnApproved {
			spawn := raknet.Writer{}
			if e = c.sendRPC(c.ctx, RPCSpawn, &spawn, raknet.ReliableOrdered); e != nil {
				return nil, e
			}
			c.setSpawned(true)
		} else if outcome == 0 {
			c.stateMu.Lock()
			c.spawnRequested = false
			c.stateMu.Unlock()
		}
		if spawnApproved {
			return &Event{Type: EventSpawned}, nil
		}
		return nil, nil
	case RPCSetSpawnInfo:
		c.observeServerInitialization()
		spawnInfo, e := decodeSpawnInfo(r)
		if e != nil {
			return nil, e
		}
		c.setSpawnInfo(spawnInfo)
		appearance := spawnInfo.PlayerEvent()
		c.stateMu.RLock()
		appearance.ID = c.localID
		c.stateMu.RUnlock()
		return &Event{Type: EventAppearance, Data: appearance}, nil
	case RPCSetPlayerDrunkLevel:
		level, e := r.Uint32()
		if e != nil {
			return nil, e
		}
		c.setDrunkLevel(level)
		return nil, nil
	case RPCSetPlayerPos, RPCSetPlayerPosFindZ:
		c.observeServerInitialization()
		position, e := readPosition(r)
		if e != nil {
			return nil, e
		}
		c.setOnFootPosition(position)
		if e = c.sendSync(c.ctx); e != nil {
			return nil, e
		}
		return &Event{Type: EventPosition, Data: position}, nil
	case RPCPutPlayerInVehicle:
		c.observeServerInitialization()
		vehicleID, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		seatID, e := r.Uint8()
		if e != nil {
			return nil, e
		}
		passenger := seatID != 0
		c.setVehicleState(vehicleID, passenger)
		return &Event{Type: EventVehicleState, Data: VehicleStateEvent{InVehicle: true, Passenger: passenger, VehicleID: vehicleID}}, nil
	case RPCRemoveFromVehicle:
		c.clearVehicleState()
		return &Event{Type: EventVehicleState, Data: VehicleStateEvent{}}, nil
	case RPCWorldPlayerAdd:
		player, e := decodeWorldPlayerAdd(r)
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventPlayerSync, Data: player}, nil
	case RPCSetPlayerSkin:
		playerID, e := r.Uint32()
		if e != nil {
			return nil, e
		}
		skin, e := r.Uint32()
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventAppearance, Data: PlayerEvent{ID: uint16(playerID), Skin: int32(skin), HasSkin: true}}, nil
	case RPCSetPlayerTeam:
		playerID, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		team, e := r.Uint8()
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventAppearance, Data: PlayerEvent{ID: playerID, Team: team, HasTeam: true}}, nil
	case RPCSetPlayerColor:
		playerID, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		color, e := r.Uint32()
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventAppearance, Data: PlayerEvent{ID: playerID, Color: color, HasColor: true}}, nil
	case RPCSetFacingAngle:
		rotation, e := r.Float32()
		if e != nil {
			return nil, e
		}
		c.stateMu.RLock()
		localID := c.localID
		c.stateMu.RUnlock()
		return &Event{Type: EventAppearance, Data: PlayerEvent{ID: localID, Rotation: rotation, HasRotation: true}}, nil
	case RPCClientMessage:
		color, e := r.Uint32()
		if e != nil {
			return nil, e
		}
		n, e := r.Uint32()
		if e != nil || n > maxChatBytes {
			return nil, ErrMalformedPacket
		}
		b, e := r.Bits(int(n)*8, true)
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventChat, Data: ChatEvent{Text: decodeText(c.codec, b), Color: color}}, nil
	case RPCChat:
		id, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		text, e := r.String8()
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventChat, Data: ChatEvent{PlayerID: &id, Text: decodeText(c.codec, []byte(text))}}, nil
	case RPCServerJoin:
		id, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		markerColor, e := r.Uint32()
		if e != nil {
			return nil, e
		}
		_, e = r.Uint8()
		if e != nil {
			return nil, e
		}
		name, e := r.String8()
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventPlayerJoin, Data: PlayerEvent{ID: id, Name: decodeText(c.codec, []byte(name)), Color: markerColor, HasColor: true}}, nil
	case RPCServerQuit:
		id, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventPlayerQuit, Data: PlayerEvent{ID: id}}, nil
	case RPCUpdateScores:
		players := []PlayerEvent{}
		for r.Remaining() >= 80 {
			id, e := r.Uint16()
			if e != nil {
				return nil, e
			}
			score, e := r.Uint32()
			if e != nil {
				return nil, e
			}
			ping, e := r.Uint32()
			if e != nil {
				return nil, e
			}
			players = append(players, PlayerEvent{ID: id, Score: int32(score), Ping: int32(ping)})
		}
		return &Event{Type: EventScores, Data: players}, nil
	case RPCClientCheck:
		if !c.emulatePCClientCheck {
			return nil, nil
		}
		checkType, e := r.Uint8()
		if e != nil {
			return nil, e
		}
		address, e := r.Uint32()
		if e != nil {
			return nil, e
		}
		if checkType != clientCheckMemoryType {
			return nil, nil
		}
		response := raknet.Writer{}
		response.Uint8(checkType)
		response.Uint32(address)
		response.Uint8(c.clientCheckMillisecondsLowByte())
		if e = c.sendRPC(c.ctx, RPCClientCheck, &response, raknet.Reliable); e != nil {
			return nil, e
		}
		return nil, nil
	case RPCDialogBox:
		c.observeServerInitialization()
		d, e := decodeDialog(r, c.codec)
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventDialog, Data: d}, nil
	case RPCShowTextDraw:
		v, e := decodeTextDraw(r, c.codec)
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventTextDrawShow, Data: v}, nil
	case RPCHideTextDraw:
		id, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventTextDrawHide, Data: TextDrawEvent{ID: id}}, nil
	case RPCSetTextDrawString:
		id, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		n, e := r.Uint16()
		if e != nil || n > maxCommandBytes {
			return nil, ErrMalformedPacket
		}
		b, e := r.Bits(int(n)*8, true)
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventTextDrawText, Data: TextDrawEvent{ID: id, Text: decodeText(c.codec, b)}}, nil
	case RPCCreateObject:
		v, e := decodeObject(r)
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventObjectAdd, Data: v}, nil
	case RPCDestroyObject:
		id, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventObjectRemove, Data: ObjectEvent{ID: id}}, nil
	case RPCWorldVehicleAdd:
		v, e := decodeVehicle(r)
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventVehicleAdd, Data: v}, nil
	case RPCWorldVehicleRemove:
		id, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventVehicleRemove, Data: VehicleEvent{ID: id}}, nil
	}
	return nil, nil
}

func (c *Client) clientCheckMillisecondsLowByte() uint8 {
	start := c.clientCheckStart
	if start.IsZero() {
		// Keep zero-value Clients useful for protocol decoding tests and for
		// callers that construct a Client internally.
		start = time.Now()
	}
	return uint8(time.Since(start).Milliseconds())
}

func decodeInitGameLocalPlayerID(r *raknet.Reader) (uint16, error) {
	for range 4 {
		if _, err := r.Bit(); err != nil {
			return 0, err
		}
	}
	if _, err := r.Float32(); err != nil {
		return 0, err
	}
	if _, err := r.Bit(); err != nil {
		return 0, err
	}
	if _, err := r.Float32(); err != nil {
		return 0, err
	}
	for range 3 {
		if _, err := r.Bit(); err != nil {
			return 0, err
		}
	}
	if _, err := r.Uint32(); err != nil {
		return 0, err
	}
	return r.Uint16()
}

type SpawnInfo struct {
	Team     uint8
	Skin     int32
	Position [3]float32
	Rotation float32
}

func (s SpawnInfo) PlayerEvent() PlayerEvent {
	return PlayerEvent{Skin: s.Skin, Team: s.Team, X: s.Position[0], Y: s.Position[1], Z: s.Position[2], Rotation: s.Rotation, HasPosition: true, HasSkin: true, HasTeam: true, HasRotation: true}
}

func decodeSpawnInfo(r *raknet.Reader) (SpawnInfo, error) {
	team, err := r.Uint8()
	if err != nil {
		return SpawnInfo{}, err
	}
	skin, err := r.Uint32()
	if err != nil {
		return SpawnInfo{}, err
	}
	if _, err := r.Uint8(); err != nil {
		return SpawnInfo{}, err
	}
	position, err := readPosition(r)
	if err != nil {
		return SpawnInfo{}, err
	}
	rotation, err := r.Float32()
	if err != nil {
		return SpawnInfo{}, err
	}
	return SpawnInfo{Team: team, Skin: int32(skin), Position: position, Rotation: rotation}, nil
}

func decodeWorldPlayerAdd(r *raknet.Reader) (PlayerEvent, error) {
	id, err := r.Uint16()
	if err != nil {
		return PlayerEvent{}, err
	}
	team, err := r.Uint8()
	if err != nil {
		return PlayerEvent{}, err
	}
	skin, err := r.Uint32()
	if err != nil {
		return PlayerEvent{}, err
	}
	position, err := readPosition(r)
	if err != nil {
		return PlayerEvent{}, err
	}
	rotation, err := r.Float32()
	if err != nil {
		return PlayerEvent{}, err
	}
	color, err := r.Uint32()
	if err != nil {
		return PlayerEvent{}, err
	}
	return PlayerEvent{ID: id, Team: team, Skin: int32(skin), X: position[0], Y: position[1], Z: position[2], Rotation: rotation, Color: color, HasPosition: true, HasSkin: true, HasTeam: true, HasRotation: true, HasColor: true}, nil
}

func (c *Client) setPosition(position [3]float32) {
	c.stateMu.Lock()
	c.position = position
	c.stateMu.Unlock()
}

func (c *Client) setOnFootPosition(position [3]float32) {
	c.stateMu.Lock()
	c.position = position
	c.inVehicle = false
	c.passenger = false
	c.vehicleID = 0
	c.stateMu.Unlock()
}

func (c *Client) setVehicleState(vehicleID uint16, passenger bool) {
	c.stateMu.Lock()
	c.vehicleID, c.inVehicle, c.passenger = vehicleID, true, passenger
	c.stateMu.Unlock()
}

func (c *Client) clearVehicleState() {
	c.stateMu.Lock()
	c.vehicleID, c.inVehicle, c.passenger = 0, false, false
	c.stateMu.Unlock()
}

func (c *Client) setSpawnInfo(info SpawnInfo) {
	c.stateMu.Lock()
	c.position, c.skin, c.team, c.rotation = info.Position, info.Skin, info.Team, info.Rotation
	c.spawnInfoReady = true
	c.stateMu.Unlock()
}

func (c *Client) setSpawned(spawned bool) {
	c.stateMu.Lock()
	c.spawned = spawned
	c.stateMu.Unlock()
}

func (c *Client) observeServerInitialization() {
	c.stateMu.Lock()
	c.initObserved = true
	c.stateMu.Unlock()
}

func (c *Client) shouldRequestInitialClass() bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return !c.spawned && !c.initObserved
}

func (c *Client) requestInitialClassFallback() {
	timer := time.NewTimer(serverInitGracePeriod)
	defer timer.Stop()
	select {
	case <-c.ctx.Done():
		return
	case <-timer.C:
	}
	if !c.shouldRequestInitialClass() {
		return
	}
	request := raknet.Writer{}
	request.Uint32(initialClassIndex)
	_ = c.sendRPC(c.ctx, RPCRequestClass, &request, raknet.Reliable)
}

func decodeTextDraw(r *raknet.Reader, codec encoding.Encoding) (TextDrawEvent, error) {
	var v TextDrawEvent
	var e error
	if v.ID, e = r.Uint16(); e != nil {
		return v, e
	}
	if v.Flags, e = r.Uint8(); e != nil {
		return v, e
	}
	if v.LetterWidth, e = r.Float32(); e != nil {
		return v, e
	}
	if v.LetterHeight, e = r.Float32(); e != nil {
		return v, e
	}
	if v.LetterColor, e = r.Uint32(); e != nil {
		return v, e
	}
	if v.LineWidth, e = r.Float32(); e != nil {
		return v, e
	}
	if v.LineHeight, e = r.Float32(); e != nil {
		return v, e
	}
	if v.BoxColor, e = r.Uint32(); e != nil {
		return v, e
	}
	if v.Shadow, e = r.Uint8(); e != nil {
		return v, e
	}
	if v.Outline, e = r.Uint8(); e != nil {
		return v, e
	}
	if v.BackgroundColor, e = r.Uint32(); e != nil {
		return v, e
	}
	if v.Style, e = r.Uint8(); e != nil {
		return v, e
	}
	if v.Selectable, e = r.Uint8(); e != nil {
		return v, e
	}
	if v.X, e = r.Float32(); e != nil {
		return v, e
	}
	if v.Y, e = r.Float32(); e != nil {
		return v, e
	}
	if v.ModelID, e = r.Uint16(); e != nil {
		return v, e
	}
	if _, e = r.Bits(20*8, true); e != nil {
		return v, e
	}
	n, e := r.Uint16()
	if e != nil || n > maxCommandBytes {
		return v, ErrMalformedPacket
	}
	b, e := r.Bits(int(n)*8, true)
	v.Text = decodeText(codec, b)
	return v, e
}
func decodeObject(r *raknet.Reader) (ObjectEvent, error) {
	var v ObjectEvent
	id, e := r.Uint16()
	if e != nil {
		return v, e
	}
	model, e := r.Uint32()
	if e != nil {
		return v, e
	}
	v.ID = id
	v.ModelID = int32(model)
	if v.X, e = r.Float32(); e != nil {
		return v, e
	}
	if v.Y, e = r.Float32(); e != nil {
		return v, e
	}
	if v.Z, e = r.Float32(); e != nil {
		return v, e
	}
	return v, nil
}
func decodeVehicle(r *raknet.Reader) (VehicleEvent, error) {
	var v VehicleEvent
	id, e := r.Uint16()
	if e != nil {
		return v, e
	}
	model, e := r.Uint32()
	if e != nil {
		return v, e
	}
	v.ID = id
	v.ModelID = int32(model)
	if v.X, e = r.Float32(); e != nil {
		return v, e
	}
	if v.Y, e = r.Float32(); e != nil {
		return v, e
	}
	if v.Z, e = r.Float32(); e != nil {
		return v, e
	}
	if _, e = r.Float32(); e != nil {
		return v, e
	}
	if _, e = r.Bits(2*8, true); e != nil {
		return v, e
	}
	if v.Health, e = r.Float32(); e != nil {
		return v, e
	}
	return v, nil
}
func decodeDialog(r *raknet.Reader, codec encoding.Encoding) (DialogEvent, error) {
	id, e := r.Int16()
	if e != nil {
		return DialogEvent{}, e
	}
	style, e := r.Uint8()
	if e != nil {
		return DialogEvent{}, e
	}
	title, e := r.String8()
	if e != nil {
		return DialogEvent{}, e
	}
	b1, e := r.String8()
	if e != nil {
		return DialogEvent{}, e
	}
	b2, e := r.String8()
	if e != nil {
		return DialogEvent{}, e
	}
	message, e := decodeHuffmanString(r, maxDialogMessageBytes)
	if e != nil {
		return DialogEvent{}, e
	}
	return DialogEvent{ID: id, Style: style, Title: decodeText(codec, []byte(title)), Button1: decodeText(codec, []byte(b1)), Button2: decodeText(codec, []byte(b2)), Message: decodeText(codec, message), RawMessage: append([]byte(nil), message...)}, nil
}
func (c *Client) emit(e Event) {
	select {
	case c.events <- e:
	case <-c.ctx.Done():
	}
}
