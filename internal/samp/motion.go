package samp

import (
	"errors"
	"math"
	"time"
)

const (
	defaultWalkSpeed       float32 = 1.4
	defaultDriveSpeed      float32 = 12
	defaultMotionTolerance float32 = 0.35
	// GTA integrates moveSpeed with a base physics timestep of 1/50 second.
	// Motion options are expressed in world units per second.
	gtaPhysicsStepsPerSecond float32 = 50
	motionProgressPeriod             = 500 * time.Millisecond
	motionEnterTimeout               = 5 * time.Second

	// SA-MP exposes directional analog values separately from the regular key
	// bitmask. These are the values used by the normal client for the digital
	// directional controls.
	analogForward  int16 = -128
	analogBackward int16 = 128
	analogLeft     int16 = -128
	analogRight    int16 = 128

	keyAccelerate uint16 = 8  // KEY_SPRINT / vehicle accelerate
	keyBrake      uint16 = 32 // KEY_JUMP / vehicle brake
)

var (
	ErrMotionSpeed      = errors.New("samp: motion speed must be positive")
	ErrMotionTarget     = errors.New("samp: motion target is not finite")
	ErrMotionNotDriver  = errors.New("samp: drive task requires the driver seat")
	ErrMotionNotSpawned = errors.New("samp: client is not spawned")
)

type MotionKind string

const (
	MotionWalk  MotionKind = "walk"
	MotionDrive MotionKind = "drive"
)

type MotionState string

const (
	MotionStarted   MotionState = "started"
	MotionProgress  MotionState = "progress"
	MotionCompleted MotionState = "completed"
	MotionStopped   MotionState = "stopped"
	MotionFailed    MotionState = "failed"
)

// MotionEvent is deliberately small: it is emitted at task boundaries and
// at a throttled progress cadence, not once per network sync frame.
type MotionEvent struct {
	TaskID   uint64
	Kind     MotionKind
	State    MotionState
	Position [3]float32
	Target   [3]float32
	Progress float32
	Error    string
}

type motionTask struct {
	id              uint64
	kind            MotionKind
	target          [3]float32
	vehicleID       uint16
	speed           float32
	tolerance       float32
	startedAt       time.Time
	lastTickAt      time.Time
	initialDistance float32
	lastProgressAt  time.Time
	lastEnterAt     time.Time
	enterRequested  bool
}

type onFootFrame struct {
	lrAnalog, udAnalog uint16
	keys               uint16
	position           [3]float32
	quaternion         [4]float32
	health, armour     uint8
	weapon             uint8
	specialAction      uint8
	velocity           [3]float32
	surfingOffsets     [3]float32
	surfingVehicleID   uint16
	animationID        int16
	animationFlags     int16
}

type vehicleFrame struct {
	vehicleID                  uint16
	lrAnalog, udAnalog         uint16
	keys                       uint16
	quaternion                 [4]float32
	position, velocity         [3]float32
	vehicleHealth              float32
	playerHealth, playerArmour uint8
	weapon, siren, landingGear uint8
	trailerID                  uint16
	trainSpeed                 float32
}

type passengerFrame struct {
	vehicleID                  uint16
	seatID, driveBy            uint8
	additionalKey, weapon      uint8
	playerHealth, playerArmour uint8
	lrAnalog, udAnalog, keys   uint16
	position                   [3]float32
}

func normalizeMotionOptions(speed, tolerance float32, drive bool) (float32, float32, error) {
	if speed == 0 {
		if drive {
			speed = defaultDriveSpeed
		} else {
			speed = defaultWalkSpeed
		}
	}
	if speed <= 0 || math.IsNaN(float64(speed)) || math.IsInf(float64(speed), 0) {
		return 0, 0, ErrMotionSpeed
	}
	if tolerance == 0 {
		tolerance = defaultMotionTolerance
	}
	if tolerance <= 0 || math.IsNaN(float64(tolerance)) || math.IsInf(float64(tolerance), 0) {
		return 0, 0, ErrMotionTarget
	}
	return speed, tolerance, nil
}

func validTarget(target [3]float32) bool {
	for _, value := range target {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}

func distance3(a, b [3]float32) float32 {
	dx, dy, dz := b[0]-a[0], b[1]-a[1], b[2]-a[2]
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

func moveTowards(position, target [3]float32, speed, dt float32) (next, velocity [3]float32, reached bool) {
	dx, dy, dz := target[0]-position[0], target[1]-position[1], target[2]-position[2]
	distance := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
	step := speed * dt
	if distance <= step || distance == 0 {
		return target, [3]float32{}, true
	}
	inv := 1 / distance
	velocity = [3]float32{dx * inv * speed, dy * inv * speed, dz * inv * speed}
	return [3]float32{
		position[0] + velocity[0]*dt,
		position[1] + velocity[1]*dt,
		position[2] + velocity[2]*dt,
	}, velocity, false
}

func yawForDirection(direction [3]float32) float32 {
	// GTA's Z rotation is measured from +Y clockwise: a matrix with heading
	// yaw has forward=(-sin(yaw), cos(yaw), 0). Convert the target vector to
	// that convention instead of using the mathematical +X counter-clockwise
	// convention.
	if math.Hypot(float64(direction[0]), float64(direction[1])) < 0.000001 {
		return 0
	}
	yaw := float32(math.Atan2(float64(-direction[0]), float64(direction[1])) * 180 / math.Pi)
	if yaw < 0 {
		yaw += 360
	}
	return yaw
}

// yawQuaternion uses the same W,X,Y,Z order used by the SA-MP sync encoder.
// GTA's quaternion-to-matrix convention stores the negative half-angle in Z
// for its forward=(-sin(yaw), cos(yaw), 0) heading basis.
func yawQuaternion(yawDegrees float32) [4]float32 {
	half := float64(yawDegrees) * math.Pi / 360
	return [4]float32{float32(math.Cos(half)), 0, 0, float32(-math.Sin(half))}
}

func analogWire(value int16) uint16 { return uint16(value) }

// SA-MP's sync velocity fields contain GTA's physics moveSpeed, not a
// network-frame displacement. GTA advances an entity by
// CTimer::GetTimeStep()*moveSpeed; with the normal base timestep of 1/50 s,
// a planner velocity expressed in world units per second is divided by 50.
func syncVelocity(velocity [3]float32) [3]float32 {
	return [3]float32{
		velocity[0] / gtaPhysicsStepsPerSecond,
		velocity[1] / gtaPhysicsStepsPerSecond,
		velocity[2] / gtaPhysicsStepsPerSecond,
	}
}

func driveTowards(position, target [3]float32, speed, dt, tolerance float32) (next, velocity [3]float32, braking, reached bool) {
	distance := distance3(position, target)
	if distance <= tolerance {
		return target, [3]float32{}, true, true
	}
	const deceleration float32 = 6
	brakingDistance := speed * speed / (2 * deceleration)
	effectiveSpeed := speed
	if distance < brakingDistance {
		braking = true
		effectiveSpeed = float32(math.Sqrt(float64(2 * deceleration * distance)))
		if effectiveSpeed < 0.25 {
			effectiveSpeed = 0.25
		}
	}
	next, velocity, reached = moveTowards(position, target, effectiveSpeed, dt)
	if distance3(next, target) <= tolerance {
		return target, [3]float32{}, braking, true
	}
	return next, velocity, braking, reached
}

func (c *Client) WalkTo(target [3]float32, speed, tolerance float32) (uint64, error) {
	if !validTarget(target) {
		return 0, ErrMotionTarget
	}
	speed, tolerance, err := normalizeMotionOptions(speed, tolerance, false)
	if err != nil {
		return 0, err
	}
	return c.startMotion(motionTask{kind: MotionWalk, target: target, speed: speed, tolerance: tolerance})
}

func (c *Client) DriveTo(vehicleID uint16, target [3]float32, speed, tolerance float32) (uint64, error) {
	if !validTarget(target) {
		return 0, ErrMotionTarget
	}
	speed, tolerance, err := normalizeMotionOptions(speed, tolerance, true)
	if err != nil {
		return 0, err
	}
	return c.startMotion(motionTask{kind: MotionDrive, vehicleID: vehicleID, target: target, speed: speed, tolerance: tolerance})
}

func (c *Client) startMotion(task motionTask) (uint64, error) {
	c.motionMu.Lock()
	c.stateMu.RLock()
	position := c.position
	spawned, afk := c.spawned, c.afk
	inVehicle, passenger := c.inVehicle, c.passenger
	c.stateMu.RUnlock()
	if !spawned {
		c.motionMu.Unlock()
		return 0, ErrMotionNotSpawned
	}
	if afk {
		c.motionMu.Unlock()
		return 0, errors.New("samp: client is AFK")
	}
	if task.kind == MotionDrive && inVehicle && passenger {
		c.motionMu.Unlock()
		return 0, ErrMotionNotDriver
	}
	if !validTarget(position) {
		c.motionMu.Unlock()
		return 0, ErrMotionTarget
	}
	c.nextMotionID++
	task.id = c.nextMotionID
	task.startedAt = time.Now()
	task.lastTickAt = task.startedAt
	task.initialDistance = distance3(position, task.target)
	previous := c.motion
	c.motion = &task
	c.motionMu.Unlock()
	if previous != nil {
		c.emit(Event{Type: EventMovement, Data: MotionEvent{TaskID: previous.id, Kind: previous.kind, State: MotionStopped, Position: position, Target: previous.target, Error: "replaced by a new movement task"}})
	}
	c.emit(Event{Type: EventMovement, Data: MotionEvent{TaskID: task.id, Kind: task.kind, State: MotionStarted, Position: position, Target: task.target}})
	return task.id, nil
}

func (c *Client) StopMovement() {
	c.motionMu.Lock()
	task := c.motion
	c.motion = nil
	c.motionMu.Unlock()
	if task == nil {
		return
	}
	c.stateMu.Lock()
	c.clearMotionFrameLocked()
	position := c.position
	c.stateMu.Unlock()
	c.emit(Event{Type: EventMovement, Data: MotionEvent{TaskID: task.id, Kind: task.kind, State: MotionStopped, Position: position, Target: task.target}})
}

func (c *Client) finishMotion(taskID uint64, state MotionState, message string) {
	c.motionMu.Lock()
	if c.motion == nil || c.motion.id != taskID {
		c.motionMu.Unlock()
		return
	}
	task := *c.motion
	c.motion = nil
	c.motionMu.Unlock()
	c.stateMu.Lock()
	c.clearMotionFrameLocked()
	position := c.position
	c.stateMu.Unlock()
	c.emit(Event{Type: EventMovement, Data: MotionEvent{TaskID: task.id, Kind: task.kind, State: state, Position: position, Target: task.target, Progress: 1, Error: message}})
}

func (c *Client) clearMotionFrameLocked() {
	c.keyMask = 0
	c.onFootVelocity = [3]float32{}
	c.onFootLRAnalog, c.onFootUDAnalog, c.onFootProtocolKeys = 0, 0, 0
	c.vehicleVelocity = [3]float32{}
	c.vehicleLRAnalog, c.vehicleUDAnalog, c.vehicleProtocolKeys = 0, 0, 0
}

func (c *Client) advanceMotion(now time.Time) {
	c.motionMu.Lock()
	task := c.motion
	if task == nil {
		c.motionMu.Unlock()
		return
	}
	if task.lastTickAt.IsZero() {
		task.lastTickAt = now
	}
	dt := float32(now.Sub(task.lastTickAt).Seconds())
	if dt <= 0 {
		dt = float32(playerSyncInterval.Seconds())
	}
	if dt > 0.2 {
		dt = 0.2
	}
	task.lastTickAt = now

	var (
		requestEnter bool
		failMessage  string
		completed    bool
		position     [3]float32
		progress     float32
	)
	c.stateMu.Lock()
	if !c.spawned || c.afk {
		c.stateMu.Unlock()
		c.motionMu.Unlock()
		return
	}
	position = c.position
	switch task.kind {
	case MotionWalk:
		direction := [3]float32{task.target[0] - position[0], task.target[1] - position[1], task.target[2] - position[2]}
		if math.Hypot(float64(direction[0]), float64(direction[1])) >= 0.000001 {
			c.onFootQuaternion = yawQuaternion(yawForDirection(direction))
		}
		next, velocity, reached := moveTowards(position, task.target, task.speed, dt)
		c.position = next
		c.onFootVelocity = syncVelocity(velocity)
		if reached {
			c.onFootVelocity = [3]float32{}
			c.onFootLRAnalog, c.onFootUDAnalog, c.onFootProtocolKeys = 0, 0, 0
			completed = true
		} else {
			c.onFootUDAnalog = analogWire(analogForward)
			c.onFootLRAnalog = 0
			c.onFootProtocolKeys = 0
		}
		position = c.position
	case MotionDrive:
		if !c.inVehicle || c.vehicleID != task.vehicleID {
			if c.inVehicle && c.passenger {
				failMessage = ErrMotionNotDriver.Error()
			} else if !task.enterRequested {
				task.enterRequested = true
				task.lastEnterAt = now
				requestEnter = true
			} else if now.Sub(task.lastEnterAt) > motionEnterTimeout {
				failMessage = "timed out waiting for vehicle entry confirmation"
			}
		} else {
			next, velocity, braking, reached := driveTowards(position, task.target, task.speed, dt, task.tolerance)
			c.position = next
			c.vehicleVelocity = syncVelocity(velocity)
			direction := [3]float32{task.target[0] - position[0], task.target[1] - position[1], task.target[2] - position[2]}
			c.vehicleQuaternion = yawQuaternion(yawForDirection(direction))
			c.vehicleLRAnalog = 0
			switch {
			case reached:
				c.vehicleUDAnalog = 0
				c.vehicleProtocolKeys = 0
				c.vehicleVelocity = [3]float32{}
				completed = true
			case braking:
				c.vehicleUDAnalog = analogWire(analogBackward)
				c.vehicleProtocolKeys = keyBrake
			default:
				c.vehicleUDAnalog = analogWire(analogForward)
				c.vehicleProtocolKeys = keyAccelerate
			}
			position = c.position
		}
	}
	if failMessage == "" && !completed {
		progress = 1
		if task.initialDistance > 0 {
			progress = 1 - distance3(position, task.target)/task.initialDistance
		}
		if progress < 0 {
			progress = 0
		}
		if progress > 0.99 {
			progress = 0.99
		}
	}
	c.stateMu.Unlock()
	c.motionMu.Unlock()

	if requestEnter {
		if err := c.EnterVehicle(c.ctx, task.vehicleID, false, VehicleEntryDirect); err != nil {
			c.finishMotion(task.id, MotionFailed, err.Error())
		}
		return
	}
	if failMessage != "" {
		c.finishMotion(task.id, MotionFailed, failMessage)
		return
	}
	if completed {
		c.finishMotion(task.id, MotionCompleted, "")
		return
	}
	c.motionMu.Lock()
	shouldEmit := c.motion != nil && c.motion.id == task.id && (c.motion.lastProgressAt.IsZero() || now.Sub(c.motion.lastProgressAt) >= motionProgressPeriod)
	if shouldEmit {
		c.motion.lastProgressAt = now
	}
	c.motionMu.Unlock()
	if shouldEmit {
		c.emit(Event{Type: EventMovement, Data: MotionEvent{TaskID: task.id, Kind: task.kind, State: MotionProgress, Position: position, Target: task.target, Progress: progress}})
	}
}
