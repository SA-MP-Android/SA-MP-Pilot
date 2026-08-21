package samp

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
)

const (
	testPositionOffset   = 7
	testQuaternionOffset = testPositionOffset + 12
	testWeaponOffset     = 37
)

func TestEncodeOnFootLayout(t *testing.T) {
	position := [3]float32{12.5, -4.25, 3}
	mask := uint32((1 << 0) | (1 << 2) | (1 << 3) | (1 << 5) | (1 << 6))
	payload := encodeOnFoot(position, mask)
	if len(payload) != onFootPayloadBytes {
		t.Fatalf("payload length = %d, want %d", len(payload), onFootPayloadBytes)
	}
	if payload[0] != packetPlayerSync {
		t.Fatalf("packet ID = %d", payload[0])
	}
	wantKeys := uint16((1 << 9) | (1 << 1) | (1 << 10) | (1 << 3))
	if got := binary.LittleEndian.Uint16(payload[5:7]); got != wantKeys {
		t.Fatalf("keys = %#x, want %#x", got, wantKeys)
	}
	for index, want := range position {
		offset := testPositionOffset + index*4
		got := math.Float32frombits(binary.LittleEndian.Uint32(payload[offset : offset+4]))
		if got != want {
			t.Fatalf("position[%d] = %v, want %v", index, got, want)
		}
	}
	for index, value := range payload[testQuaternionOffset : testQuaternionOffset+16] {
		if value != 0 {
			t.Fatalf("quaternion byte[%d] = %#x, want zero", index, value)
		}
	}
	if got := payload[testWeaponOffset] >> 6; got != 1 {
		t.Fatalf("additional key = %d, want 1", got)
	}
}

func TestEncodePassengerLayout(t *testing.T) {
	payload := encodePassengerFrame(passengerFrame{
		vehicleID: 42, driveBy: 1, seatID: 2, additionalKey: 3, weapon: 22,
		playerHealth: 100, playerArmour: 0, lrAnalog: 4, udAnalog: 5, keys: 6,
		position: [3]float32{1, 2, 3},
	})
	if len(payload) != 25 {
		t.Fatalf("payload length = %d, want 25", len(payload))
	}
	if payload[0] != packetPassengerSync {
		t.Fatalf("packet ID = %d", payload[0])
	}
	if got := binary.LittleEndian.Uint16(payload[1:3]); got != 42 {
		t.Fatalf("vehicle ID = %d, want 42", got)
	}
	if got := payload[3:5]; got[0] != 0x42 || got[1] != 0xd6 {
		t.Fatalf("packed passenger fields = %#x %#x, want 0x42 0xd6", got[0], got[1])
	}
	if payload[5] != 100 || payload[6] != 0 {
		t.Fatalf("health/armour = %d/%d", payload[5], payload[6])
	}
}

func TestEncodeVehicleLayout(t *testing.T) {
	payload := encodeVehicleFrame(vehicleFrame{
		vehicleID: 42, quaternion: [4]float32{1, 0, 0, 0},
		position: [3]float32{1, 2, 3}, velocity: [3]float32{0.24, 0, 0}, vehicleHealth: 1000,
		playerHealth: 100, landingGear: 1,
	}, 0)
	if payload[0] != packetVehicleSync {
		t.Fatalf("packet ID = %d", payload[0])
	}
	if len(payload) != 64 {
		t.Fatalf("zero-velocity payload length = %d, want 64", len(payload))
	}
	gotVelocity := math.Float32frombits(binary.LittleEndian.Uint32(payload[37:41]))
	if gotVelocity != 0.24 {
		t.Fatalf("move speed = %v, want 0.24", gotVelocity)
	}
}

func TestDecodePlayerSync(t *testing.T) {
	w := raknet.Writer{}
	w.Uint8(packetPlayerSync)
	w.Uint16(42)
	w.Bit(false)
	w.Bit(false)
	w.Uint16(0)
	for _, value := range []float32{1.5, -2, 9.25} {
		w.Float32(value)
	}
	for range 4 {
		w.Bit(false)
	}
	for range 3 {
		w.Uint16(0)
	}
	w.Uint8(0xf7)
	player, err := decodePlayerSync(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if player.ID != 42 || player.X != 1.5 || player.Y != -2 || player.Z != 9.25 {
		t.Fatalf("unexpected player: %+v", player)
	}
	if player.Health != 100 || player.Armour != 49 {
		t.Fatalf("unexpected health/armour: %+v", player)
	}
}

func TestDecodeDialogRPCPayload(t *testing.T) {
	w := raknet.Writer{}
	w.Int16(17)
	w.Uint8(1)
	w.String8("Login")
	w.String8("OK")
	w.String8("Cancel")
	compressedHello := []byte{0x86, 0x30, 0x25, 0x28, 0x40}
	w.Bits(compressedHello, 34, false)
	dialog, err := decodeDialog(raknet.NewReaderBits(w.Bytes(), w.LenBits()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if dialog.ID != 17 || dialog.Style != 1 || dialog.Title != "Login" || dialog.Button1 != "OK" || dialog.Button2 != "Cancel" || dialog.Message != "hello" {
		t.Fatalf("unexpected dialog: %+v", dialog)
	}
}

func TestDispatchDialogRPC(t *testing.T) {
	w := raknet.Writer{}
	w.Int16(23)
	w.Uint8(0)
	w.String8("Notice")
	w.String8("OK")
	w.String8("")
	compressedHello := []byte{0x86, 0x30, 0x25, 0x28, 0x40}
	w.Bits(compressedHello, 34, false)
	rpcPacket := raknet.EncodeRPC(RPCDialogBox, w.Bytes(), w.LenBits())
	rpc, err := raknet.DecodeRPC(rpcPacket)
	if err != nil {
		t.Fatal(err)
	}
	event, err := (&Client{}).decodeRPC(rpc)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != EventDialog || event.Data.(DialogEvent).ID != 23 {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestDispatchTimestampedDialogRPC(t *testing.T) {
	w := raknet.Writer{}
	w.Int16(24)
	w.Uint8(0)
	w.String8("Timestamped Notice")
	w.String8("OK")
	w.String8("")
	compressedHello := []byte{0x86, 0x30, 0x25, 0x28, 0x40}
	w.Bits(compressedHello, 34, false)
	packet := append([]byte{raknet.PacketTimestamp, 1, 2, 3, 4}, raknet.EncodeRPC(RPCDialogBox, w.Bytes(), w.LenBits())...)
	rpc, err := raknet.DecodeRPC(packet)
	if err != nil {
		t.Fatal(err)
	}
	event, err := (&Client{}).decodeRPC(rpc)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != EventDialog || event.Data.(DialogEvent).Title != "Timestamped Notice" {
		t.Fatalf("unexpected timestamped dialog event: %+v", event)
	}
}

func TestClientCheckRejectsTruncatedPayload(t *testing.T) {
	rpc := raknet.RPC{ID: RPCClientCheck, Payload: []byte{clientCheckMemoryType}, PayloadBits: 8}
	client := &Client{emulatePCClientCheck: true}
	if _, err := client.decodeRPC(rpc); err == nil {
		t.Fatal("expected truncated ClientCheck payload to fail")
	}
}

func TestClientCheckIsDisabledByDefault(t *testing.T) {
	w := raknet.Writer{}
	w.Uint8(clientCheckMemoryType)
	w.Uint32(0x12345678)
	client := &Client{}
	if event, err := client.decodeRPC(raknet.RPC{ID: RPCClientCheck, Payload: w.Bytes(), PayloadBits: w.LenBits()}); err != nil || event != nil {
		t.Fatalf("disabled ClientCheck = event %v, error %v; want no response", event, err)
	}
}

func TestSetPlayerDrunkLevelEmulatesSixtyFramesPerSecond(t *testing.T) {
	w := raknet.Writer{}
	w.Uint32(5000)
	c := &Client{}
	event, err := c.decodeRPC(raknet.RPC{ID: RPCSetPlayerDrunkLevel, Payload: w.Bytes(), PayloadBits: w.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	if event != nil {
		t.Fatalf("unexpected event: %+v", event)
	}
	level, active := c.advanceDrunkLevel(targetFramesPerSecond)
	if !active || level != 4940 {
		t.Fatalf("drunk level = %d, active = %v; want 4940, true", level, active)
	}
}

func TestHealthRPCsUpdateLocalStateAndEmitEvents(t *testing.T) {
	c := &Client{health: 100, armour: 0}
	healthPayload := raknet.Writer{}
	healthPayload.Float32(73.5)
	event, err := c.decodeRPC(raknet.RPC{ID: RPCSetPlayerHealth, Payload: healthPayload.Bytes(), PayloadBits: healthPayload.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != EventPlayerHealth {
		t.Fatalf("health event = %+v", event)
	}
	health := event.Data.(PlayerHealthEvent)
	if health.Health != 73.5 || health.Armour != 0 || c.health != 73.5 {
		t.Fatalf("health state = %+v, client health = %v", health, c.health)
	}

	armourPayload := raknet.Writer{}
	armourPayload.Float32(25.25)
	event, err = c.decodeRPC(raknet.RPC{ID: RPCSetPlayerArmour, Payload: armourPayload.Bytes(), PayloadBits: armourPayload.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	armour := event.Data.(PlayerHealthEvent)
	if armour.Health != 73.5 || armour.Armour != 25.25 || c.armour != 25.25 {
		t.Fatalf("armour state = %+v, client armour = %v", armour, c.armour)
	}
}

func TestVehicleHealthRPCsUpdateStreamedAndCurrentVehicle(t *testing.T) {
	c := &Client{
		vehicles:  map[uint16]VehicleEvent{7: {ID: 7, ModelID: 411, X: 1, Health: 1000}},
		inVehicle: true,
		vehicleID: 7,
	}
	payload := raknet.Writer{}
	payload.Uint16(7)
	payload.Float32(642.5)
	event, err := c.decodeRPC(raknet.RPC{ID: RPCSetVehicleHealth, Payload: payload.Bytes(), PayloadBits: payload.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != EventVehicleHealth {
		t.Fatalf("vehicle health event = %+v", event)
	}
	if got := c.vehicles[7].Health; got != 642.5 || c.vehicleHealth != 642.5 {
		t.Fatalf("vehicle health = %v, current health = %v", got, c.vehicleHealth)
	}

	death := raknet.Writer{}
	death.Uint16(7)
	event, err = c.decodeRPC(raknet.RPC{ID: RPCVehicleDeath, Payload: death.Bytes(), PayloadBits: death.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	if event.Data.(VehicleHealthEvent).Health != 0 || c.vehicles[7].Health != 0 || c.vehicleHealth != 0 {
		t.Fatalf("vehicle death did not clear health: event=%+v vehicle=%+v current=%v", event, c.vehicles[7], c.vehicleHealth)
	}
	if !c.vehicleHealthKnown {
		t.Fatal("vehicle death must keep the current vehicle health marked as known")
	}
}

func TestMarkSpawnedResetsLocalHealth(t *testing.T) {
	c := &Client{health: 0, armour: 55, spawned: false}
	event := c.markSpawned()
	if !c.spawned || c.health != defaultPlayerHealthValue || c.armour != 0 {
		t.Fatalf("spawn state = spawned:%v health:%v armour:%v", c.spawned, c.health, c.armour)
	}
	if event.Health != defaultPlayerHealthValue || event.Armour != 0 {
		t.Fatalf("spawn event = %+v", event)
	}

	c = &Client{health: 72.5, armour: 18, spawned: false}
	event = c.markSpawned()
	if event.Health != 72.5 || event.Armour != 18 {
		t.Fatalf("server-provided spawn state was overwritten: %+v", event)
	}
}

func TestSyncHealthByteClampsRPCHealthToWireRange(t *testing.T) {
	for _, test := range []struct {
		value float32
		want  uint8
	}{
		{value: -1, want: 0},
		{value: 73.5, want: 74},
		{value: 255, want: 255},
		{value: 500, want: 255},
	} {
		if got := syncHealthByte(test.value); got != test.want {
			t.Errorf("syncHealthByte(%v) = %d, want %d", test.value, got, test.want)
		}
	}
}

func TestDrunkLevelDoesNotUnderflow(t *testing.T) {
	c := &Client{drunkLevel: 30, drunkLevelSet: true}
	level, active := c.advanceDrunkLevel(targetFramesPerSecond)
	if !active || level != 0 {
		t.Fatalf("drunk level = %d, active = %v; want 0, true", level, active)
	}
}

func TestProtocolAdditionalKeyPriority(t *testing.T) {
	if got := protocolAdditionalKey((1 << 4) | (1 << 1) | 1); got != 3 {
		t.Fatalf("priority result = %d, want 3", got)
	}
	if got := protocolAdditionalKey(1 << 1); got != 2 {
		t.Fatalf("N result = %d, want 2", got)
	}
	if got := protocolAdditionalKey(0); got != 0 {
		t.Fatalf("empty result = %d, want 0", got)
	}
}

func TestDecodeInitGameLocalPlayerID(t *testing.T) {
	w := raknet.Writer{}
	for range 4 {
		w.Bit(false)
	}
	w.Float32(200)
	w.Bit(true)
	w.Float32(70)
	for range 3 {
		w.Bit(false)
	}
	w.Uint32(5)
	w.Uint16(42)
	id, err := decodeInitGameLocalPlayerID(raknet.NewReaderBits(w.Bytes(), w.LenBits()))
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("local player ID = %d, want 42", id)
	}
}

func TestDecodeWorldPlayerAdd(t *testing.T) {
	w := raknet.Writer{}
	w.Uint16(9)
	w.Uint8(1)
	w.Uint32(23)
	w.Float32(10.5)
	w.Float32(-20.25)
	w.Float32(3)
	w.Float32(90)
	w.Uint32(0xFF0000FF)
	player, err := decodeWorldPlayerAdd(raknet.NewReaderBits(w.Bytes(), w.LenBits()))
	if err != nil {
		t.Fatal(err)
	}
	if player.ID != 9 || player.Skin != 23 || player.X != 10.5 || player.Y != -20.25 || player.Z != 3 || player.Rotation != 90 {
		t.Fatalf("unexpected world player: %+v", player)
	}
}

func TestDecodeSpawnInfo(t *testing.T) {
	w := raknet.Writer{}
	w.Uint8(2)
	w.Uint32(100)
	w.Uint8(0)
	w.Float32(1)
	w.Float32(2)
	w.Float32(3)
	w.Float32(180)
	info, err := decodeSpawnInfo(raknet.NewReaderBits(w.Bytes(), w.LenBits()))
	if err != nil {
		t.Fatal(err)
	}
	if info.Team != 2 || info.Skin != 100 || info.Position != [3]float32{1, 2, 3} || info.Rotation != 180 {
		t.Fatalf("spawn info = %+v", info)
	}
}

func TestSetOnFootPositionLeavesVehicle(t *testing.T) {
	c := &Client{inVehicle: true, passenger: true, vehicleID: 25}
	c.setOnFootPosition([3]float32{1, 2, 3})
	if c.inVehicle || c.passenger || c.vehicleID != 0 || c.position != [3]float32{1, 2, 3} {
		t.Fatalf("unexpected state after SetPlayerPos: %+v", c)
	}
}

func TestDecodePutPlayerInVehicle(t *testing.T) {
	w := raknet.Writer{}
	w.Uint16(42)
	w.Uint8(1)
	event, err := (&Client{}).decodeRPC(raknet.RPC{ID: RPCPutPlayerInVehicle, Payload: w.Bytes(), PayloadBits: w.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	state := event.Data.(VehicleStateEvent)
	if !state.InVehicle || !state.Passenger || state.VehicleID != 42 {
		t.Fatalf("vehicle state = %+v", state)
	}
	if state.HasHealth {
		t.Fatal("vehicle state without streamed data must not claim known health")
	}
}

func TestSetVehicleStateUsesStreamedVehicleTransform(t *testing.T) {
	c := &Client{
		vehicles: map[uint16]VehicleEvent{
			42: {ID: 42, X: 10, Y: -4, Z: 3, Angle: 90, Health: 875},
		},
	}
	c.setVehicleState(42, 0)
	if !c.inVehicle || c.passenger || c.vehicleID != 42 {
		t.Fatalf("unexpected driver state: %+v", c)
	}
	if c.position != [3]float32{10, -4, 3} {
		t.Fatalf("position = %v, want streamed vehicle position", c.position)
	}
	if c.vehicleHealth != 875 || c.vehicleQuaternion != yawQuaternion(90) {
		t.Fatalf("vehicle transform = health %v quaternion %v", c.vehicleHealth, c.vehicleQuaternion)
	}
	state := c.vehicleStateEvent(42, false)
	if !state.HasHealth || state.Health != 875 {
		t.Fatalf("vehicle state health = %+v", state)
	}
}

func TestVehicleStateDistinguishesExplicitZeroFromUnknownHealth(t *testing.T) {
	unknown := &Client{}
	unknown.setVehicleState(42, 0)
	if state := unknown.vehicleStateEvent(42, false); state.HasHealth {
		t.Fatalf("unknown vehicle state = %+v", state)
	}

	knownZero := &Client{vehicles: map[uint16]VehicleEvent{42: {ID: 42, Health: 0}}}
	knownZero.setVehicleState(42, 0)
	if state := knownZero.vehicleStateEvent(42, false); !state.HasHealth || state.Health != 0 {
		t.Fatalf("known zero vehicle state = %+v", state)
	}
}

func TestPlayerSyncUsesRaksampOrderingChannel(t *testing.T) {
	if playerSyncChannel != 1 {
		t.Fatalf("player sync ordering channel = %d, want 1", playerSyncChannel)
	}
}

func TestVehicleTransitionRequiresExitBeforeChangingVehicle(t *testing.T) {
	if !needsVehicleExit(true, 10, false, 11, false) {
		t.Fatal("changing vehicles must exit the current vehicle first")
	}
	if !needsVehicleExit(true, 10, false, 10, true) {
		t.Fatal("changing seats must exit the current vehicle first")
	}
	if needsVehicleExit(true, 10, false, 10, false) {
		t.Fatal("re-entering the same seat must be a no-op")
	}
	if needsVehicleExit(false, 0, false, 10, false) {
		t.Fatal("an on-foot player has no vehicle to exit")
	}
}

func TestVehicleEntryModes(t *testing.T) {
	if got, err := NormalizeVehicleEntryMode(""); err != nil || got != VehicleEntryDirect {
		t.Fatalf("empty mode = %q, error = %v; want direct", got, err)
	}
	for _, mode := range []VehicleEntryMode{VehicleEntryDirect, VehicleEntryNormal} {
		if got, err := NormalizeVehicleEntryMode(mode); err != nil || got != mode {
			t.Fatalf("mode %q normalized to %q with error %v", mode, got, err)
		}
	}
	if _, err := NormalizeVehicleEntryMode("instant"); err == nil {
		t.Fatal("unsupported vehicle entry mode unexpectedly succeeded")
	}
}

func TestNormalVehicleEntryRequiresAStreamedNearbyVehicle(t *testing.T) {
	c := &Client{
		position: [3]float32{0, 0, 0},
		vehicles: map[uint16]VehicleEvent{
			42: {ID: 42, X: 20, Y: 0, Z: 0},
		},
	}
	if err := c.EnterVehicle(context.Background(), 42, false, VehicleEntryNormal); !errors.Is(err, ErrVehicleEntryOutOfRange) {
		t.Fatalf("far normal entry error = %v, want %v", err, ErrVehicleEntryOutOfRange)
	}
	if err := c.EnterVehicle(context.Background(), 43, false, VehicleEntryNormal); !errors.Is(err, ErrVehicleEntryOutOfRange) {
		t.Fatalf("unknown normal entry error = %v, want %v", err, ErrVehicleEntryOutOfRange)
	}
}

func TestRejectedRequestClassDoesNotRequireSpawnInfo(t *testing.T) {
	event, err := (&Client{}).decodeRPC(raknet.RPC{ID: RPCRequestClass, Payload: []byte{0}, PayloadBits: 8})
	if err != nil {
		t.Fatal(err)
	}
	if event != nil {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestRequestClassDoesNotAutomaticallyRequestSpawn(t *testing.T) {
	w := raknet.Writer{}
	w.Uint8(1)
	w.Uint8(1)
	w.Uint32(7)
	w.Uint8(0)
	for range 3 {
		w.Float32(0)
	}
	w.Float32(0)
	c := &Client{}
	if _, err := c.decodeRPC(raknet.RPC{ID: RPCRequestClass, Payload: w.Bytes(), PayloadBits: w.LenBits()}); err != nil {
		t.Fatal(err)
	}
	if c.spawnRequested {
		t.Fatal("class response automatically requested spawn")
	}
}

func TestRequestSpawnRequiresSpawnInfo(t *testing.T) {
	client := &Client{}
	if err := client.RequestSpawn(context.Background()); !errors.Is(err, ErrSpawnNotReady) {
		t.Fatalf("RequestSpawn error = %v, want %v", err, ErrSpawnNotReady)
	}
}

func TestOrdinaryRequestSpawnOutcomeDoesNotForceSpawn(t *testing.T) {
	c := &Client{}
	event, err := c.decodeRPC(raknet.RPC{ID: RPCRequestSpawn, Payload: []byte{1}, PayloadBits: 8})
	if err != nil {
		t.Fatal(err)
	}
	if event != nil || c.spawned {
		t.Fatalf("ordinary outcome forced spawn: event=%+v spawned=%v", event, c.spawned)
	}
}

func TestInitGameOnlyInitializesLocalState(t *testing.T) {
	w := raknet.Writer{}
	for range 4 {
		w.Bit(false)
	}
	w.Float32(200)
	w.Bit(true)
	w.Float32(70)
	for range 3 {
		w.Bit(false)
	}
	w.Uint32(5)
	w.Uint16(42)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{ctx: ctx, spawned: true, spawnRequested: true}
	event, err := c.decodeRPC(raknet.RPC{ID: RPCInitGame, Payload: w.Bytes(), PayloadBits: w.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != EventJoined || event.Data.(PlayerEvent).ID != 42 {
		t.Fatalf("unexpected init event: %+v", event)
	}
	if c.spawned || c.spawnRequested || c.localID != 42 {
		t.Fatalf("unexpected init state: %+v", c)
	}
}

func TestInitialClassFallbackOnlyRunsWithoutServerDrivenInitialization(t *testing.T) {
	c := &Client{}
	if !c.shouldRequestInitialClass() {
		t.Fatal("missing server initialization must enable the class fallback")
	}
	c.observeServerInitialization()
	if c.shouldRequestInitialClass() {
		t.Fatal("server-driven initialization must suppress the class fallback")
	}
	c = &Client{spawned: true}
	if c.shouldRequestInitialClass() {
		t.Fatal("a spawned client must suppress the class fallback")
	}
}
