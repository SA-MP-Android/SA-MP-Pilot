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
