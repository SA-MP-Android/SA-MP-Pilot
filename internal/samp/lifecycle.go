package samp

import (
	"context"
	"fmt"
	"time"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
)

// PlayerLifeState is the protocol-visible lifecycle of the local player.
// Internal transaction phases deliberately remain private: plugins should see
// stable semantic states, while the client can still distinguish a request
// being written from a spawn already being committed.
type PlayerLifeState string

const (
	PlayerLifeStateClassSelection      PlayerLifeState = "class_selection"
	PlayerLifeStateSpawnReady          PlayerLifeState = "spawn_ready"
	PlayerLifeStateSpawnRequestPending PlayerLifeState = "spawn_request_pending"
	PlayerLifeStateSpawned             PlayerLifeState = "spawned"
	PlayerLifeStateDead                PlayerLifeState = "dead"
)

type DeathSource string

const (
	DeathSourceServerHealth DeathSource = "server_health"
	// Vehicle and RC vehicle sources are reserved for an integration that has
	// confirmed ped death; server vehicle RPCs only detach the occupant.
	DeathSourceVehicle   DeathSource = "vehicle"
	DeathSourceRCVehicle DeathSource = "rc_vehicle"
)

// InvalidSAMPPlayerID is the wire representation of an unknown killer.
const InvalidSAMPPlayerID uint16 = ^uint16(0)

// UnknownDeathReason is the public-event sentinel for a death whose weapon
// cannot be recovered from the authoritative update. Weapon ID 0 means
// "fist" in SA-MP and must not be reused for an unknown cause.
const UnknownDeathReason uint8 = ^uint8(0)

const (
	spawnRequestTimeout           = 5 * time.Second
	deathNotificationRetry        = time.Second
	deathNotificationRetryCeiling = 30 * time.Second
	deathWriteTimeout             = 2 * time.Second
	autoRespawnAfterDeathDelay    = 2500 * time.Millisecond
	autoRespawnRetry              = 500 * time.Millisecond
	autoRespawnRetryCeiling       = 30 * time.Second
)

type lifecycleSpawnPhase uint8

const (
	spawnPhaseIdle lifecycleSpawnPhase = iota
	spawnPhaseRequesting
	spawnPhaseSpawning
)

// playerLifecycle owns every field that participates in the local player's
// class/spawn/death transaction. Client stateMu protects this structure. The
// rest of Client contains gameplay state, not lifecycle decisions.
type playerLifecycle struct {
	lifeState          PlayerLifeState
	spawned            bool
	deathReported      bool
	spawnRequested     bool
	spawnRequestOrigin PlayerLifeState
	spawnInfoReady     bool
	deathInProgress    bool
	respawnNotBefore   time.Time

	spawnPhase     lifecycleSpawnPhase
	spawnRequestAt time.Time
	spawnRequestID uint64

	deathReportPending  bool
	deathReportNextTry  time.Time
	deathReportAttempts uint32
	deathCause          DeathCause
	autoRespawnRunning  bool
	autoRespawnEpoch    uint64
}

func newPlayerLifecycle() playerLifecycle {
	return playerLifecycle{
		lifeState:          PlayerLifeStateClassSelection,
		spawnRequestOrigin: PlayerLifeStateClassSelection,
		spawnPhase:         spawnPhaseIdle,
	}
}

func normalizeLifeState(state PlayerLifeState) PlayerLifeState {
	if state == "" {
		return PlayerLifeStateClassSelection
	}
	return state
}

func (l *playerLifecycle) state() PlayerLifeState {
	return normalizeLifeState(l.lifeState)
}

func (l *playerLifecycle) setState(state PlayerLifeState) {
	l.lifeState = normalizeLifeState(state)
	l.spawned = l.lifeState == PlayerLifeStateSpawned
}

func (l *playerLifecycle) transition(state PlayerLifeState) bool {
	previous := l.state()
	l.setState(state)
	return previous != l.state()
}

func (l *playerLifecycle) resetForConnection() {
	nextEpoch := l.autoRespawnEpoch + 1
	*l = newPlayerLifecycle()
	l.autoRespawnEpoch = nextEpoch
}

func (l *playerLifecycle) invalidateAutomaticSpawn() {
	l.autoRespawnEpoch++
	l.autoRespawnRunning = false
}

func (l *playerLifecycle) enterClassSelection(clearSpawnInfo, resetDeath bool) bool {
	previous := l.state()
	l.invalidateAutomaticSpawn()
	l.spawned = false
	l.spawnRequested = false
	l.spawnRequestOrigin = PlayerLifeStateClassSelection
	l.spawnPhase = spawnPhaseIdle
	l.spawnRequestAt = time.Time{}
	if clearSpawnInfo {
		l.spawnInfoReady = false
	}
	if resetDeath {
		l.deathReported = false
		l.deathInProgress = false
		l.respawnNotBefore = time.Time{}
		l.deathReportPending = false
		l.deathReportNextTry = time.Time{}
	}
	l.setState(PlayerLifeStateClassSelection)
	return previous != PlayerLifeStateClassSelection
}

func (l *playerLifecycle) beginSpawnRequest(now time.Time) (PlayerLifeState, uint64, bool) {
	if !l.spawnInfoReady || l.spawned || l.spawnPhase != spawnPhaseIdle {
		return "", 0, false
	}
	origin := l.state()
	if origin != PlayerLifeStateDead {
		origin = PlayerLifeStateSpawnReady
	}
	l.spawnRequestID++
	l.spawnRequestAt = now
	l.spawnRequestOrigin = origin
	l.spawnRequested = true
	l.spawnPhase = spawnPhaseRequesting
	l.setState(PlayerLifeStateSpawnRequestPending)
	return origin, l.spawnRequestID, true
}

// beginDirectSpawn starts the PC SA-MP death-respawn transaction. Once a
// player has been spawned, the class/spawn information already sent by the
// server remains valid for the next life; the client sends RPC_Spawn directly
// instead of asking the server to open another class-selection request.
func (l *playerLifecycle) beginDirectSpawn() bool {
	if !l.spawnInfoReady || l.spawned || l.state() != PlayerLifeStateDead || l.spawnPhase != spawnPhaseIdle {
		return false
	}
	l.spawnRequestOrigin = PlayerLifeStateDead
	l.spawnRequested = false
	l.spawnPhase = spawnPhaseSpawning
	l.spawnRequestAt = time.Time{}
	return true
}

func (l *playerLifecycle) rollbackSpawnRequest(requestID uint64) (PlayerLifeState, bool) {
	if l.spawnPhase != spawnPhaseRequesting || l.spawnRequestID != requestID {
		return l.state(), false
	}
	origin := normalizeLifeState(l.spawnRequestOrigin)
	l.spawnRequested = false
	l.spawnPhase = spawnPhaseIdle
	l.spawnRequestAt = time.Time{}
	l.setState(origin)
	return l.state(), true
}

func (l *playerLifecycle) beginSpawning(outcome uint8, forcedOutcome uint8) bool {
	if l.spawned || l.spawnPhase == spawnPhaseSpawning {
		return false
	}
	if outcome != forcedOutcome && (outcome == 0 || l.spawnPhase != spawnPhaseRequesting || !l.spawnRequested) {
		return false
	}
	if l.spawnRequestOrigin == "" || l.spawnRequestOrigin == PlayerLifeStateClassSelection {
		origin := l.state()
		if origin != PlayerLifeStateDead {
			origin = PlayerLifeStateSpawnReady
		}
		l.spawnRequestOrigin = origin
	}
	l.spawnPhase = spawnPhaseSpawning
	l.spawnRequested = false
	l.spawnRequestAt = time.Time{}
	return true
}

func (l *playerLifecycle) rejectSpawnRequest() (PlayerLifeState, bool) {
	if l.spawnPhase != spawnPhaseRequesting {
		return l.state(), false
	}
	origin := normalizeLifeState(l.spawnRequestOrigin)
	l.spawnRequested = false
	l.spawnPhase = spawnPhaseIdle
	l.spawnRequestAt = time.Time{}
	l.setState(origin)
	return l.state(), true
}

func (l *playerLifecycle) rollbackSpawning() (PlayerLifeState, bool) {
	if l.spawnPhase != spawnPhaseSpawning {
		return l.state(), false
	}
	origin := normalizeLifeState(l.spawnRequestOrigin)
	l.spawnPhase = spawnPhaseIdle
	l.spawnRequested = false
	l.spawnRequestAt = time.Time{}
	l.setState(origin)
	return l.state(), true
}

func (l *playerLifecycle) expireSpawnRequest(now time.Time) (PlayerLifeState, bool) {
	if l.spawnPhase != spawnPhaseRequesting || l.spawnRequestAt.IsZero() || now.Sub(l.spawnRequestAt) < spawnRequestTimeout {
		return l.state(), false
	}
	return l.rejectSpawnRequest()
}

type PlayerDeathEvent struct {
	Reason      uint8
	KillerID    uint16
	ReasonKnown bool
	Source      DeathSource
}

// DeathCause is supplied by a native GTA integration when it can inspect the
// actual ped damage state. Server health RPCs do not contain this information.
type DeathCause struct {
	Reason      uint8
	KillerID    uint16
	ReasonKnown bool
}

func unknownDeathCause() DeathCause {
	return DeathCause{Reason: UnknownDeathReason, KillerID: InvalidSAMPPlayerID}
}

type PlayerLifeStateEvent struct {
	State PlayerLifeState
}

func encodeDeathNotificationPayload(reason uint8, killerID uint16) []byte {
	payload := raknet.Writer{}
	payload.Uint8(reason)
	payload.Uint16(killerID)
	return payload.Bytes()
}

func (c *Client) queuePendingEvent(event Event) {
	c.pendingMu.Lock()
	c.pendingEvents = append(c.pendingEvents, event)
	c.pendingMu.Unlock()
}

func (c *Client) drainPendingEvents() []Event {
	c.pendingMu.Lock()
	events := c.pendingEvents
	c.pendingEvents = nil
	c.pendingMu.Unlock()
	return events
}

func (c *Client) flushPendingEvents() {
	c.eventBatchMu.Lock()
	c.emitBatch(c.drainPendingEvents())
	c.eventBatchMu.Unlock()
}

func (c *Client) contextOrBackground() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

func (c *Client) emitLifeState(state PlayerLifeState) {
	event := lifecycleStateEvent(state)
	if c.ctx == nil || c.events == nil {
		c.queuePendingEvent(event)
		return
	}
	c.eventBatchMu.Lock()
	c.emitBatch([]Event{event})
	c.eventBatchMu.Unlock()
}

func lifecycleStateEvent(state PlayerLifeState) Event {
	return Event{Type: EventPlayerLifeState, Data: PlayerLifeStateEvent{State: state}}
}

func (c *Client) queueLifeState(state PlayerLifeState) {
	c.queuePendingEvent(lifecycleStateEvent(state))
}

func (c *Client) rollbackSpawnRequest(requestID uint64) {
	c.stateMu.Lock()
	state, changed := c.lifecycle.rollbackSpawnRequest(requestID)
	c.stateMu.Unlock()
	if changed {
		c.emitLifeState(state)
	}
}

func (c *Client) rollbackSpawning() {
	c.stateMu.Lock()
	state, changed := c.lifecycle.rollbackSpawning()
	c.stateMu.Unlock()
	if changed {
		// Called from the RPC decode transaction. The caller will append this
		// state event after the protocol error/result so event order remains
		// deterministic without re-entering eventBatchMu.
		c.queuePendingEvent(lifecycleStateEvent(state))
	}
}

func (c *Client) expireSpawnRequest(now time.Time) {
	c.stateMu.Lock()
	state, expired := c.lifecycle.expireSpawnRequest(now)
	c.stateMu.Unlock()
	if expired {
		c.emitLifeState(state)
	}
}

func (c *Client) rejectClassSelection() *Event {
	c.stateMu.Lock()
	if c.lifecycle.spawned {
		c.stateMu.Unlock()
		return nil
	}
	// A class-selection response can invalidate an in-flight vehicle task
	// without passing through death. Do not leave a stale entry transaction
	// that could later commit after a new spawn epoch begins.
	if c.inVehicle || c.enterPending || c.enterQueued || c.exitPending {
		c.clearVehicleStateLocked()
	}
	changed := c.lifecycle.enterClassSelection(true, false)
	state := c.lifecycle.state()
	c.stateMu.Unlock()
	if !changed {
		return nil
	}
	return &Event{Type: EventPlayerLifeState, Data: PlayerLifeStateEvent{State: state}}
}

func (c *Client) sendDeathNotification(ctx context.Context, cause DeathCause) error {
	payload := raknet.Writer{}
	// A server health update has no weapon or killer fields. Keep the SA-MP
	// wire format explicit for that case, while preserving a native
	// integration's confirmed cause when one is available. SA-MP weapon 0 is
	// fist; 255 is the protocol's unknown/suicide sentinel and must not be
	// reported as a fist hit.
	if !cause.ReasonKnown || cause.Reason == UnknownDeathReason {
		payload.Uint8(UnknownDeathReason)
		payload.Uint16(InvalidSAMPPlayerID)
	} else {
		payload.Uint8(cause.Reason)
		payload.Uint16(cause.KillerID)
	}
	// PC SA-MP uses RELIABLE_SEQUENCED for the death notification. Ordered
	// reliability can hold this lifecycle edge behind an older packet and does
	// not match the native client's wire contract.
	return c.sendRPC(ctx, RPCDeath, &payload, raknet.ReliableSequenced)
}

func (l *playerLifecycle) scheduleDeathReportRetry(now time.Time) {
	l.deathReportAttempts++
	delay := deathNotificationRetry
	for attempt := uint32(1); attempt < l.deathReportAttempts && delay < deathNotificationRetryCeiling; attempt++ {
		delay *= 2
		if delay > deathNotificationRetryCeiling {
			delay = deathNotificationRetryCeiling
		}
	}
	l.deathReportNextTry = now.Add(delay)
}

func (l *playerLifecycle) clearDeathReportRetry() {
	l.deathReportPending = false
	l.deathReportNextTry = time.Time{}
	l.deathReportAttempts = 0
}

func nextAutoRespawnDelay(previous time.Duration) time.Duration {
	if previous <= 0 {
		return autoRespawnRetry
	}
	next := previous * 2
	if next > autoRespawnRetryCeiling {
		return autoRespawnRetryCeiling
	}
	return next
}

// startAutomaticSpawn starts one policy worker for a confirmed death. Initial
// spawning remains explicit: some servers use their class-selection flow to
// authorize the first spawn and reject unsolicited RequestSpawn RPCs. The
// worker is deliberately outside the decoder: it can wait for spawn
// information, retry a failed direct respawn write, and stop as soon as a
// spawn is committed without complicating RPC parsing.
func (c *Client) startAutomaticSpawn() {
	c.stateMu.Lock()
	state := c.lifecycle.state()
	if c.respawnPolicy != RespawnPolicyAutomatic ||
		state != PlayerLifeStateDead ||
		c.lifecycle.autoRespawnRunning {
		c.stateMu.Unlock()
		return
	}
	c.lifecycle.autoRespawnRunning = true
	c.lifecycle.autoRespawnEpoch++
	epoch := c.lifecycle.autoRespawnEpoch
	initialDelay := time.Duration(0)
	if state == PlayerLifeStateDead {
		if c.lifecycle.respawnNotBefore.IsZero() {
			initialDelay = autoRespawnAfterDeathDelay
		} else {
			initialDelay = time.Until(c.lifecycle.respawnNotBefore)
			if initialDelay < 0 {
				initialDelay = 0
			}
		}
	}
	c.stateMu.Unlock()
	go c.runAutomaticSpawn(epoch, initialDelay)
}

func (c *Client) runAutomaticSpawn(epoch uint64, initialDelay time.Duration) {
	ctx := c.contextOrBackground()
	defer func() {
		c.stateMu.Lock()
		restart := epoch == c.lifecycle.autoRespawnEpoch &&
			c.respawnPolicy == RespawnPolicyAutomatic &&
			c.lifecycle.state() == PlayerLifeStateDead &&
			ctx.Err() == nil
		if epoch == c.lifecycle.autoRespawnEpoch {
			c.lifecycle.autoRespawnRunning = false
		}
		c.stateMu.Unlock()
		if restart {
			c.startAutomaticSpawn()
		}
	}()

	firstAttempt := true
	retryDelay := time.Duration(0)
	for {
		delay := retryDelay
		if firstAttempt {
			delay = initialDelay
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			}
		}

		c.stateMu.RLock()
		state := c.lifecycle.state()
		ready := c.lifecycle.spawnInfoReady
		currentEpoch := c.lifecycle.autoRespawnEpoch
		policy := c.respawnPolicy
		c.stateMu.RUnlock()
		if currentEpoch != epoch || policy != RespawnPolicyAutomatic ||
			state != PlayerLifeStateDead {
			return
		}
		if ready {
			// RequestSpawn selects the direct RPC_Spawn path for a dead player,
			// matching the PC client's death-respawn protocol. A failed write is
			// retried by this bounded backoff loop.
			_ = c.RequestSpawn(ctx)
		}
		if firstAttempt {
			firstAttempt = false
			retryDelay = autoRespawnRetry
		} else {
			retryDelay = nextAutoRespawnDelay(retryDelay)
		}
	}
}

func (c *Client) hasTransport() bool {
	return c.conn != nil || c.rpcSender != nil
}

// NotifyLocalVehicleDestroyed is the integration point for a future native
// GTA backend. Vehicle removal is not by itself enough to kill the local
// player, so this only detaches the local occupant. A native backend that has
// independently confirmed ped death must call NotifyLocalPlayerDeath.
func (c *Client) NotifyLocalVehicleDestroyed(vehicleID uint16, remoteControlled bool) {
	_ = remoteControlled // kept for compatibility with the native integration API
	c.detachLocalVehicle(vehicleID)
}

// NotifyLocalPlayerDeath lets a native backend report a ped death when it can
// observe the local GTA ped directly. The cause remains explicitly unknown
// because this compatibility entry point carries no damage metadata.
func (c *Client) NotifyLocalPlayerDeath(source DeathSource) {
	c.markDeadWithCause(source, unknownDeathCause())
	c.flushPendingEvents()
	c.startAutomaticSpawn()
}

// NotifyLocalPlayerDeathWithCause reports a native-confirmed death and its
// SA-MP weapon/killer metadata. It is intentionally separate from server
// health handling: RPC_SetPlayerHealth cannot establish either field.
func (c *Client) NotifyLocalPlayerDeathWithCause(source DeathSource, reason uint8, killerID uint16) {
	c.markDeadWithCause(source, DeathCause{Reason: reason, KillerID: killerID, ReasonKnown: true})
	c.flushPendingEvents()
	c.startAutomaticSpawn()
}

func (c *Client) detachLocalVehicle(vehicleID uint16) {
	var queuedVehicle uint16
	var queuedPassenger bool
	var queuedMode VehicleEntryMode
	var queuedKnown bool
	var shouldEnter bool
	c.syncMu.Lock()
	c.stateMu.Lock()
	c.cancelVehicleEntryForVehicleLocked(vehicleID)
	if c.inVehicle && c.vehicleID == vehicleID {
		queuedVehicle, queuedPassenger, queuedMode, queuedKnown, shouldEnter = c.clearVehicleStateLocked()
		c.stateMu.Unlock()
		c.syncMu.Unlock()
		c.queuePendingEvent(Event{Type: EventVehicleState, Data: VehicleStateEvent{}})
		c.flushPendingEvents()
		c.continueQueuedVehicleEntry(queuedVehicle, queuedPassenger, queuedMode, queuedKnown, shouldEnter)
		return
	}
	c.stateMu.Unlock()
	c.syncMu.Unlock()
}

// retryDeathNotification retries only the reliable death report. Normal
// player sync is already disabled by the dead lifecycle state, so this cannot
// accidentally revive the player or emit an ordinary post-death frame.
func (c *Client) retryDeathNotification(now time.Time) {
	c.stateMu.RLock()
	pending := c.lifecycle.deathReportPending
	nextTry := c.lifecycle.deathReportNextTry
	cause := c.lifecycle.deathCause
	c.stateMu.RUnlock()
	if !pending || (!nextTry.IsZero() && now.Before(nextTry)) {
		return
	}
	c.deathWireMu.Lock()
	defer c.deathWireMu.Unlock()
	c.stateMu.RLock()
	pending = c.lifecycle.deathReportPending
	nextTry = c.lifecycle.deathReportNextTry
	cause = c.lifecycle.deathCause
	c.stateMu.RUnlock()
	if !pending || (!nextTry.IsZero() && now.Before(nextTry)) {
		return
	}
	ctx, cancel := context.WithTimeout(c.contextOrBackground(), deathWriteTimeout)
	err := c.sendDeathNotification(ctx, cause)
	cancel()
	c.stateMu.Lock()
	if err == nil {
		c.lifecycle.clearDeathReportRetry()
	} else {
		c.lifecycle.scheduleDeathReportRetry(now)
	}
	c.stateMu.Unlock()
	// The initial write failure is already surfaced by markDead. Retries are
	// intentionally silent and exponentially backed off so a persistent
	// transport failure cannot flood the plugin event stream.
}

// markDead performs the one-time transition for a spawned local player. The
// sync lock only reserves the transition; network writes use a separate,
// bounded wire transaction so state locks are never held during I/O.
func (c *Client) markDead(source DeathSource) {
	c.markDeadWithCause(source, unknownDeathCause())
}

func (c *Client) markDeadWithCause(source DeathSource, cause DeathCause) {
	finalDriverFrame, reserved := c.reserveDeath()
	if !reserved {
		return
	}
	c.completeDeath(source, finalDriverFrame, cause)
}

func (c *Client) reserveDeath() (finalDriverFrame bool, reserved bool) {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if !c.lifecycle.spawned || c.lifecycle.deathReported || c.lifecycle.deathInProgress {
		return false, false
	}
	finalDriverFrame = c.conn != nil && c.inVehicle && !c.passenger
	c.lifecycle.deathInProgress = true
	return finalDriverFrame, true
}

func (c *Client) completeDeath(source DeathSource, finalDriverFrame bool, cause DeathCause) {
	// Keep the final active frame, RPC_Death, and the dead-state transition on
	// one realtime-sync critical section. Otherwise a concurrent sync tick can
	// publish an old on-foot/vehicle snapshot after the death notification.
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	c.deathWireMu.Lock()
	ctx, cancel := context.WithTimeout(c.contextOrBackground(), deathWriteTimeout)
	if finalDriverFrame {
		// Preserve the native ordering: the final driver frame is emitted while
		// the player is still considered active, immediately before RPC_Death.
		_ = c.sendVehicle(ctx, false)
	}

	c.stateMu.Lock()
	if !c.lifecycle.spawned || c.lifecycle.deathReported || !c.lifecycle.deathInProgress {
		c.stateMu.Unlock()
		cancel()
		c.deathWireMu.Unlock()
		return
	}
	position := c.position
	wasInVehicle := c.inVehicle
	c.lifecycle.spawned = false
	c.lifecycle.setState(PlayerLifeStateDead)
	c.lifecycle.spawnRequested = false
	c.lifecycle.spawnRequestOrigin = PlayerLifeStateDead
	c.lifecycle.spawnPhase = spawnPhaseIdle
	c.lifecycle.spawnRequestAt = time.Time{}
	c.lifecycle.deathInProgress = false
	c.lifecycle.deathReported = true
	c.lifecycle.respawnNotBefore = time.Now().Add(autoRespawnAfterDeathDelay)
	c.lifecycle.invalidateAutomaticSpawn()
	c.lifecycle.deathReportPending = c.hasTransport()
	c.lifecycle.deathReportNextTry = time.Time{}
	c.lifecycle.deathReportAttempts = 0
	if !cause.ReasonKnown || cause.Reason == UnknownDeathReason {
		cause = unknownDeathCause()
	}
	c.lifecycle.deathCause = cause
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
	c.health = 0
	c.respawnHealth = 0
	c.respawnArmour = 0
	c.respawnHealthKnown = false
	c.vehicleHealth = 0
	c.vehicleHealthKnown = false
	c.vehicleVelocity = [3]float32{}
	c.vehicleQuaternion = [4]float32{}
	c.vehicleLRAnalog = 0
	c.vehicleUDAnalog = 0
	c.vehicleProtocolKeys = 0
	c.clearMotionFrameLocked()
	c.stateMu.Unlock()

	// Cancel a plugin movement task as part of death, but publish it after the
	// health event so observers see the same lifecycle order as the client.
	c.motionMu.Lock()
	task := c.motion
	c.motion = nil
	c.motionMu.Unlock()
	if task != nil {
		c.queuePendingEvent(Event{Type: EventMovement, Data: MotionEvent{
			TaskID: task.id, Kind: task.kind, State: MotionStopped,
			Position: position, Target: task.target, Error: "player died",
		}})
	}

	var notificationErr error
	if c.hasTransport() {
		notificationErr = c.sendDeathNotification(ctx, cause)
		if notificationErr == nil {
			c.stateMu.Lock()
			c.lifecycle.clearDeathReportRetry()
			c.stateMu.Unlock()
		} else {
			c.stateMu.Lock()
			c.lifecycle.scheduleDeathReportRetry(time.Now())
			c.stateMu.Unlock()
		}
	}
	if wasInVehicle {
		c.queuePendingEvent(Event{Type: EventVehicleState, Data: VehicleStateEvent{}})
	}
	c.queuePendingEvent(Event{Type: EventPlayerLifeState, Data: PlayerLifeStateEvent{State: PlayerLifeStateDead}})
	c.queuePendingEvent(Event{Type: EventPlayerDeath, Data: PlayerDeathEvent{
		Reason: cause.Reason, KillerID: cause.KillerID, ReasonKnown: cause.ReasonKnown, Source: source,
	}})
	if notificationErr != nil {
		c.queuePendingEvent(Event{Type: EventProtocolError, Data: fmt.Errorf("death notification: %w", notificationErr)})
	}
	cancel()
	c.deathWireMu.Unlock()
}
