package samp

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"

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

func TestSyncEncodersNormalizeInvalidWeapons(t *testing.T) {
	onFoot := encodeOnFootFrame(onFootFrame{weapon: 0xff})
	if got := onFoot[testWeaponOffset]; got != 0xc0 {
		t.Fatalf("on-foot weapon byte = %#x, want additional keys preserved with unarmed", got)
	}

	vehicle := encodeVehicleFrame(vehicleFrame{weapon: 0xff}, 3)
	if got := vehicle[55]; got != 0xc0 {
		t.Fatalf("vehicle weapon byte = %#x, want additional keys preserved with unarmed", got)
	}

	passenger := encodePassengerFrame(passengerFrame{additionalKey: 3, weapon: 0xff})
	if got := passenger[4]; got != 0xc0 {
		t.Fatalf("passenger weapon byte = %#x, want additional keys preserved with unarmed", got)
	}
}

func TestNormalizeSyncWeaponAcceptsProtocolRange(t *testing.T) {
	for _, weapon := range []uint8{0, maxSyncWeaponID} {
		if got := normalizeSyncWeapon(weapon); got != weapon {
			t.Errorf("normalizeSyncWeapon(%d) = %d", weapon, got)
		}
	}
	if got := normalizeSyncWeapon(maxSyncWeaponID + 1); got != 0 {
		t.Fatalf("normalizeSyncWeapon(%d) = %d, want unarmed", maxSyncWeaponID+1, got)
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

func TestDecodeConnectionRejectedReasons(t *testing.T) {
	tests := []struct {
		reason uint8
		want   error
	}{
		{1, ErrBadVersion},
		{2, ErrBadNickname},
		{3, ErrBadMod},
		{4, ErrBadPlayerID},
	}
	for _, test := range tests {
		w := raknet.Writer{}
		w.Uint8(test.reason)
		_, err := (&Client{}).decodeRPC(raknet.RPC{ID: RPCConnectionRejected, Payload: w.Bytes(), PayloadBits: w.LenBits()})
		if !errors.Is(err, test.want) || !errors.Is(err, ErrConnectionRejected) {
			t.Errorf("reason %d error = %v", test.reason, err)
		}
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

func TestZeroHealthTransitionsSpawnedClientToDeadOnce(t *testing.T) {
	c := &Client{
		ctx:       context.Background(),
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		health:    100,
		armour:    25,
	}
	payload := raknet.Writer{}
	payload.Float32(0)

	event, err := c.decodeRPC(raknet.RPC{ID: RPCSetPlayerHealth, Payload: payload.Bytes(), PayloadBits: payload.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != EventPlayerHealth || event.Data.(PlayerHealthEvent).Health != 0 {
		t.Fatalf("health event = %+v", event)
	}
	if c.lifecycle.spawned || c.lifecycle.state() != PlayerLifeStateDead || !c.lifecycle.deathReported {
		t.Fatalf("death state = spawned:%v lifeState:%q reported:%v", c.lifecycle.spawned, c.lifecycle.state(), c.lifecycle.deathReported)
	}
	pending := c.drainPendingEvents()
	if len(pending) != 2 || pending[0].Type != EventPlayerLifeState || pending[1].Type != EventPlayerDeath {
		t.Fatalf("pending death events = %+v", pending)
	}
	state := pending[0].Data.(PlayerLifeStateEvent)
	if state.State != PlayerLifeStateDead {
		t.Fatalf("death lifecycle event = %+v", state)
	}
	death := pending[1].Data.(PlayerDeathEvent)
	if death.Reason != UnknownDeathReason || death.KillerID != InvalidSAMPPlayerID || death.ReasonKnown || death.Source != DeathSourceServerHealth {
		t.Fatalf("death event = %+v", death)
	}

	payload = raknet.Writer{}
	payload.Float32(0)
	if _, err = c.decodeRPC(raknet.RPC{ID: RPCSetPlayerHealth, Payload: payload.Bytes(), PayloadBits: payload.LenBits()}); err != nil {
		t.Fatal(err)
	}
	if pending = c.drainPendingEvents(); len(pending) != 0 {
		t.Fatalf("repeated zero health emitted events = %+v", pending)
	}
}

func TestPositiveHealthDoesNotReviveDeadClient(t *testing.T) {
	c := &Client{lifecycle: playerLifecycle{lifeState: PlayerLifeStateDead}, health: 0}
	if event := c.setPlayerHealth(75); event.Health != 0 {
		t.Fatalf("health event = %+v", event)
	}
	if c.lifecycle.spawned || c.lifecycle.state() != PlayerLifeStateDead || c.lifecycle.deathReported || !c.respawnHealthKnown || c.respawnHealth != 75 {
		t.Fatalf("positive health revived client: spawned:%v lifeState:%q reported:%v", c.lifecycle.spawned, c.lifecycle.state(), c.lifecycle.deathReported)
	}
	spawned := c.markSpawned()
	if spawned.Health != 75 || spawned.Armour != 0 {
		t.Fatalf("deferred spawn health = %+v", spawned)
	}
}

func TestVehicleDeathDetachesLocalOccupantWithoutKillingPlayer(t *testing.T) {
	death := raknet.Writer{}
	death.Uint16(7)

	driver := &Client{
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		inVehicle: true, vehicleID: 7, health: 100,
		vehicles: map[uint16]VehicleEvent{7: {ID: 7, Health: 1000}},
	}
	event, err := driver.decodeRPC(raknet.RPC{ID: RPCVehicleDeath, Payload: death.Bytes(), PayloadBits: death.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || driver.lifecycle.state() != PlayerLifeStateSpawned || !driver.lifecycle.spawned || driver.inVehicle {
		t.Fatalf("driver vehicle death state = event:%+v lifecycle:%+v inVehicle:%v", event, driver.lifecycle, driver.inVehicle)
	}
	if driver.vehicles[7].Health != 0 || driver.vehicleHealthKnown {
		t.Fatalf("driver vehicle health state = vehicle:%+v known:%v", driver.vehicles[7], driver.vehicleHealthKnown)
	}
	for _, pending := range driver.drainPendingEvents() {
		if pending.Type == EventPlayerDeath {
			t.Fatal("vehicle death must not emit player death")
		}
	}

	passenger := &Client{
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		inVehicle: true, passenger: true, vehicleID: 7,
		vehicles: map[uint16]VehicleEvent{7: {ID: 7, Health: 1000}},
	}
	if _, err = passenger.decodeRPC(raknet.RPC{ID: RPCVehicleDeath, Payload: death.Bytes(), PayloadBits: death.LenBits()}); err != nil {
		t.Fatal(err)
	}
	if passenger.lifecycle.state() != PlayerLifeStateSpawned || !passenger.lifecycle.spawned || passenger.inVehicle {
		t.Fatalf("passenger was killed by vehicle death: %+v", passenger.lifecycle)
	}
}

func TestVehicleRemovalDetachesLocalDriver(t *testing.T) {
	c := &Client{
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		inVehicle: true, vehicleID: 9, health: 100,
		vehicles: map[uint16]VehicleEvent{9: {ID: 9, ModelID: 441, Health: 1000}},
	}
	payload := raknet.Writer{}
	payload.Uint16(9)
	if _, err := c.decodeRPC(raknet.RPC{ID: RPCWorldVehicleRemove, Payload: payload.Bytes(), PayloadBits: payload.LenBits()}); err != nil {
		t.Fatal(err)
	}
	if c.lifecycle.state() != PlayerLifeStateSpawned || !c.lifecycle.spawned || c.inVehicle || c.vehicleID != 0 {
		t.Fatalf("vehicle removal changed player lifecycle: %+v inVehicle:%v vehicleID:%d", c.lifecycle, c.inVehicle, c.vehicleID)
	}
	pending := c.drainPendingEvents()
	if len(pending) != 1 || pending[0].Type != EventVehicleState || pending[0].Data.(VehicleStateEvent).InVehicle {
		t.Fatalf("vehicle removal events = %+v", pending)
	}
}

func TestLifecycleStateEventsCoverReadyAndSpawned(t *testing.T) {
	c := &Client{lifecycle: newPlayerLifecycle()}
	c.setSpawnInfo(SpawnInfo{Position: [3]float32{1, 2, 3}, Skin: 7})
	spawned := c.drainPendingEvents()
	if len(spawned) != 1 || spawned[0].Type != EventPlayerLifeState || spawned[0].Data.(PlayerLifeStateEvent).State != PlayerLifeStateSpawnReady {
		t.Fatalf("spawn-ready events = %+v", spawned)
	}

	c.markSpawned()
	spawned = c.drainPendingEvents()
	if len(spawned) != 1 || spawned[0].Type != EventPlayerLifeState || spawned[0].Data.(PlayerLifeStateEvent).State != PlayerLifeStateSpawned {
		t.Fatalf("spawned lifecycle events = %+v", spawned)
	}
}

func TestPutPlayerInVehicleIsIgnoredBeforeSpawn(t *testing.T) {
	w := raknet.Writer{}
	w.Uint16(42)
	w.Uint8(0)
	event, err := (&Client{lifecycle: playerLifecycle{lifeState: PlayerLifeStateDead}}).decodeRPC(raknet.RPC{ID: RPCPutPlayerInVehicle, Payload: w.Bytes(), PayloadBits: w.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	if event != nil {
		t.Fatalf("unexpected vehicle event before spawn: %+v", event)
	}
}

func TestEnterVehicleRequiresSpawn(t *testing.T) {
	c := &Client{vehicles: map[uint16]VehicleEvent{42: {ID: 42}}}
	if err := c.EnterVehicle(context.Background(), 42, false, VehicleEntryDirect); !errors.Is(err, ErrClientNotSpawned) {
		t.Fatalf("entry before spawn error = %v, want %v", err, ErrClientNotSpawned)
	}
}

func TestMarkSpawnedClearsPreviousSpawnRequest(t *testing.T) {
	c := &Client{
		lifecycle: playerLifecycle{
			spawnRequested: true, spawnRequestOrigin: PlayerLifeStateDead,
			lifeState: PlayerLifeStateSpawnRequestPending, spawnPhase: spawnPhaseRequesting,
		},
		health: 0,
		armour: 40,
	}
	spawned := c.markSpawned()
	if !c.lifecycle.spawned || c.lifecycle.spawnRequested || c.lifecycle.state() != PlayerLifeStateSpawned || c.lifecycle.deathReported {
		t.Fatalf("spawn state = spawned:%v requested:%v lifeState:%q reported:%v", c.lifecycle.spawned, c.lifecycle.spawnRequested, c.lifecycle.state(), c.lifecycle.deathReported)
	}
	if spawned.Health != defaultPlayerHealthValue || spawned.Armour != 0 {
		t.Fatalf("spawn health = %+v", spawned)
	}
}

func TestDeathNotificationPayloadUsesUnknownKillerSentinel(t *testing.T) {
	payload := encodeDeathNotificationPayload(UnknownDeathReason, InvalidSAMPPlayerID)
	if len(payload) != 3 || payload[0] != UnknownDeathReason || payload[1] != 0xff || payload[2] != 0xff {
		t.Fatalf("death payload = %v", payload)
	}
}

func TestUnknownDeathNotificationDoesNotSendFistReason(t *testing.T) {
	var wirePayload []byte
	c := &Client{
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		rpcSender: func(_ context.Context, id uint8, payload []byte, _ int, reliability raknet.Reliability) error {
			if id == RPCDeath {
				if reliability != raknet.ReliableSequenced {
					t.Fatalf("death notification reliability = %v, want %v", reliability, raknet.ReliableSequenced)
				}
				wirePayload = append([]byte(nil), payload...)
			}
			return nil
		},
	}
	c.markDead(DeathSourceServerHealth)
	if want := encodeDeathNotificationPayload(UnknownDeathReason, InvalidSAMPPlayerID); string(wirePayload) != string(want) {
		t.Fatalf("unknown death notification payload = %v, want %v", wirePayload, want)
	}
}

func TestNativeDeathCauseIsPreservedInEventAndNotification(t *testing.T) {
	var wirePayload []byte
	c := &Client{
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		rpcSender: func(_ context.Context, id uint8, payload []byte, _ int, reliability raknet.Reliability) error {
			if id != RPCDeath {
				t.Fatalf("death notification RPC = %d, want %d", id, RPCDeath)
			}
			if reliability != raknet.ReliableSequenced {
				t.Fatalf("death notification reliability = %v, want %v", reliability, raknet.ReliableSequenced)
			}
			wirePayload = append([]byte(nil), payload...)
			return nil
		},
	}
	c.markDeadWithCause(DeathSourceVehicle, DeathCause{Reason: 24, KillerID: 7, ReasonKnown: true})

	if want := encodeDeathNotificationPayload(24, 7); string(wirePayload) != string(want) {
		t.Fatalf("death notification payload = %v, want %v", wirePayload, want)
	}
	pending := c.drainPendingEvents()
	if len(pending) != 2 {
		t.Fatalf("death events = %+v", pending)
	}
	death := pending[1].Data.(PlayerDeathEvent)
	if death.Reason != 24 || death.KillerID != 7 || !death.ReasonKnown || death.Source != DeathSourceVehicle {
		t.Fatalf("native death event = %+v", death)
	}
}

func TestAutomaticRespawnSendsDirectSpawn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spawnSent := make(chan struct{})
	var rpcIDs []uint8
	c := &Client{
		ctx:           ctx,
		respawnPolicy: RespawnPolicyAutomatic,
		lifecycle: playerLifecycle{
			lifeState:      PlayerLifeStateDead,
			spawnInfoReady: true,
		},
		rpcSender: func(_ context.Context, id uint8, _ []byte, _ int, reliability raknet.Reliability) error {
			rpcIDs = append(rpcIDs, id)
			if id == RPCSpawn {
				if reliability != raknet.ReliableSequenced {
					t.Errorf("spawn reliability = %v, want %v", reliability, raknet.ReliableSequenced)
				}
				close(spawnSent)
			}
			return nil
		},
	}
	c.startAutomaticSpawn()
	startedAt := time.Now()
	select {
	case <-spawnSent:
	case <-time.After(4 * time.Second):
		t.Fatal("automatic respawn did not complete")
	}
	if elapsed := time.Since(startedAt); elapsed < autoRespawnAfterDeathDelay {
		t.Fatalf("automatic respawn started after %s, want at least %s", elapsed, autoRespawnAfterDeathDelay)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		c.stateMu.RLock()
		state, isSpawned := c.lifecycle.state(), c.lifecycle.spawned
		c.stateMu.RUnlock()
		if state == PlayerLifeStateSpawned && isSpawned {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("automatic respawn lifecycle = state:%q spawned:%v", state, isSpawned)
		case <-time.After(time.Millisecond):
		}
	}
	if len(rpcIDs) != 1 || rpcIDs[0] != RPCSpawn {
		t.Fatalf("automatic respawn RPCs = %v, want [%d]", rpcIDs, RPCSpawn)
	}
	c.stateMu.RLock()
	state, isSpawned := c.lifecycle.state(), c.lifecycle.spawned
	c.stateMu.RUnlock()
	if state != PlayerLifeStateSpawned || !isSpawned {
		t.Fatalf("automatic respawn lifecycle = state:%q spawned:%v", state, isSpawned)
	}
}

func TestAutomaticRespawnDoesNotRequestInitialSpawn(t *testing.T) {
	c := &Client{
		ctx:           context.Background(),
		respawnPolicy: RespawnPolicyAutomatic,
		lifecycle: playerLifecycle{
			lifeState:      PlayerLifeStateSpawnReady,
			spawnInfoReady: true,
		},
		rpcSender: func(_ context.Context, id uint8, _ []byte, _ int, _ raknet.Reliability) error {
			if id == RPCRequestSpawn {
				t.Fatal("automatic policy requested an initial spawn")
			}
			return nil
		},
	}

	c.startAutomaticSpawn()

	c.stateMu.RLock()
	running := c.lifecycle.autoRespawnRunning
	state := c.lifecycle.state()
	c.stateMu.RUnlock()
	if running || state != PlayerLifeStateSpawnReady {
		t.Fatalf("initial spawn lifecycle = running:%v state:%q", running, state)
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
	if c.vehicleHealthKnown || c.inVehicle {
		t.Fatal("vehicle death must detach the current vehicle and clear local health state")
	}
}

func TestMarkSpawnedResetsLocalHealth(t *testing.T) {
	c := &Client{health: 0, armour: 55, lifecycle: playerLifecycle{lifeState: PlayerLifeStateClassSelection}}
	event := c.markSpawned()
	if !c.lifecycle.spawned || c.health != defaultPlayerHealthValue || c.armour != 0 {
		t.Fatalf("spawn state = spawned:%v health:%v armour:%v", c.lifecycle.spawned, c.health, c.armour)
	}
	if event.Health != defaultPlayerHealthValue || event.Armour != 0 {
		t.Fatalf("spawn event = %+v", event)
	}

	c = &Client{health: 72.5, armour: 18, lifecycle: playerLifecycle{lifeState: PlayerLifeStateClassSelection}}
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
	event, err := (&Client{lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned}}).decodeRPC(raknet.RPC{ID: RPCPutPlayerInVehicle, Payload: w.Bytes(), PayloadBits: w.LenBits()})
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
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
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
	unknown := &Client{lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned}}
	unknown.setVehicleState(42, 0)
	if state := unknown.vehicleStateEvent(42, false); state.HasHealth {
		t.Fatalf("unknown vehicle state = %+v", state)
	}

	knownZero := &Client{lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned}, vehicles: map[uint16]VehicleEvent{42: {ID: 42, Health: 0}}}
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
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		position:  [3]float32{0, 0, 0},
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

func TestRejectedRequestClassClearsStaleSpawnState(t *testing.T) {
	c := &Client{lifecycle: playerLifecycle{
		lifeState:      PlayerLifeStateSpawnReady,
		spawnInfoReady: true,
	}}
	event, err := c.decodeRPC(raknet.RPC{ID: RPCRequestClass, Payload: []byte{0}, PayloadBits: 8})
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != EventPlayerLifeState || event.Data.(PlayerLifeStateEvent).State != PlayerLifeStateClassSelection {
		t.Fatalf("class rejection event = %+v", event)
	}
	if c.lifecycle.spawnInfoReady || c.lifecycle.state() != PlayerLifeStateClassSelection {
		t.Fatalf("class rejection left stale lifecycle: %+v", c.lifecycle)
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
	if c.lifecycle.spawnRequested {
		t.Fatal("class response automatically requested spawn")
	}
}

func TestRequestSpawnRequiresSpawnInfo(t *testing.T) {
	client := &Client{}
	if err := client.RequestSpawn(context.Background()); !errors.Is(err, ErrSpawnNotReady) {
		t.Fatalf("RequestSpawn error = %v, want %v", err, ErrSpawnNotReady)
	}
}

func TestRequestSpawnRespectsDeathCooldown(t *testing.T) {
	rpcCalls := 0
	c := &Client{
		lifecycle: playerLifecycle{
			lifeState:        PlayerLifeStateDead,
			spawnInfoReady:   true,
			respawnNotBefore: time.Now().Add(time.Second),
		},
		rpcSender: func(context.Context, uint8, []byte, int, raknet.Reliability) error {
			rpcCalls++
			return nil
		},
	}
	if err := c.RequestSpawn(context.Background()); !errors.Is(err, ErrSpawnCooldown) {
		t.Fatalf("RequestSpawn error = %v, want %v", err, ErrSpawnCooldown)
	}
	if rpcCalls != 0 || c.lifecycle.spawnPhase != spawnPhaseIdle || c.lifecycle.state() != PlayerLifeStateDead {
		t.Fatalf("cooldown request changed lifecycle: calls:%d lifecycle:%+v", rpcCalls, c.lifecycle)
	}
}

func TestDeathInvalidatesAutomaticWorkerAndSetsCooldown(t *testing.T) {
	c := &Client{
		lifecycle: playerLifecycle{
			lifeState:          PlayerLifeStateSpawned,
			spawned:            true,
			autoRespawnEpoch:   7,
			autoRespawnRunning: true,
		},
	}
	startedAt := time.Now()
	c.markDead(DeathSourceServerHealth)
	if c.lifecycle.state() != PlayerLifeStateDead || c.lifecycle.autoRespawnEpoch != 8 || c.lifecycle.autoRespawnRunning {
		t.Fatalf("death worker lifecycle = %+v", c.lifecycle)
	}
	if remaining := time.Until(c.lifecycle.respawnNotBefore); remaining < autoRespawnAfterDeathDelay-(time.Since(startedAt)) {
		t.Fatalf("death cooldown = %s, want approximately %s", remaining, autoRespawnAfterDeathDelay)
	}
}

func TestResetGameplayStateInvalidatesPreviousAutomaticWorker(t *testing.T) {
	c := &Client{lifecycle: playerLifecycle{autoRespawnEpoch: 7, autoRespawnRunning: true}}
	c.resetGameplayState()
	if c.lifecycle.autoRespawnEpoch != 8 || c.lifecycle.autoRespawnRunning || c.lifecycle.state() != PlayerLifeStateClassSelection {
		t.Fatalf("reset worker lifecycle = %+v", c.lifecycle)
	}
}

func TestRequestSpawnAfterDeathSendsDirectSpawn(t *testing.T) {
	var rpcIDs []uint8
	c := &Client{
		ctx: context.Background(),
		lifecycle: playerLifecycle{
			lifeState:          PlayerLifeStateDead,
			spawnInfoReady:     true,
			deathReported:      true,
			spawnRequestOrigin: PlayerLifeStateDead,
		},
		rpcSender: func(_ context.Context, id uint8, _ []byte, _ int, reliability raknet.Reliability) error {
			rpcIDs = append(rpcIDs, id)
			if id != RPCSpawn {
				t.Fatalf("respawn RPC = %d, want %d", id, RPCSpawn)
			}
			if reliability != raknet.ReliableSequenced {
				t.Fatalf("respawn reliability = %v, want %v", reliability, raknet.ReliableSequenced)
			}
			return nil
		},
	}
	if err := c.RequestSpawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rpcIDs) != 1 || rpcIDs[0] != RPCSpawn {
		t.Fatalf("respawn RPCs = %v, want [%d]", rpcIDs, RPCSpawn)
	}
	if !c.lifecycle.spawned || c.lifecycle.state() != PlayerLifeStateSpawned || c.lifecycle.spawnPhase != spawnPhaseIdle {
		t.Fatalf("respawn lifecycle = %+v", c.lifecycle)
	}
}

func TestRespawnRequestWriteFailureRestoresDeadState(t *testing.T) {
	wantErr := errors.New("spawn write failed")
	c := &Client{
		ctx: context.Background(),
		lifecycle: playerLifecycle{
			lifeState:      PlayerLifeStateDead,
			spawnInfoReady: true,
			deathReported:  true,
		},
		rpcSender: func(context.Context, uint8, []byte, int, raknet.Reliability) error {
			return wantErr
		},
	}
	if err := c.RequestSpawn(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("direct respawn error = %v, want %v", err, wantErr)
	}
	if c.lifecycle.state() != PlayerLifeStateDead || c.lifecycle.spawned || c.lifecycle.spawnPhase != spawnPhaseIdle {
		t.Fatalf("failed respawn lifecycle = %+v", c.lifecycle)
	}
}

func TestOrdinaryRequestSpawnOutcomeDoesNotForceSpawn(t *testing.T) {
	c := &Client{}
	event, err := c.decodeRPC(raknet.RPC{ID: RPCRequestSpawn, Payload: []byte{1}, PayloadBits: 8})
	if err != nil {
		t.Fatal(err)
	}
	if event != nil || c.lifecycle.spawned {
		t.Fatalf("ordinary outcome forced spawn: event=%+v spawned=%v", event, c.lifecycle.spawned)
	}
}

func TestRejectedRespawnRestoresDeadState(t *testing.T) {
	c := &Client{
		lifecycle: playerLifecycle{
			spawnRequested: true, spawnRequestOrigin: PlayerLifeStateDead,
			lifeState: PlayerLifeStateSpawnRequestPending, spawnPhase: spawnPhaseRequesting,
		},
	}
	event, err := c.decodeRPC(raknet.RPC{ID: RPCRequestSpawn, Payload: []byte{0}, PayloadBits: 8})
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != EventPlayerLifeState || c.lifecycle.spawnRequested || c.lifecycle.state() != PlayerLifeStateDead {
		t.Fatalf("rejected respawn state = event:%+v requested:%v lifeState:%q", event, c.lifecycle.spawnRequested, c.lifecycle.state())
	}
}

func TestSpawnRequestTimeoutRestoresOrigin(t *testing.T) {
	c := &Client{lifecycle: playerLifecycle{
		lifeState:          PlayerLifeStateSpawnRequestPending,
		spawnRequested:     true,
		spawnRequestOrigin: PlayerLifeStateDead,
		spawnPhase:         spawnPhaseRequesting,
		spawnRequestAt:     time.Now().Add(-spawnRequestTimeout - time.Second),
	}}
	c.expireSpawnRequest(time.Now())
	if c.lifecycle.spawnRequested || c.lifecycle.spawnPhase != spawnPhaseIdle || c.lifecycle.state() != PlayerLifeStateDead {
		t.Fatalf("expired spawn request state = %+v", c.lifecycle)
	}
	pending := c.drainPendingEvents()
	if len(pending) != 1 || pending[0].Type != EventPlayerLifeState || pending[0].Data.(PlayerLifeStateEvent).State != PlayerLifeStateDead {
		t.Fatalf("expired spawn request events = %+v", pending)
	}
}

func TestSpawnRPCFailureRestoresOrigin(t *testing.T) {
	wantErr := errors.New("spawn write failed")
	c := &Client{
		ctx: context.Background(),
		lifecycle: playerLifecycle{
			lifeState:          PlayerLifeStateSpawnRequestPending,
			spawnRequested:     true,
			spawnRequestOrigin: PlayerLifeStateDead,
			spawnPhase:         spawnPhaseRequesting,
		},
		rpcSender: func(context.Context, uint8, []byte, int, raknet.Reliability) error {
			return wantErr
		},
	}
	_, err := c.decodeRPC(raknet.RPC{ID: RPCRequestSpawn, Payload: []byte{1}, PayloadBits: 8})
	if !errors.Is(err, wantErr) {
		t.Fatalf("spawn write error = %v, want %v", err, wantErr)
	}
	if c.lifecycle.spawnPhase != spawnPhaseIdle || c.lifecycle.spawnRequested || c.lifecycle.state() != PlayerLifeStateDead {
		t.Fatalf("spawn write failure state = %+v", c.lifecycle)
	}
}

func TestDeathNotificationRetriesAfterWriteFailure(t *testing.T) {
	wantErr := errors.New("death write failed")
	attempts := 0
	c := &Client{
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		rpcSender: func(context.Context, uint8, []byte, int, raknet.Reliability) error {
			attempts++
			if attempts == 1 {
				return wantErr
			}
			return nil
		},
	}
	c.markDead(DeathSourceServerHealth)
	if attempts != 1 || !c.lifecycle.deathReportPending || !c.lifecycle.deathReported {
		t.Fatalf("initial death report state = attempts:%d lifecycle:%+v", attempts, c.lifecycle)
	}
	c.retryDeathNotification(time.Now().Add(2 * deathNotificationRetry))
	if attempts != 2 || c.lifecycle.deathReportPending {
		t.Fatalf("retried death report state = attempts:%d lifecycle:%+v", attempts, c.lifecycle)
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
	c := &Client{
		ctx:       ctx,
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned, spawnRequested: true},
		vehicles:  map[uint16]VehicleEvent{9: {ID: 9}},
		inVehicle: true, vehicleID: 9, vehicleHealthKnown: true,
		keyMask: 7, enterPending: true,
		pendingEvents: []Event{{Type: EventPlayerDeath}},
	}
	event, err := c.decodeRPC(raknet.RPC{ID: RPCInitGame, Payload: w.Bytes(), PayloadBits: w.LenBits()})
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Type != EventJoined || event.Data.(PlayerEvent).ID != 42 {
		t.Fatalf("unexpected init event: %+v", event)
	}
	if c.lifecycle.spawned || c.lifecycle.spawnRequested || c.localID != 42 || c.inVehicle || c.vehicleID != 0 || c.keyMask != 0 || c.enterPending || len(c.vehicles) != 0 {
		t.Fatalf("unexpected init state: %+v", c)
	}
	pending := c.drainPendingEvents()
	if len(pending) != 1 || pending[0].Type != EventPlayerLifeState || pending[0].Data.(PlayerLifeStateEvent).State != PlayerLifeStateClassSelection {
		t.Fatalf("init lifecycle events = %+v", pending)
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
	c = &Client{lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned}}
	if c.shouldRequestInitialClass() {
		t.Fatal("a spawned client must suppress the class fallback")
	}
}

func TestAsyncDeathFlushesEventsWithoutInboundRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{
		ctx:       ctx,
		cancel:    cancel,
		events:    make(chan Event, 8),
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		health:    100,
	}
	c.initEventDispatcher()
	c.NotifyLocalPlayerDeath(DeathSourceVehicle)

	select {
	case event := <-c.events:
		if event.Type != EventPlayerLifeState || event.Data.(PlayerLifeStateEvent).State != PlayerLifeStateDead {
			t.Fatalf("first async death event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async lifecycle event")
	}
	select {
	case event := <-c.events:
		if event.Type != EventPlayerDeath || event.Data.(PlayerDeathEvent).Source != DeathSourceVehicle {
			t.Fatalf("second async death event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async death event")
	}
	c.stopEventDispatcher(nil)
}

func TestTerminalEventSurvivesFullEventBuffer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{ctx: ctx, events: make(chan Event, 1)}
	c.initEventDispatcher()
	c.emit(Event{Type: EventChat, Data: ChatEvent{Text: "stale"}})

	deadline := time.After(time.Second)
	for len(c.events) == 0 {
		select {
		case <-deadline:
			t.Fatal("dispatcher did not fill the event buffer")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	c.stopEventDispatcher(&Event{Type: EventDisconnected, Data: errors.New("closed")})

	deadline = time.After(time.Second)
	for {
		select {
		case event, ok := <-c.events:
			if !ok {
				t.Fatal("event channel closed before terminal event")
			}
			if event.Type == EventDisconnected {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for terminal event")
		}
	}
}

func TestRejectClassSelectionClearsVehicleEntryTransaction(t *testing.T) {
	c := &Client{
		lifecycle:    playerLifecycle{lifeState: PlayerLifeStateSpawnReady, spawnInfoReady: true},
		enterPending: true, enterPendingVehicle: 7, enterPendingMode: VehicleEntryNormal,
		enterQueued: true, enterQueuedVehicle: 9, enterQueuedMode: VehicleEntryDirect,
	}
	if event := c.rejectClassSelection(); event == nil {
		t.Fatal("class selection rejection did not emit a lifecycle event")
	}
	if c.enterPending || c.enterQueued || c.exitPending || c.inVehicle || c.vehicleHealthKnown {
		t.Fatalf("stale vehicle transaction after class rejection: %+v", c)
	}
}

func TestSetOnFootPositionClearsVehicleTransientState(t *testing.T) {
	c := &Client{
		inVehicle: true, vehicleID: 7, vehicleHealth: 450, vehicleHealthKnown: true,
		vehicleVelocity: [3]float32{1, 2, 3}, vehicleQuaternion: [4]float32{1, 0, 0, 0},
		vehicleLRAnalog: 10, vehicleUDAnalog: 20, vehicleProtocolKeys: 30,
	}
	c.setOnFootPosition([3]float32{4, 5, 6})
	if c.inVehicle || c.vehicleID != 0 || c.vehicleHealth != 0 || c.vehicleHealthKnown || c.vehicleVelocity != [3]float32{} || c.vehicleQuaternion != [4]float32{} || c.vehicleLRAnalog != 0 || c.vehicleUDAnalog != 0 || c.vehicleProtocolKeys != 0 {
		t.Fatalf("vehicle transient state after on-foot reset: %+v", c)
	}
}

func TestVehicleDestructionCancelsPendingEntry(t *testing.T) {
	c := &Client{
		lifecycle:           playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		vehicles:            map[uint16]VehicleEvent{7: {ID: 7, Health: 1000}},
		enterPending:        true,
		enterPendingID:      11,
		enterPendingVehicle: 7,
		enterPendingMode:    VehicleEntryNormal,
		enterQueued:         true,
		enterQueuedVehicle:  7,
		enterQueuedMode:     VehicleEntryDirect,
	}
	c.applyServerVehicleHealth(7, 0, DeathSourceVehicle)
	if c.enterPending || c.enterPendingID != 0 || c.enterQueued || c.inVehicle {
		t.Fatalf("destroyed vehicle left entry transaction active: %+v", c)
	}
	if _, ok := c.vehicles[7]; !ok {
		t.Fatal("vehicle health update unexpectedly removed vehicle record")
	}
}

func TestVehicleEntryCommitRejectsStaleTokenOrRemovedVehicle(t *testing.T) {
	c := &Client{
		lifecycle:           playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		vehicles:            map[uint16]VehicleEvent{7: {ID: 7, Health: 1000}},
		enterPending:        true,
		enterPendingID:      11,
		enterPendingVehicle: 7,
		enterPendingMode:    VehicleEntryNormal,
	}
	if c.completeVehicleEntry(7, false, VehicleEntryNormal, 10, 0) {
		t.Fatal("stale vehicle entry token was committed")
	}
	delete(c.vehicles, 7)
	if c.completeVehicleEntry(7, false, VehicleEntryNormal, 11, 0) {
		t.Fatal("removed vehicle was committed")
	}
	if c.inVehicle || c.enterPending {
		t.Fatalf("stale entry state after rejected commit: %+v", c)
	}
}

func TestDirectVehicleEntryDoesNotCommitRemovedKnownVehicle(t *testing.T) {
	c := &Client{
		lifecycle:           playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		vehicles:            map[uint16]VehicleEvent{7: {ID: 7, Health: 1000}},
		enterPending:        true,
		enterPendingID:      11,
		enterPendingVehicle: 7,
		enterPendingMode:    VehicleEntryDirect,
		enterPendingKnown:   true,
	}
	delete(c.vehicles, 7)
	if c.completeVehicleEntry(7, false, VehicleEntryDirect, 11, 0) {
		t.Fatal("direct entry committed a removed known vehicle")
	}
}

func TestQueuedKnownVehicleEntryRejectsRemovedTargetBeforeRPC(t *testing.T) {
	calls := 0
	c := &Client{
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		rpcSender: func(context.Context, uint8, []byte, int, raknet.Reliability) error {
			calls++
			return nil
		},
	}
	known := true
	if err := c.enterVehicle(context.Background(), 7, false, VehicleEntryDirect, &known); !errors.Is(err, ErrVehicleEntryCanceled) {
		t.Fatalf("queued known target error = %v, want %v", err, ErrVehicleEntryCanceled)
	}
	if calls != 0 {
		t.Fatalf("removed queued target still sent an entry RPC: %d", calls)
	}
}

func TestStaleVehicleEntryCancellationCannotClearNewTransaction(t *testing.T) {
	c := &Client{
		enterPending:        true,
		enterPendingID:      2,
		enterPendingVehicle: 7,
		enterPendingMode:    VehicleEntryNormal,
	}
	c.cancelPendingVehicleEntry(1, 7, false, VehicleEntryNormal)
	if !c.enterPending || c.enterPendingID != 2 {
		t.Fatalf("stale cancellation cleared the new entry transaction: %+v", c)
	}
	c.cancelPendingVehicleEntry(2, 7, false, VehicleEntryNormal)
	if c.enterPending || c.enterPendingID != 0 {
		t.Fatalf("matching cancellation left the entry transaction active: %+v", c)
	}
}

func TestQueuedVehicleEntryContinuationDoesNotBlockCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	c := &Client{
		ctx:       ctx,
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		rpcSender: func(context.Context, uint8, []byte, int, raknet.Reliability) error {
			close(started)
			<-release
			return errors.New("cancel queued test")
		},
	}
	startedAt := time.Now()
	c.continueQueuedVehicleEntry(7, false, VehicleEntryDirect, false, true)
	select {
	case <-started:
		if time.Since(startedAt) > 100*time.Millisecond {
			t.Fatal("queued vehicle continuation blocked the caller")
		}
	case <-time.After(time.Second):
		t.Fatal("queued vehicle continuation did not start")
	}
	close(release)
}
