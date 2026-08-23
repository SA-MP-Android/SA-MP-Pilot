package samp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
	"golang.org/x/text/encoding"
	"math"
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
	RPCDeath               uint8  = 53
	RPCRequestClass        uint8  = 128
	RPCRequestSpawn        uint8  = 129
	RPCSpawn               uint8  = 52
	RPCWorldPlayerAdd      uint8  = 32
	RPCSetSpawnInfo        uint8  = 68
	RPCSetPlayerPos        uint8  = 12
	RPCSetPlayerPosFindZ   uint8  = 13
	RPCSetPlayerHealth     uint8  = 14
	RPCSetPlayerSkin       uint8  = 153
	RPCSetPlayerTeam       uint8  = 69
	RPCSetFacingAngle      uint8  = 19
	RPCSetPlayerArmour     uint8  = 66
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
	RPCVehicleDeath        uint8  = 136
	RPCSetVehicleHealth    uint8  = 147
	packetPlayerSync       uint8  = 207
	packetVehicleSync      uint8  = 200
	packetPassengerSync    uint8  = 211
	packetStatsUpdate      uint8  = 205
	// The Android client sends on-foot and passenger sync on channel 1. Vehicle
	// sync remains on channel 0, as in CLocalPlayer::SendInCarFullSyncData.
	// Enter/exit RPCs use channel 0 independently of the sync stream.
	playerSyncChannel uint8 = 1
	// Android's raksamp network loop runs at roughly 30 FPS and the default
	// SA-MP on-foot send rate is 30 ms. Keep the Go sync cadence close to that
	// loop so the first sync is emitted by the next tick after RPC_Spawn,
	// instead of being written from inside the spawn RPC handler.
	playerSyncInterval = 30 * time.Millisecond
	// A real GTA entry task keeps the ped on foot for roughly this interval
	// before the local game starts emitting vehicle/passenger sync. There is no
	// animation or task engine in the headless client, so normal entry models
	// the network-visible part of that transition with this bounded phase.
	normalVehicleEntryDuration              = 1200 * time.Millisecond
	normalVehicleEntryMaxDuration           = 3 * time.Second
	normalVehicleEntryApproachSpeed float32 = 3
	normalVehicleEntryStandOff      float32 = 1.25
	normalVehicleEntryMaxDistance   float32 = 8
	scoreRefreshInterval                    = 3 * time.Second
	statsUpdateInterval                     = time.Second
	targetFramesPerSecond           uint32  = 60
	defaultPlayerMoney              int32   = 0
	gameHandshakeTimeout                    = 15 * time.Second
	defaultPlayerHealth             uint8   = 100
	defaultPlayerHealthValue        float32 = float32(defaultPlayerHealth)
	clientCheckMemoryType           uint8   = 0x48
	serverForcedSpawnOutcome        uint8   = 2
	initialClassIndex               uint32  = 0
	// CLocalPlayer::ProcessClassSelection in Android raksamp runs on the
	// roughly 30 FPS network loop, so its first RequestClass is sent on the
	// next tick after InitGame rather than after a half-second fallback wait.
	serverInitGracePeriod = playerSyncInterval
	onFootPayloadBytes    = 69
	maxSyncWeaponID       = 46
)

// RespawnPolicy controls who drives the spawn transaction after class data or
// death is available. The protocol client remains usable in manual mode, while
// the application can opt into Android-like automatic spawning.
type RespawnPolicy uint8

const (
	RespawnPolicyManual RespawnPolicy = iota
	RespawnPolicyAutomatic
)

// ClientOptions controls compatibility behavior that is optional in the
// Android raksamp client. In particular, PC client-check emulation is off by
// default there and must not be advertised unless explicitly requested.
type ClientOptions struct {
	EmulatePCClientCheck bool
	RespawnPolicy        RespawnPolicy
}

var (
	ErrNicknameTooLong        = errors.New("samp: nickname is too long")
	ErrMessageTooLong         = errors.New("samp: message is too long")
	ErrMalformedPacket        = errors.New("samp: malformed packet")
	ErrClientNotConnected     = errors.New("samp: client is not connected")
	ErrClientNotSpawned       = errors.New("samp: client is not spawned")
	ErrSpawnNotReady          = errors.New("samp: spawn information is not ready")
	ErrSpawnCooldown          = errors.New("samp: respawn cooldown is active")
	ErrVehicleEntryInProgress = errors.New("samp: another vehicle entry is already in progress")
	ErrVehicleEntryCanceled   = errors.New("samp: vehicle entry was canceled before completion")
	ErrVehicleEntryOutOfRange = errors.New("samp: vehicle is not streamed or is outside the normal entry range")
)

// VehicleEntryMode controls how the headless client transitions from on-foot
// to vehicle synchronization. Direct is the historical behavior: the local
// state changes as soon as RPC_EnterVehicle is sent. Normal preserves the
// network sequence of a regular client: RPC_EnterVehicle marks the beginning
// of entry, on-foot sync continues during the entry task, and vehicle sync
// starts only after the entry phase completes.
type VehicleEntryMode string

const (
	VehicleEntryDirect VehicleEntryMode = "direct"
	VehicleEntryNormal VehicleEntryMode = "normal"
)

// NormalizeVehicleEntryMode validates an entry mode and maps the empty value
// to the backward-compatible direct mode.
func NormalizeVehicleEntryMode(mode VehicleEntryMode) (VehicleEntryMode, error) {
	if mode == "" {
		return VehicleEntryDirect, nil
	}
	switch mode {
	case VehicleEntryDirect, VehicleEntryNormal:
		return mode, nil
	default:
		return "", fmt.Errorf("samp: unsupported vehicle entry mode %q (want %q or %q)", mode, VehicleEntryDirect, VehicleEntryNormal)
	}
}

type EventType string

const (
	EventJoined          EventType = "joined"
	EventChat            EventType = "chat"
	EventPlayerJoin      EventType = "player.join"
	EventPlayerQuit      EventType = "player.quit"
	EventScores          EventType = "scores"
	EventDialog          EventType = "dialog"
	EventDisconnected    EventType = "disconnected"
	EventProtocolError   EventType = "protocol.error"
	EventTextDrawShow    EventType = "textdraw.show"
	EventTextDrawHide    EventType = "textdraw.hide"
	EventTextDrawText    EventType = "textdraw.text"
	EventObjectAdd       EventType = "object.add"
	EventObjectRemove    EventType = "object.remove"
	EventVehicleAdd      EventType = "vehicle.add"
	EventVehicleRemove   EventType = "vehicle.remove"
	EventPlayerSync      EventType = "player.sync"
	EventPosition        EventType = "position"
	EventAppearance      EventType = "appearance"
	EventVehicleState    EventType = "vehicle.state"
	EventPlayerHealth    EventType = "player.health"
	EventPlayerLifeState EventType = "player.state"
	EventPlayerDeath     EventType = "player.death"
	EventVehicleHealth   EventType = "vehicle.health"
	EventSpawned         EventType = "spawned"
	EventVehicleSync     EventType = "vehicle.sync"
	EventMovement        EventType = "movement"
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
	Angle           float32
}
type VehicleStateEvent struct {
	InVehicle bool
	Passenger bool
	VehicleID uint16
	Health    float32
	HasHealth bool
}
type SpawnedEvent struct {
	Health float32
	Armour float32
}
type PlayerHealthEvent struct {
	Health float32
	Armour float32
}
type VehicleHealthEvent struct {
	ID     uint16
	Health float32
}
type Client struct {
	conn                  *raknet.Conn
	rpcSender             func(context.Context, uint8, []byte, int, raknet.Reliability) error
	codec                 encoding.Encoding
	events                chan Event
	ctx                   context.Context
	cancel                context.CancelFunc
	closeOnce             sync.Once
	eventsMu              sync.RWMutex
	eventsClosed          bool
	eventQueue            chan Event
	eventStop             chan struct{}
	eventStopOnce         sync.Once
	eventDone             chan struct{}
	eventSubmitMu         sync.Mutex
	eventBatchMu          sync.Mutex
	eventTerminalMu       sync.Mutex
	eventTerminal         *Event
	stateMu               sync.RWMutex
	syncMu                sync.Mutex
	deathWireMu           sync.Mutex
	lifecycle             playerLifecycle
	position              [3]float32
	keyMask               uint32
	afk                   bool
	vehicleID             uint16
	inVehicle             bool
	passenger             bool
	vehicleSeat           uint8
	health                float32
	armour                float32
	vehicleHealthKnown    bool
	respawnHealth         float32
	respawnArmour         float32
	respawnHealthKnown    bool
	localID               uint16
	skin                  int32
	team                  uint8
	rotation              float32
	drunkLevel            uint32
	drunkLevelSet         bool
	initObserved          bool
	emulatePCClientCheck  bool
	respawnPolicy         RespawnPolicy
	clientCheckStart      time.Time
	vehicles              map[uint16]VehicleEvent
	motionMu              sync.Mutex
	motion                *motionTask
	nextMotionID          uint64
	onFootVelocity        [3]float32
	onFootQuaternion      [4]float32
	onFootLRAnalog        uint16
	onFootUDAnalog        uint16
	onFootProtocolKeys    uint16
	onFootSpecialAction   uint8
	onFootWeapon          uint8
	onFootAnimationID     int16
	onFootAnimationFlags  int16
	vehicleVelocity       [3]float32
	vehicleQuaternion     [4]float32
	vehicleLRAnalog       uint16
	vehicleUDAnalog       uint16
	vehicleProtocolKeys   uint16
	vehicleHealth         float32
	enterPending          bool
	nextVehicleEntryID    uint64
	enterPendingID        uint64
	enterPendingVehicle   uint16
	enterPendingPassenger bool
	enterPendingMode      VehicleEntryMode
	enterPendingKnown     bool
	enterPendingLastTick  time.Time
	enterPendingTarget    [3]float32
	enterPendingHasTarget bool
	enterQueued           bool
	enterQueuedVehicle    uint16
	enterQueuedPassenger  bool
	enterQueuedMode       VehicleEntryMode
	enterQueuedKnown      bool
	exitPending           bool
	pendingMu             sync.Mutex
	pendingEvents         []Event
}

func DialClient(ctx context.Context, address, nickname, password, charset string) (*Client, error) {
	return DialClientWithOptions(ctx, address, nickname, password, charset, ClientOptions{RespawnPolicy: RespawnPolicyAutomatic})
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
		respawnPolicy:        options.RespawnPolicy,
		clientCheckStart:     clientCheckStart,
		vehicles:             make(map[uint16]VehicleEvent),
		health:               defaultPlayerHealthValue,
		lifecycle:            newPlayerLifecycle(),
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
	c.initEventDispatcher()
	go c.run()
	go c.syncLoop()
	go c.scoreLoop()
	go c.statsLoop()
	return c, nil
}
func (c *Client) Events() <-chan Event { return c.events }

func (c *Client) initEventDispatcher() {
	if c.events == nil {
		c.events = make(chan Event, 256)
	}
	c.eventQueue = make(chan Event, 256)
	c.eventStop = make(chan struct{})
	c.eventDone = make(chan struct{})
	go c.dispatchEvents()
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		// Keep the client/network loops alive while RakNet performs the same
		// graceful Disconnect(500) sequence as the native SA-MP client. The
		// transport must send ID_DISCONNECTION_NOTIFICATION before the upper
		// layer is cancelled.
		if c.conn != nil {
			_ = c.conn.Close()
		}
		if c.cancel != nil {
			c.cancel()
		}
		// A Client used by a failed handshake or a unit test has no run loop
		// that can perform the final event shutdown.
		if c.eventQueue == nil || c.conn == nil {
			c.stopEventDispatcher(nil)
		}
	})
	return nil
}
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

// RequestSpawn mirrors the Android raksamp spawn request. A class response
// only supplies spawn information; the server must authorize each spawn
// transaction through RPC_RequestSpawn before the client sends RPC_Spawn.
// Keeping that handshake for respawns is more interoperable than assuming the
// server still has an outstanding spawn authorization after death.
func (c *Client) RequestSpawn(ctx context.Context) error {
	var requestID uint64
	var started bool
	c.stateMu.Lock()
	if !c.lifecycle.spawnInfoReady {
		c.stateMu.Unlock()
		return ErrSpawnNotReady
	}
	if c.lifecycle.spawned || c.lifecycle.spawnRequested || c.lifecycle.spawnPhase == spawnPhaseSpawning {
		c.stateMu.Unlock()
		return nil
	}
	if !c.lifecycle.respawnNotBefore.IsZero() && time.Now().Before(c.lifecycle.respawnNotBefore) {
		c.stateMu.Unlock()
		return ErrSpawnCooldown
	}
	_, requestID, started = c.lifecycle.beginSpawnRequest(time.Now())
	if !started {
		c.stateMu.Unlock()
		return nil
	}
	c.stateMu.Unlock()
	c.emitLifeState(PlayerLifeStateSpawnRequestPending)
	request := raknet.Writer{}
	if err := c.sendRPC(ctx, RPCRequestSpawn, &request, raknet.Reliable); err != nil {
		c.rollbackSpawnRequest(requestID)
		return err
	}
	return nil
}
func (c *Client) ClickPlayer(ctx context.Context, playerID uint16) error {
	w := raknet.Writer{}
	w.Uint16(playerID)
	w.Uint8(0)
	// Match RaksampNativeBridge::nativeSendClickPlayer: RPC 23 is sent as
	// RELIABLE on ordering channel 0, not RELIABLE_ORDERED.
	return c.sendRPC(ctx, RPCClickPlayer, &w, raknet.Reliable)
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
	if enabled {
		c.StopMovement()
	}
	c.stateMu.Lock()
	c.afk = enabled
	if enabled {
		c.keyMask = 0
	}
	c.stateMu.Unlock()
}
func (c *Client) Teleport(ctx context.Context, x, y, z float32) error {
	c.StopMovement()
	c.stateMu.Lock()
	c.position = [3]float32{x, y, z}
	c.stateMu.Unlock()
	return c.sendSync(ctx)
}
func (c *Client) EnterVehicle(ctx context.Context, vehicleID uint16, passenger bool, requestedMode VehicleEntryMode) error {
	return c.enterVehicle(ctx, vehicleID, passenger, requestedMode, nil)
}

// enterVehicle is the internal form used by a queued transition. A queued
// target that was known before the exit must remain known until the new entry
// commits; otherwise an asynchronous continuation could enter a vehicle after
// its remove/death RPC was processed.
func (c *Client) enterVehicle(ctx context.Context, vehicleID uint16, passenger bool, requestedMode VehicleEntryMode, queuedKnown *bool) error {
	mode, err := NormalizeVehicleEntryMode(requestedMode)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.stateMu.RLock()
	spawned := c.lifecycle.spawned && !c.lifecycle.deathInProgress
	inVehicle := c.inVehicle
	currentVehicleID := c.vehicleID
	currentPassenger := c.passenger
	pending := c.enterPending
	pendingSame := pending && c.enterPendingVehicle == vehicleID && c.enterPendingPassenger == passenger && c.enterPendingMode == mode
	queued := c.enterQueued
	queuedSame := queued && c.enterQueuedVehicle == vehicleID && c.enterQueuedPassenger == passenger && c.enterQueuedMode == mode
	vehicle, vehicleKnown := c.vehicles[vehicleID]
	position := c.position
	c.stateMu.RUnlock()
	if !spawned {
		return ErrClientNotSpawned
	}
	if inVehicle && currentVehicleID == vehicleID && currentPassenger == passenger {
		return nil
	}
	if pendingSame || queuedSame {
		return nil
	}
	if pending || queued {
		return ErrVehicleEntryInProgress
	}
	entryRequiresKnownVehicle := vehicleKnown
	if mode == VehicleEntryNormal {
		entryRequiresKnownVehicle = true
	}
	if queuedKnown != nil {
		entryRequiresKnownVehicle = *queuedKnown
		if entryRequiresKnownVehicle && (!vehicleKnown || vehicle.Health <= 0) {
			return ErrVehicleEntryCanceled
		}
	}
	if mode == VehicleEntryNormal {
		if !vehicleKnown || vehicle.Health <= 0 || distance3(position, [3]float32{vehicle.X, vehicle.Y, vehicle.Z}) > normalVehicleEntryMaxDistance {
			return ErrVehicleEntryOutOfRange
		}
	}
	if needsVehicleExit(inVehicle, currentVehicleID, currentPassenger, vehicleID, passenger) {
		c.stateMu.Lock()
		c.enterQueued = true
		c.enterQueuedVehicle = vehicleID
		c.enterQueuedPassenger = passenger
		c.enterQueuedMode = mode
		c.enterQueuedKnown = entryRequiresKnownVehicle
		c.stateMu.Unlock()
		if err := c.exitVehicle(ctx, ctx); err != nil {
			c.stateMu.Lock()
			c.enterQueued = false
			c.enterQueuedKnown = false
			c.enterQueuedMode = ""
			c.stateMu.Unlock()
			return err
		}
		return nil
	}
	// Mark the transition before writing the RPC. syncLoop is concurrent with
	// API calls; it must not emit another on-foot frame between the request and
	// the first vehicle frame. syncMu also preserves the RPC/frame order on the
	// shared RakNet ordering channel.
	c.syncMu.Lock()
	c.stateMu.Lock()
	if !c.lifecycle.spawned || c.lifecycle.deathInProgress {
		c.stateMu.Unlock()
		c.syncMu.Unlock()
		return ErrClientNotSpawned
	}
	if entryRequiresKnownVehicle {
		vehicle, known := c.vehicles[vehicleID]
		if !known || vehicle.Health <= 0 {
			c.stateMu.Unlock()
			c.syncMu.Unlock()
			return ErrVehicleEntryCanceled
		}
	}
	c.nextVehicleEntryID++
	entryID := c.nextVehicleEntryID
	c.enterPending = true
	c.enterPendingID = entryID
	c.enterPendingVehicle = vehicleID
	c.enterPendingPassenger = passenger
	c.enterPendingMode = mode
	c.enterPendingKnown = entryRequiresKnownVehicle
	if mode == VehicleEntryNormal {
		// The regular client starts the entry task while still on foot and turns
		// toward the vehicle. This heading is visible in the on-foot sync frames
		// sent during the entry phase.
		if vehicle, ok := c.vehicles[vehicleID]; ok {
			direction := [3]float32{vehicle.X - c.position[0], vehicle.Y - c.position[1], vehicle.Z - c.position[2]}
			if math.Hypot(float64(direction[0]), float64(direction[1])) >= 0.000001 {
				c.onFootQuaternion = yawQuaternion(yawForDirection(direction))
			}
			// CPlayerPed::EnterVehicle lets GTA walk the ped to the vehicle
			// door. Without a game task, approach a short distance from the
			// streamed vehicle transform and keep publishing on-foot sync.
			horizontalDistance := float32(math.Hypot(float64(direction[0]), float64(direction[1])))
			if horizontalDistance > normalVehicleEntryStandOff {
				inv := 1 / horizontalDistance
				c.enterPendingTarget = [3]float32{
					vehicle.X - direction[0]*inv*normalVehicleEntryStandOff,
					vehicle.Y - direction[1]*inv*normalVehicleEntryStandOff,
					c.position[2],
				}
				c.enterPendingHasTarget = true
			}
		}
	}
	entryDuration := normalVehicleEntryDuration
	if mode == VehicleEntryNormal && c.enterPendingHasTarget {
		approachDistance := distance3(c.position, c.enterPendingTarget)
		entryDuration += time.Duration(float64(approachDistance/normalVehicleEntryApproachSpeed) * float64(time.Second))
		if entryDuration > normalVehicleEntryMaxDuration {
			entryDuration = normalVehicleEntryMaxDuration
		}
	}
	if mode == VehicleEntryNormal {
		c.enterPendingLastTick = time.Now()
	}
	c.stateMu.Unlock()
	w := raknet.Writer{}
	w.Uint16(vehicleID)
	if passenger {
		w.Uint8(1)
	} else {
		w.Uint8(0)
	}
	if err := c.sendRPC(ctx, RPCEnterVehicle, &w, raknet.ReliableSequenced); err != nil {
		c.stateMu.Lock()
		if c.enterPending && c.enterPendingID == entryID && c.enterPendingVehicle == vehicleID && c.enterPendingPassenger == passenger && c.enterPendingMode == mode {
			c.clearPendingVehicleEntryLocked()
		}
		c.stateMu.Unlock()
		c.syncMu.Unlock()
		return err
	}
	if mode == VehicleEntryDirect {
		// RPC_EnterVehicle is the local client's request to begin entering. The
		// server normally does not echo PutPlayerInVehicle back to the requesting
		// player; that RPC is for server-forced placement. Direct mode mirrors the
		// historical headless behavior immediately so the next sync is vehicle or
		// passenger sync.
		seatID := uint8(0)
		if passenger {
			seatID = 1
		}
		if !c.completeVehicleEntry(vehicleID, passenger, mode, entryID, seatID) {
			c.syncMu.Unlock()
			c.stateMu.RLock()
			spawned := c.lifecycle.spawned && !c.lifecycle.deathInProgress
			c.stateMu.RUnlock()
			if !spawned {
				return ErrClientNotSpawned
			}
			return ErrVehicleEntryCanceled
		}
		c.syncMu.Unlock()
		c.emit(Event{Type: EventVehicleState, Data: c.vehicleStateEvent(vehicleID, passenger)})
		return nil
	}
	c.syncMu.Unlock()

	// The real client sends on-foot sync while the GTA enter task is active.
	// Emit one immediately so a caller does not depend on the next 30 ms tick
	// to establish the correct post-RPC packet sequence.
	if err := c.sendSync(ctx); err != nil {
		c.cancelPendingVehicleEntry(entryID, vehicleID, passenger, mode)
		return err
	}

	timer := time.NewTimer(entryDuration)
	defer timer.Stop()
	var clientDone <-chan struct{}
	if c.ctx != nil {
		clientDone = c.ctx.Done()
	}
	select {
	case <-timer.C:
	case <-ctx.Done():
		c.cancelPendingVehicleEntry(entryID, vehicleID, passenger, mode)
		return ctx.Err()
	case <-clientDone:
		c.cancelPendingVehicleEntry(entryID, vehicleID, passenger, mode)
		return context.Canceled
	}

	c.stateMu.RLock()
	completedByServer := c.inVehicle && c.vehicleID == vehicleID && c.passenger == passenger
	entryIDMatches := c.enterPending && c.enterPendingID == entryID && c.enterPendingVehicle == vehicleID && c.enterPendingPassenger == passenger && c.enterPendingMode == mode
	c.stateMu.RUnlock()
	if completedByServer {
		return nil
	}
	if !entryIDMatches {
		return ErrVehicleEntryCanceled
	}
	seatID := uint8(0)
	if passenger {
		seatID = 1
	}
	if !c.completeVehicleEntry(vehicleID, passenger, mode, entryID, seatID) {
		c.stateMu.RLock()
		spawned := c.lifecycle.spawned && !c.lifecycle.deathInProgress
		c.stateMu.RUnlock()
		if !spawned {
			return ErrClientNotSpawned
		}
		return ErrVehicleEntryCanceled
	}
	c.emit(Event{Type: EventVehicleState, Data: c.vehicleStateEvent(vehicleID, passenger)})
	return nil
}

func needsVehicleExit(inVehicle bool, currentVehicleID uint16, currentPassenger bool, targetVehicleID uint16, targetPassenger bool) bool {
	return inVehicle && (currentVehicleID != targetVehicleID || currentPassenger != targetPassenger)
}

func (c *Client) cancelPendingVehicleEntry(entryID uint64, vehicleID uint16, passenger bool, mode VehicleEntryMode) {
	c.stateMu.Lock()
	if c.enterPending && c.enterPendingID == entryID && c.enterPendingVehicle == vehicleID && c.enterPendingPassenger == passenger && c.enterPendingMode == mode {
		c.clearPendingVehicleEntryLocked()
	}
	c.stateMu.Unlock()
}

func (c *Client) ExitVehicle(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.exitVehicle(ctx, nil)
}

func (c *Client) exitVehicle(ctx, queuedContext context.Context) error {
	c.stateMu.Lock()
	vehicleID := c.vehicleID
	inVehicle := c.inVehicle
	pending := c.exitPending
	if !c.lifecycle.spawned || c.lifecycle.deathInProgress {
		c.stateMu.Unlock()
		return ErrClientNotSpawned
	}
	if !inVehicle {
		c.clearPendingVehicleEntryLocked()
		c.exitPending = false
		c.enterQueued = false
		c.enterQueuedKnown = false
		c.enterQueuedMode = ""
		c.stateMu.Unlock()
		return nil
	}
	if pending {
		c.stateMu.Unlock()
		return nil
	}
	c.exitPending = true
	c.stateMu.Unlock()
	c.syncMu.Lock()
	w := raknet.Writer{}
	w.Uint16(vehicleID)
	if err := c.sendRPC(ctx, RPCExitVehicle, &w, raknet.ReliableSequenced); err != nil {
		c.stateMu.Lock()
		c.exitPending = false
		c.stateMu.Unlock()
		c.syncMu.Unlock()
		return err
	}
	// As with entering, the normal client changes its local state when the
	// request is sent. The server's RemoveFromVehicle RPC is reserved for a
	// server-forced exit and is not the normal acknowledgement path.
	c.stateMu.Lock()
	queuedVehicle := c.enterQueuedVehicle
	queuedPassenger := c.enterQueuedPassenger
	queuedMode := c.enterQueuedMode
	queuedKnown := c.enterQueuedKnown
	shouldEnter := c.enterQueued
	c.vehicleID, c.inVehicle, c.passenger, c.vehicleSeat = 0, false, false, 0
	c.clearPendingVehicleEntryLocked()
	c.exitPending = false
	c.enterQueued = false
	c.enterQueuedVehicle = 0
	c.enterQueuedPassenger = false
	c.enterQueuedKnown = false
	c.enterQueuedMode = ""
	c.vehicleVelocity = [3]float32{}
	c.vehicleQuaternion = [4]float32{}
	c.vehicleLRAnalog, c.vehicleUDAnalog, c.vehicleProtocolKeys = 0, 0, 0
	c.vehicleHealth = 0
	c.vehicleHealthKnown = false
	c.stateMu.Unlock()
	c.syncMu.Unlock()
	c.emit(Event{Type: EventVehicleState, Data: VehicleStateEvent{}})
	if shouldEnter && c.ctx != nil {
		if queuedContext == nil {
			queuedContext = c.ctx
		}
		if err := c.enterVehicle(queuedContext, queuedVehicle, queuedPassenger, queuedMode, &queuedKnown); err != nil {
			c.emit(Event{Type: EventProtocolError, Data: err.Error()})
		}
	}
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
			c.expireSpawnRequest(time.Now())
			c.retryDeathNotification(time.Now())
			c.stateMu.RLock()
			shouldSync := c.lifecycle.spawned && !c.lifecycle.deathInProgress && !c.afk
			c.stateMu.RUnlock()
			if shouldSync {
				c.advanceMotion(time.Now())
				c.advanceVehicleEntry(time.Now())
				_ = c.sendSync(c.ctx)
			}
		}
	}
}

// advanceVehicleEntry emulates the movement portion of GTA's enter-vehicle
// task. It only updates the on-foot frame; the caller that started the entry
// owns the timed transition to vehicle/passenger state.
func (c *Client) advanceVehicleEntry(now time.Time) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if !c.enterPending || c.enterPendingMode != VehicleEntryNormal || !c.enterPendingHasTarget {
		return
	}
	if c.enterPendingLastTick.IsZero() {
		c.enterPendingLastTick = now
		return
	}
	dt := float32(now.Sub(c.enterPendingLastTick).Seconds())
	if dt <= 0 {
		return
	}
	if dt > 0.2 {
		dt = 0.2
	}
	c.enterPendingLastTick = now
	next, velocity, reached := moveTowards(c.position, c.enterPendingTarget, normalVehicleEntryApproachSpeed, dt)
	c.position = next
	c.onFootVelocity = syncVelocity(velocity)
	c.onFootUDAnalog = analogWire(analogForward)
	c.onFootLRAnalog = 0
	c.onFootProtocolKeys = 0
	if reached {
		c.onFootVelocity = [3]float32{}
		c.onFootUDAnalog = 0
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
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	c.stateMu.RLock()
	spawned, inVehicle, passenger := c.lifecycle.spawned && !c.lifecycle.deathInProgress, c.inVehicle, c.passenger
	if !inVehicle && c.enterPending && c.enterPendingMode == VehicleEntryDirect {
		// A normal client changes its local GTA state as soon as the enter
		// action starts. Keep this compatibility path for the tiny interval
		// between sending the direct-mode RPC and mirroring its local state.
		inVehicle = true
		passenger = c.enterPendingPassenger
	}
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
	frame := onFootFrame{
		lrAnalog: c.onFootLRAnalog, udAnalog: c.onFootUDAnalog,
		keys:     c.onFootProtocolKeys | protocolKeys(c.keyMask),
		position: c.position, quaternion: c.onFootQuaternion,
		health: syncHealthByte(c.health), armour: syncHealthByte(c.armour), weapon: normalizeSyncWeapon(c.onFootWeapon) | (protocolAdditionalKey(c.keyMask)&0x03)<<6,
		specialAction: c.onFootSpecialAction, velocity: c.onFootVelocity,
		animationID: c.onFootAnimationID, animationFlags: c.onFootAnimationFlags,
	}
	c.stateMu.RUnlock()
	payload := encodeOnFootFrame(frame)
	return c.conn.WriteChannel(ctx, payload, raknet.UnreliableSequenced, playerSyncChannel)
}
func (c *Client) sendVehicle(ctx context.Context, passenger bool) error {
	c.stateMu.RLock()
	vehicleID, position, mask := c.vehicleID, c.position, c.keyMask
	vehicleQuaternion := c.vehicleQuaternion
	vehicleSeat := c.vehicleSeat
	pendingEntry := !c.inVehicle && c.enterPending && c.enterPendingMode == VehicleEntryDirect
	if pendingEntry {
		vehicleID = c.enterPendingVehicle
		if passenger && vehicleSeat == 0 {
			vehicleSeat = 1
		}
	}
	vehicle, vehicleKnown := c.vehicles[vehicleID]
	if vehicleSeat == 0 {
		vehicleSeat = 1
	}
	if pendingEntry {
		// A pending entry has not received the server's seat/transform RPC
		// yet. The streamed vehicle is the closest equivalent to the local
		// game's vehicle transform used by the normal client.
		if vehicleKnown {
			position = [3]float32{vehicle.X, vehicle.Y, vehicle.Z}
			vehicleQuaternion = yawQuaternion(vehicle.Angle)
		}
	}
	vehicleHealth := c.vehicleHealth
	if vehicleHealth == 0 && !vehicleKnown {
		// A direct entry can start before the vehicle has been streamed. Use the
		// GTA default only when there is no authoritative vehicle health yet;
		// zero from a health RPC/death update must remain zero on the wire.
		vehicleHealth = 1000
	}
	vehicleFrame := vehicleFrame{
		vehicleID: vehicleID, lrAnalog: c.vehicleLRAnalog, udAnalog: c.vehicleUDAnalog,
		keys: c.vehicleProtocolKeys | protocolVehicleKeys(mask), quaternion: vehicleQuaternion,
		position: position, velocity: c.vehicleVelocity, vehicleHealth: vehicleHealth,
		playerHealth: syncHealthByte(c.health), playerArmour: syncHealthByte(c.armour), landingGear: 1,
	}
	passengerFrame := passengerFrame{
		vehicleID: vehicleID, seatID: vehicleSeat, playerHealth: syncHealthByte(c.health), playerArmour: syncHealthByte(c.armour),
		additionalKey: protocolAdditionalKey(mask) & 0x03, weapon: normalizeSyncWeapon(c.onFootWeapon),
		lrAnalog: c.vehicleLRAnalog, udAnalog: c.vehicleUDAnalog,
		keys: c.vehicleProtocolKeys | protocolVehicleKeys(mask), position: position,
	}
	c.stateMu.RUnlock()
	if passenger {
		return c.conn.WriteChannel(ctx, encodePassengerFrame(passengerFrame), raknet.UnreliableSequenced, playerSyncChannel)
	}
	return c.conn.WriteChannel(ctx, encodeVehicleFrame(vehicleFrame, protocolAdditionalKey(mask)), raknet.UnreliableSequenced, 0)
}
func encodeOnFoot(position [3]float32, mask uint32) []byte {
	return encodeOnFootFrame(onFootFrame{position: position, keys: protocolKeys(mask), health: defaultPlayerHealth, weapon: protocolAdditionalKey(mask) << 6})
}

func encodeOnFootFrame(frame onFootFrame) []byte {
	w := raknet.Writer{}
	w.Uint8(packetPlayerSync)
	w.Uint16(frame.lrAnalog)
	w.Uint16(frame.udAnalog)
	w.Uint16(frame.keys)
	for _, value := range frame.position {
		w.Float32(value)
	}
	for _, value := range frame.quaternion {
		w.Float32(value)
	}
	w.Uint8(frame.health)
	w.Uint8(frame.armour)
	w.Uint8((normalizeSyncWeapon(frame.weapon) & 0x3f) | (frame.weapon & 0xc0))
	w.Uint8(frame.specialAction)
	for _, value := range frame.velocity {
		w.Float32(value)
	}
	for _, value := range frame.surfingOffsets {
		w.Float32(value)
	}
	w.Uint16(frame.surfingVehicleID)
	w.Uint16(uint16(frame.animationID))
	w.Uint16(uint16(frame.animationFlags))
	return w.Bytes()
}

func encodeVehicleFrame(frame vehicleFrame, additionalKey uint8) []byte {
	w := raknet.Writer{}
	w.Uint8(packetVehicleSync)
	w.Uint16(frame.vehicleID)
	w.Uint16(frame.lrAnalog)
	w.Uint16(frame.udAnalog)
	w.Uint16(frame.keys)
	for _, value := range frame.quaternion {
		w.Float32(value)
	}
	for _, value := range frame.position {
		w.Float32(value)
	}
	for _, value := range frame.velocity {
		w.Float32(value)
	}
	w.Float32(frame.vehicleHealth)
	w.Uint8(frame.playerHealth)
	w.Uint8(frame.playerArmour)
	w.Uint8(normalizeSyncWeapon(frame.weapon) | (additionalKey&0x03)<<6)
	w.Uint8(frame.siren)
	w.Uint8(frame.landingGear)
	w.Uint16(frame.trailerID)
	w.Float32(frame.trainSpeed)
	return w.Bytes()
}

func encodePassengerFrame(frame passengerFrame) []byte {
	w := raknet.Writer{}
	w.Uint8(packetPassengerSync)
	w.Uint16(frame.vehicleID)
	writeBitField(&w, frame.driveBy, 2)
	writeBitField(&w, frame.seatID, 6)
	writeBitField(&w, frame.additionalKey&0x03, 2)
	writeBitField(&w, normalizeSyncWeapon(frame.weapon), 6)
	w.Uint8(frame.playerHealth)
	w.Uint8(frame.playerArmour)
	w.Uint16(frame.lrAnalog)
	w.Uint16(frame.udAnalog)
	w.Uint16(frame.keys)
	for _, value := range frame.position {
		w.Float32(value)
	}
	return w.Bytes()
}

func writeBitField(w *raknet.Writer, value uint8, bits int) {
	w.Bits([]byte{value}, bits, true)
}

func normalizeSyncWeapon(weapon uint8) uint8 {
	if weapon <= maxSyncWeaponID {
		return weapon
	}
	return 0
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
	if c.rpcSender != nil {
		return c.rpcSender(ctx, id, w.Bytes(), w.LenBits(), reliability)
	}
	if c.conn == nil {
		return ErrClientNotConnected
	}
	return c.conn.Write(ctx, raknet.EncodeRPC(id, w.Bytes(), w.LenBits()), reliability)
}
func (c *Client) run() {
	for {
		packet, e := c.conn.Read(c.ctx)
		if e != nil {
			// A read-side disconnect must stop the sync, score, stats and
			// lifecycle retry loops as well. The manager will own reconnecting
			// with a fresh Client instance.
			if c.cancel != nil {
				c.cancel()
			}
			// Cancel first so movement cleanup cannot block behind a full event
			// queue. The dispatcher still guarantees delivery of this terminal
			// event before closing Events().
			c.StopMovement()
			c.stopEventDispatcher(&Event{Type: EventDisconnected, Data: e})
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
				c.observeVehicleSync(vehicle)
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
		c.eventBatchMu.Lock()
		var batch []Event
		if event, e := c.decodeRPC(rpc); e != nil {
			batch = append(batch, Event{Type: EventProtocolError, Data: fmt.Sprintf("RPC %d (%d bits): %v", rpc.ID, rpc.PayloadBits, e)})
		} else if event != nil {
			batch = append(batch, *event)
		}
		batch = append(batch, c.drainPendingEvents()...)
		c.emitBatch(batch)
		c.eventBatchMu.Unlock()
		// Start policy-driven spawning only after the complete inbound batch,
		// including death/lifecycle events, has entered the event queue. This
		// preserves FIFO ordering for observers and plugins.
		c.startAutomaticSpawn()
	}
}

func (c *Client) observeVehicleSync(vehicle VehicleEvent) {
	c.stateMu.Lock()
	if c.vehicles == nil {
		c.vehicles = make(map[uint16]VehicleEvent)
	}
	if previous, ok := c.vehicles[vehicle.ID]; ok {
		vehicle.ModelID = previous.ModelID
		vehicle.Angle = previous.Angle
	}
	c.vehicles[vehicle.ID] = vehicle
	if c.inVehicle && c.vehicleID == vehicle.ID {
		c.vehicleHealth = vehicle.Health
		c.vehicleHealthKnown = true
	}
	c.stateMu.Unlock()
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

// GTA's on-foot and in-car sync packets carry health and armour as bytes,
// while the server RPCs use floats. Keep the full float in client state and
// clamp only at the wire boundary, matching the game's HUD/sync range.
func syncHealthByte(value float32) uint8 {
	if value <= 0 || math.IsNaN(float64(value)) {
		return 0
	}
	if value >= 255 || math.IsInf(float64(value), 1) {
		return 255
	}
	return uint8(math.Round(float64(value)))
}

func readHealth(r *raknet.Reader) (float32, error) {
	value, err := r.Float32()
	if err != nil {
		return 0, err
	}
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		return 0, ErrMalformedPacket
	}
	return value, nil
}

func (c *Client) playerHealthEvent() PlayerHealthEvent {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return PlayerHealthEvent{Health: c.health, Armour: c.armour}
}

func (c *Client) setPlayerHealth(health float32) PlayerHealthEvent {
	if health <= 0 {
		health = 0
		var finalDriverFrame bool
		c.syncMu.Lock()
		c.stateMu.Lock()
		c.health = health
		if c.lifecycle.state() == PlayerLifeStateDead {
			c.respawnHealth = 0
			c.respawnArmour = 0
			c.respawnHealthKnown = false
		}
		event := PlayerHealthEvent{Health: c.health, Armour: c.armour}
		shouldDie := c.lifecycle.spawned && !c.lifecycle.deathReported && !c.lifecycle.deathInProgress
		if shouldDie {
			finalDriverFrame = c.conn != nil && c.inVehicle && !c.passenger
			c.lifecycle.deathInProgress = true
		}
		c.stateMu.Unlock()
		c.syncMu.Unlock()
		if shouldDie {
			c.completeDeath(DeathSourceServerHealth, finalDriverFrame, unknownDeathCause())
		}
		return event
	}
	c.stateMu.Lock()
	if c.lifecycle.state() == PlayerLifeStateDead {
		// A late positive health update must not make the public dead state look
		// alive. Keep it as the baseline for the next explicit spawn instead.
		c.respawnHealth = health
		c.respawnArmour = c.armour
		c.respawnHealthKnown = true
		event := PlayerHealthEvent{Health: 0, Armour: c.armour}
		c.stateMu.Unlock()
		return event
	}
	c.health = health
	event := PlayerHealthEvent{Health: c.health, Armour: c.armour}
	c.stateMu.Unlock()
	return event
}

func (c *Client) setPlayerArmour(armour float32) PlayerHealthEvent {
	c.stateMu.Lock()
	c.armour = armour
	if c.lifecycle.state() == PlayerLifeStateDead && c.respawnHealthKnown {
		c.respawnArmour = armour
	}
	event := PlayerHealthEvent{Health: c.health, Armour: c.armour}
	c.stateMu.Unlock()
	return event
}

func (c *Client) setVehicleHealth(vehicleID uint16, health float32) {
	c.stateMu.Lock()
	c.setVehicleHealthLocked(vehicleID, health)
	c.stateMu.Unlock()
}

func (c *Client) setVehicleHealthLocked(vehicleID uint16, health float32) {
	if c.vehicles == nil {
		c.vehicles = make(map[uint16]VehicleEvent)
	}
	vehicle := c.vehicles[vehicleID]
	vehicle.ID = vehicleID
	vehicle.Health = health
	c.vehicles[vehicleID] = vehicle
	if c.inVehicle && c.vehicleID == vehicleID {
		c.vehicleHealth = health
		c.vehicleHealthKnown = true
	}
}

func (c *Client) applyServerVehicleHealth(vehicleID uint16, health float32, _ DeathSource) {
	// Vehicle destruction is a vehicle-state transition, not proof that the
	// local ped died. Detach a local occupant and let the next on-foot frame
	// establish the new state; a native backend can report confirmed ped death
	// through NotifyLocalPlayerDeath.
	var queuedVehicle uint16
	var queuedPassenger bool
	var queuedMode VehicleEntryMode
	var queuedKnown bool
	var shouldEnter bool
	var detached bool
	c.syncMu.Lock()
	c.stateMu.Lock()
	c.setVehicleHealthLocked(vehicleID, health)
	if health <= 0 {
		c.cancelVehicleEntryForVehicleLocked(vehicleID)
		if c.inVehicle && c.vehicleID == vehicleID {
			detached = true
			queuedVehicle, queuedPassenger, queuedMode, queuedKnown, shouldEnter = c.clearVehicleStateLocked()
		}
	}
	c.stateMu.Unlock()
	c.syncMu.Unlock()
	if detached {
		// The vehicle state event is queued after the protocol event returned by
		// decodeRPC, preserving the wire event first while making the local state
		// authoritative for subsequent snapshots.
		c.queuePendingEvent(Event{Type: EventVehicleState, Data: VehicleStateEvent{}})
	}
	c.continueQueuedVehicleEntry(queuedVehicle, queuedPassenger, queuedMode, queuedKnown, shouldEnter)
}

func (c *Client) removeServerVehicle(vehicleID uint16) {
	var queuedVehicle uint16
	var queuedPassenger bool
	var queuedMode VehicleEntryMode
	var queuedKnown bool
	var shouldEnter bool
	var localOccupant bool
	c.syncMu.Lock()
	c.stateMu.Lock()
	localOccupant = c.inVehicle && c.vehicleID == vehicleID
	c.cancelVehicleEntryForVehicleLocked(vehicleID)
	delete(c.vehicles, vehicleID)
	if localOccupant {
		queuedVehicle, queuedPassenger, queuedMode, queuedKnown, shouldEnter = c.clearVehicleStateLocked()
	}
	c.stateMu.Unlock()
	c.syncMu.Unlock()
	if localOccupant {
		c.queuePendingEvent(Event{Type: EventVehicleState, Data: VehicleStateEvent{}})
		c.continueQueuedVehicleEntry(queuedVehicle, queuedPassenger, queuedMode, queuedKnown, shouldEnter)
	}
}

func (c *Client) vehicleStateEvent(vehicleID uint16, passenger bool) VehicleStateEvent {
	c.stateMu.RLock()
	health := float32(0)
	hasHealth := false
	if c.inVehicle && c.vehicleID == vehicleID {
		health = c.vehicleHealth
		hasHealth = c.vehicleHealthKnown
	} else if vehicle, ok := c.vehicles[vehicleID]; ok {
		health = vehicle.Health
		hasHealth = true
	}
	c.stateMu.RUnlock()
	return VehicleStateEvent{InVehicle: true, Passenger: passenger, VehicleID: vehicleID, Health: health, HasHealth: hasHealth}
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
		localPlayerID, e := decodeInitGameLocalPlayerID(r)
		if e != nil {
			return nil, e
		}
		c.StopMovement()
		c.resetGameplayState()
		c.stateMu.Lock()
		c.localID = localPlayerID
		c.stateMu.Unlock()
		c.queueLifeState(PlayerLifeStateClassSelection)
		go c.requestInitialClassFallback()
		health := c.playerHealthEvent()
		return &Event{Type: EventJoined, Data: PlayerEvent{ID: localPlayerID, Health: health.Health, Armour: health.Armour}}, nil
	case RPCRequestClass:
		outcome, e := r.Uint8()
		if e != nil {
			return nil, e
		}
		if outcome == 0 {
			return c.rejectClassSelection(), nil
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
		c.stateMu.Lock()
		spawnApproved := c.lifecycle.beginSpawning(outcome, serverForcedSpawnOutcome)
		c.stateMu.Unlock()
		if spawnApproved {
			spawned, e := c.sendSpawnAndCommit(c.ctx)
			if e != nil {
				return nil, e
			}
			return &Event{Type: EventSpawned, Data: spawned}, nil
		} else if outcome == 0 {
			c.stateMu.Lock()
			state, rejected := c.lifecycle.rejectSpawnRequest()
			c.stateMu.Unlock()
			if !rejected {
				return nil, nil
			}
			return &Event{Type: EventPlayerLifeState, Data: PlayerLifeStateEvent{State: state}}, nil
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
	case RPCSetPlayerHealth:
		health, e := readHealth(r)
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventPlayerHealth, Data: c.setPlayerHealth(health)}, nil
	case RPCSetPlayerArmour:
		armour, e := readHealth(r)
		if e != nil {
			return nil, e
		}
		return &Event{Type: EventPlayerHealth, Data: c.setPlayerArmour(armour)}, nil
	case RPCSetPlayerPos, RPCSetPlayerPosFindZ:
		c.observeServerInitialization()
		c.StopMovement()
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
		if !c.setVehicleState(vehicleID, seatID) {
			return nil, nil
		}
		return &Event{Type: EventVehicleState, Data: c.vehicleStateEvent(vehicleID, passenger)}, nil
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
		c.stateMu.Lock()
		if c.vehicles == nil {
			c.vehicles = make(map[uint16]VehicleEvent)
		}
		c.vehicles[v.ID] = v
		if c.inVehicle && c.vehicleID == v.ID {
			c.vehicleHealth = v.Health
			c.vehicleHealthKnown = true
		}
		c.stateMu.Unlock()
		return &Event{Type: EventVehicleAdd, Data: v}, nil
	case RPCWorldVehicleRemove:
		id, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		c.removeServerVehicle(id)
		return &Event{Type: EventVehicleRemove, Data: VehicleEvent{ID: id}}, nil
	case RPCSetVehicleHealth:
		vehicleID, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		health, e := readHealth(r)
		if e != nil {
			return nil, e
		}
		c.applyServerVehicleHealth(vehicleID, health, DeathSourceVehicle)
		return &Event{Type: EventVehicleHealth, Data: VehicleHealthEvent{ID: vehicleID, Health: health}}, nil
	case RPCVehicleDeath:
		vehicleID, e := r.Uint16()
		if e != nil {
			return nil, e
		}
		c.applyServerVehicleHealth(vehicleID, 0, DeathSourceVehicle)
		return &Event{Type: EventVehicleHealth, Data: VehicleHealthEvent{ID: vehicleID, Health: 0}}, nil
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
	c.vehicleSeat = 0
	c.clearPendingVehicleEntryLocked()
	c.exitPending = false
	c.enterQueued = false
	c.enterQueuedVehicle = 0
	c.enterQueuedPassenger = false
	c.enterQueuedKnown = false
	c.enterQueuedMode = ""
	c.vehicleHealth = 0
	c.vehicleHealthKnown = false
	c.vehicleVelocity = [3]float32{}
	c.vehicleQuaternion = [4]float32{}
	c.vehicleLRAnalog, c.vehicleUDAnalog, c.vehicleProtocolKeys = 0, 0, 0
	c.clearMotionFrameLocked()
	c.stateMu.Unlock()
}

func (c *Client) setVehicleState(vehicleID uint16, seatID uint8) bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.setVehicleStateLocked(vehicleID, seatID)
}

func (c *Client) setVehicleStateLocked(vehicleID uint16, seatID uint8) bool {
	if !c.lifecycle.spawned || c.lifecycle.deathInProgress {
		return false
	}
	c.vehicleID, c.inVehicle, c.passenger, c.vehicleSeat = vehicleID, true, seatID != 0, seatID
	c.clearPendingVehicleEntryLocked()
	c.exitPending = false
	c.enterQueued = false
	c.enterQueuedKnown = false
	c.enterQueuedMode = ""
	c.vehicleHealth = 0
	c.vehicleHealthKnown = false
	c.vehicleQuaternion = [4]float32{}
	if vehicle, ok := c.vehicles[vehicleID]; ok {
		// PutPlayerInVehicle does not carry a position. The normal game client
		// obtains it from the streamed vehicle before producing its first
		// in-car sync, so use the same authoritative vehicle transform here.
		c.position = [3]float32{vehicle.X, vehicle.Y, vehicle.Z}
		c.vehicleHealth = vehicle.Health
		c.vehicleHealthKnown = true
		c.vehicleQuaternion = yawQuaternion(vehicle.Angle)
	}
	return true
}

func (c *Client) completeVehicleEntry(vehicleID uint16, passenger bool, mode VehicleEntryMode, entryID uint64, seatID uint8) bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if !c.lifecycle.spawned || c.lifecycle.deathInProgress || !c.enterPending || c.enterPendingID != entryID || c.enterPendingVehicle != vehicleID || c.enterPendingPassenger != passenger || c.enterPendingMode != mode {
		return false
	}
	if mode == VehicleEntryNormal || c.enterPendingKnown {
		vehicle, ok := c.vehicles[vehicleID]
		if !ok || vehicle.Health <= 0 {
			c.clearPendingVehicleEntryLocked()
			return false
		}
	}
	return c.setVehicleStateLocked(vehicleID, seatID)
}

func (c *Client) clearPendingVehicleEntryLocked() {
	c.enterPending = false
	c.enterPendingID = 0
	c.enterPendingVehicle = 0
	c.enterPendingPassenger = false
	c.enterPendingMode = ""
	c.enterPendingKnown = false
	c.enterPendingLastTick = time.Time{}
	c.enterPendingTarget = [3]float32{}
	c.enterPendingHasTarget = false
}

func (c *Client) cancelVehicleEntryForVehicleLocked(vehicleID uint16) bool {
	cancelled := false
	if c.enterPending && c.enterPendingVehicle == vehicleID {
		c.clearPendingVehicleEntryLocked()
		cancelled = true
	}
	if c.enterQueued && c.enterQueuedVehicle == vehicleID {
		c.enterQueued = false
		c.enterQueuedVehicle = 0
		c.enterQueuedPassenger = false
		c.enterQueuedKnown = false
		c.enterQueuedMode = ""
		cancelled = true
	}
	return cancelled
}

func (c *Client) clearVehicleState() {
	var queuedVehicle uint16
	var queuedPassenger bool
	var queuedMode VehicleEntryMode
	var queuedKnown bool
	var shouldEnter bool
	c.stateMu.Lock()
	queuedVehicle, queuedPassenger, queuedMode, queuedKnown, shouldEnter = c.clearVehicleStateLocked()
	c.stateMu.Unlock()
	c.continueQueuedVehicleEntry(queuedVehicle, queuedPassenger, queuedMode, queuedKnown, shouldEnter)
}

func (c *Client) clearVehicleStateLocked() (queuedVehicle uint16, queuedPassenger bool, queuedMode VehicleEntryMode, queuedKnown bool, shouldEnter bool) {
	if c.enterQueued {
		queuedVehicle = c.enterQueuedVehicle
		queuedPassenger = c.enterQueuedPassenger
		queuedMode = c.enterQueuedMode
		queuedKnown = c.enterQueuedKnown
		shouldEnter = true
	}
	c.vehicleID, c.inVehicle, c.passenger, c.vehicleSeat = 0, false, false, 0
	c.clearPendingVehicleEntryLocked()
	c.exitPending = false
	c.enterQueued = false
	c.enterQueuedVehicle = 0
	c.enterQueuedPassenger = false
	c.enterQueuedKnown = false
	c.enterQueuedMode = ""
	c.vehicleVelocity = [3]float32{}
	c.vehicleQuaternion = [4]float32{}
	c.vehicleLRAnalog, c.vehicleUDAnalog, c.vehicleProtocolKeys = 0, 0, 0
	c.vehicleHealth = 0
	c.vehicleHealthKnown = false
	return queuedVehicle, queuedPassenger, queuedMode, queuedKnown, shouldEnter
}

func (c *Client) continueQueuedVehicleEntry(queuedVehicle uint16, queuedPassenger bool, queuedMode VehicleEntryMode, queuedKnown bool, shouldEnter bool) {
	if !shouldEnter || c.ctx == nil {
		return
	}
	ctx := c.ctx
	go func() {
		if err := c.enterVehicle(ctx, queuedVehicle, queuedPassenger, queuedMode, &queuedKnown); err != nil {
			c.emit(Event{Type: EventProtocolError, Data: err.Error()})
		}
	}()
}

func (c *Client) setSpawnInfo(info SpawnInfo) {
	c.stateMu.Lock()
	c.position, c.skin, c.team, c.rotation = info.Position, info.Skin, info.Team, info.Rotation
	c.lifecycle.spawnInfoReady = true
	changed := false
	if !c.lifecycle.spawned && c.lifecycle.state() != PlayerLifeStateDead && c.lifecycle.spawnPhase == spawnPhaseIdle {
		changed = c.lifecycle.transition(PlayerLifeStateSpawnReady)
	}
	c.stateMu.Unlock()
	if changed {
		c.queueLifeState(PlayerLifeStateSpawnReady)
	}
}

func (c *Client) markSpawned() SpawnedEvent {
	c.deathWireMu.Lock()
	defer c.deathWireMu.Unlock()
	return c.markSpawnedLocked()
}

func (c *Client) sendSpawnAndCommit(ctx context.Context) (SpawnedEvent, error) {
	c.deathWireMu.Lock()
	defer c.deathWireMu.Unlock()
	spawn := raknet.Writer{}
	if err := c.sendRPC(ctx, RPCSpawn, &spawn, raknet.ReliableOrdered); err != nil {
		c.rollbackSpawning()
		return SpawnedEvent{}, err
	}
	return c.markSpawnedLocked(), nil
}

func (c *Client) markSpawnedLocked() SpawnedEvent {
	c.stateMu.Lock()
	// SpawnInfo carries team, skin, transform, weapons and ammunition, but no
	// health or armour. A dead local client needs a default baseline for its
	// first sync; preserve a positive server-provided value if it arrived before
	// the spawn acknowledgement, and let later health RPCs remain authoritative.
	if c.respawnHealthKnown {
		c.health = c.respawnHealth
		c.armour = c.respawnArmour
	} else if c.health <= 0 || math.IsNaN(float64(c.health)) || math.IsInf(float64(c.health), 0) {
		c.health = defaultPlayerHealthValue
		c.armour = 0
	}
	changed := c.lifecycle.transition(PlayerLifeStateSpawned)
	c.lifecycle.invalidateAutomaticSpawn()
	c.lifecycle.spawnRequested = false
	c.lifecycle.spawnRequestOrigin = PlayerLifeStateSpawnReady
	c.lifecycle.spawnPhase = spawnPhaseIdle
	c.lifecycle.spawnRequestAt = time.Time{}
	c.lifecycle.deathReported = false
	c.lifecycle.deathInProgress = false
	c.lifecycle.respawnNotBefore = time.Time{}
	c.lifecycle.clearDeathReportRetry()
	c.respawnHealth = 0
	c.respawnArmour = 0
	c.respawnHealthKnown = false
	// A spawn is a new gameplay epoch. Clear every transient network control
	// field in one place so stale vehicle/task state cannot leak across death.
	c.inVehicle = false
	c.passenger = false
	c.vehicleID = 0
	c.vehicleSeat = 0
	c.clearPendingVehicleEntryLocked()
	c.enterQueued = false
	c.enterQueuedVehicle = 0
	c.enterQueuedPassenger = false
	c.enterQueuedKnown = false
	c.enterQueuedMode = ""
	c.exitPending = false
	c.keyMask = 0
	c.vehicleHealth = 0
	c.vehicleHealthKnown = false
	c.vehicleVelocity = [3]float32{}
	c.vehicleQuaternion = [4]float32{}
	c.vehicleLRAnalog = 0
	c.vehicleUDAnalog = 0
	c.vehicleProtocolKeys = 0
	c.clearMotionFrameLocked()
	event := SpawnedEvent{Health: c.health, Armour: c.armour}
	c.stateMu.Unlock()
	if changed {
		c.queueLifeState(PlayerLifeStateSpawned)
	}
	return event
}

func (c *Client) resetGameplayState() {
	c.syncMu.Lock()
	c.deathWireMu.Lock()
	c.stateMu.Lock()
	c.lifecycle.resetForConnection()
	c.position = [3]float32{}
	c.keyMask = 0
	c.afk = false
	c.vehicleID = 0
	c.inVehicle = false
	c.passenger = false
	c.vehicleSeat = 0
	c.health = defaultPlayerHealthValue
	c.armour = 0
	c.respawnHealth = 0
	c.respawnArmour = 0
	c.respawnHealthKnown = false
	c.localID = 0
	c.skin = 0
	c.team = 0
	c.rotation = 0
	c.drunkLevel = 0
	c.drunkLevelSet = false
	c.initObserved = false
	c.vehicleHealthKnown = false
	c.clearPendingVehicleEntryLocked()
	c.enterQueued = false
	c.enterQueuedVehicle = 0
	c.enterQueuedPassenger = false
	c.enterQueuedKnown = false
	c.enterQueuedMode = ""
	c.exitPending = false
	c.vehicleHealth = 0
	c.vehicleVelocity = [3]float32{}
	c.vehicleQuaternion = [4]float32{}
	c.vehicleLRAnalog = 0
	c.vehicleUDAnalog = 0
	c.vehicleProtocolKeys = 0
	c.clearMotionFrameLocked()
	if c.vehicles == nil {
		c.vehicles = make(map[uint16]VehicleEvent)
	} else {
		clear(c.vehicles)
	}
	c.stateMu.Unlock()
	c.pendingMu.Lock()
	c.pendingEvents = nil
	c.pendingMu.Unlock()
	c.deathWireMu.Unlock()
	c.syncMu.Unlock()
}

func (c *Client) observeServerInitialization() {
	c.stateMu.Lock()
	c.initObserved = true
	c.stateMu.Unlock()
}

func (c *Client) shouldRequestInitialClass() bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return !c.lifecycle.spawned && !c.initObserved
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
	if v.Angle, e = r.Float32(); e != nil {
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
	c.emitBatch([]Event{e})
}

func (c *Client) emitBatch(batch []Event) {
	if len(batch) == 0 {
		return
	}
	if c.eventQueue == nil {
		for _, event := range batch {
			c.emitDirect(event)
		}
		return
	}

	c.eventSubmitMu.Lock()
	defer c.eventSubmitMu.Unlock()
	for _, event := range batch {
		select {
		case c.eventQueue <- event:
		case <-c.eventStop:
			return
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) emitDirect(e Event) {
	if c.events == nil || c.ctx == nil {
		return
	}
	c.eventsMu.RLock()
	defer c.eventsMu.RUnlock()
	if c.eventsClosed {
		return
	}
	select {
	case c.events <- e:
	case <-c.ctx.Done():
	}
}

func (c *Client) dispatchEvents() {
	defer close(c.eventDone)
	for {
		select {
		case <-c.eventStop:
			c.finishEventDispatch()
			return
		case event := <-c.eventQueue:
			if !c.deliverEvent(event) {
				c.finishEventDispatch()
				return
			}
		}
	}
}

func (c *Client) deliverEvent(event Event) bool {
	c.eventsMu.RLock()
	defer c.eventsMu.RUnlock()
	if c.eventsClosed || c.events == nil {
		return false
	}
	select {
	case c.events <- event:
		return true
	case <-c.eventStop:
		return false
	}
}

func (c *Client) stopEventDispatcher(terminal *Event) {
	if c.eventQueue == nil {
		c.closeEvents()
		return
	}
	if terminal != nil {
		c.eventTerminalMu.Lock()
		if c.eventTerminal == nil {
			copy := *terminal
			c.eventTerminal = &copy
		}
		c.eventTerminalMu.Unlock()
	}
	c.eventStopOnce.Do(func() {
		close(c.eventStop)
	})
	// Callers that initiate shutdown need a deterministic hand-off: once this
	// returns, the terminal event has either been delivered or the public
	// channel has been closed after the dispatcher completed its final drain.
	if c.eventDone != nil {
		<-c.eventDone
	}
}

func (c *Client) finishEventDispatch() {
	c.eventTerminalMu.Lock()
	terminal := c.eventTerminal
	c.eventTerminalMu.Unlock()
	if terminal != nil {
		c.deliverTerminal(*terminal)
	}
	c.closeEvents()
}

func (c *Client) deliverTerminal(event Event) {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	if c.eventsClosed || c.events == nil {
		return
	}
	// Terminal delivery must not be blocked by ordinary event backpressure.
	// Evict the oldest buffered event when necessary; after disconnect no
	// queued gameplay event is more important than allowing consumers to
	// observe termination and finish reconnect/cleanup.
	for {
		select {
		case c.events <- event:
			return
		default:
			select {
			case <-c.events:
			default:
				return
			}
		}
	}
}

func (c *Client) closeEvents() {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	if c.eventsClosed || c.events == nil {
		return
	}
	c.eventsClosed = true
	close(c.events)
}
