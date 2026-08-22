package samp

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestMoveTowardsDoesNotOvershoot(t *testing.T) {
	next, velocity, reached := moveTowards([3]float32{0, 0, 0}, [3]float32{1, 0, 0}, 10, 0.2)
	if !reached || next != [3]float32{1, 0, 0} || velocity != [3]float32{} {
		t.Fatalf("moveTowards = next=%v velocity=%v reached=%v", next, velocity, reached)
	}
}

func TestDriveTowardsBrakesBeforeTarget(t *testing.T) {
	next, velocity, braking, reached := driveTowards([3]float32{0, 0, 0}, [3]float32{10, 0, 0}, 12, 0.03, 0.35)
	if !braking || reached {
		t.Fatalf("driveTowards braking=%v reached=%v", braking, reached)
	}
	if velocity[0] <= 0 || next[0] <= 0 || next[0] >= 10 {
		t.Fatalf("driveTowards next=%v velocity=%v", next, velocity)
	}
}

func TestSyncVelocityUsesGTAPhysicsUnits(t *testing.T) {
	got := syncVelocity([3]float32{1.4, -2, 0.5})
	want := [3]float32{0.028, -0.04, 0.01}
	for index := range got {
		if diff := got[index] - want[index]; diff < -0.000001 || diff > 0.000001 {
			t.Fatalf("syncVelocity[%d] = %f, want %f", index, got[index], want[index])
		}
	}
}

func TestTargetHeadingUsesGTAForwardAxis(t *testing.T) {
	tests := []struct {
		name      string
		direction [3]float32
		wantYaw   float32
		wantQuat  [4]float32
	}{
		{name: "north", direction: [3]float32{0, 1, 0}, wantYaw: 0, wantQuat: [4]float32{1, 0, 0, 0}},
		{name: "east", direction: [3]float32{1, 0, 0}, wantYaw: 270, wantQuat: [4]float32{-0.70710677, 0, 0, -0.70710677}},
		{name: "south", direction: [3]float32{0, -1, 0}, wantYaw: 180, wantQuat: [4]float32{0, 0, 0, -1}},
		{name: "west", direction: [3]float32{-1, 0, 0}, wantYaw: 90, wantQuat: [4]float32{0.70710677, 0, 0, -0.70710677}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := yawForDirection(test.direction); math.Abs(float64(got-test.wantYaw)) > 0.00001 {
				t.Fatalf("yawForDirection = %f, want %f", got, test.wantYaw)
			}
			got := yawQuaternion(test.wantYaw)
			for index := range got {
				if math.Abs(float64(got[index]-test.wantQuat[index])) > 0.00001 {
					t.Fatalf("yawQuaternion[%d] = %f, want %f", index, got[index], test.wantQuat[index])
				}
			}
		})
	}
}

func TestWalkToEmitsStartedProgressAndStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{
		ctx:       ctx,
		events:    make(chan Event, 8),
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		position:  [3]float32{0, 0, 0},
	}
	taskID, err := c.WalkTo([3]float32{10, 0, 0}, 1, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	started := (<-c.events).Data.(MotionEvent)
	if started.TaskID != taskID || started.State != MotionStarted {
		t.Fatalf("started event = %+v", started)
	}

	c.advanceMotion(time.Now().Add(100 * time.Millisecond))
	progress := (<-c.events).Data.(MotionEvent)
	if progress.TaskID != taskID || progress.State != MotionProgress {
		t.Fatalf("progress event = %+v", progress)
	}
	if c.position[0] <= 0.09 || c.position[0] >= 0.11 || c.onFootUDAnalog != analogWire(analogForward) || c.onFootQuaternion != yawQuaternion(270) {
		t.Fatalf("walking frame position=%v velocity=%v quaternion=%v udAnalog=%d", c.position, c.onFootVelocity, c.onFootQuaternion, c.onFootUDAnalog)
	}
	if got, want := c.onFootVelocity[0], float32(1)/gtaPhysicsStepsPerSecond; math.Abs(float64(got-want)) > 0.000001 {
		t.Fatalf("walking sync velocity = %f, want %f", got, want)
	}

	c.StopMovement()
	stopped := (<-c.events).Data.(MotionEvent)
	if stopped.TaskID != taskID || stopped.State != MotionStopped {
		t.Fatalf("stopped event = %+v", stopped)
	}
}

func TestDriveToUsesGTAPhysicsVelocityAndVehicleControls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{
		ctx:       ctx,
		events:    make(chan Event, 8),
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		inVehicle: true,
		vehicleID: 42,
		position:  [3]float32{0, 0, 0},
	}
	taskID, err := c.DriveTo(42, [3]float32{20, 0, 0}, 12, 0.35)
	if err != nil {
		t.Fatal(err)
	}
	started := (<-c.events).Data.(MotionEvent)
	if started.TaskID != taskID || started.State != MotionStarted {
		t.Fatalf("started event = %+v", started)
	}

	c.advanceMotion(time.Now().Add(100 * time.Millisecond))
	progress := (<-c.events).Data.(MotionEvent)
	if progress.TaskID != taskID || progress.State != MotionProgress {
		t.Fatalf("progress event = %+v", progress)
	}
	if got, want := c.vehicleVelocity[0], float32(12)/gtaPhysicsStepsPerSecond; math.Abs(float64(got-want)) > 0.000001 {
		t.Fatalf("vehicle sync velocity = %f, want %f", got, want)
	}
	if c.vehicleUDAnalog != analogWire(analogForward) || c.vehicleProtocolKeys != keyAccelerate || c.vehicleQuaternion != yawQuaternion(270) {
		t.Fatalf("vehicle controls = ud=%d keys=%d quaternion=%v, want ud=%d keys=%d quaternion=%v", c.vehicleUDAnalog, c.vehicleProtocolKeys, c.vehicleQuaternion, analogWire(analogForward), keyAccelerate, yawQuaternion(270))
	}
}

func TestDriveToRejectsPassengerImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{
		ctx:       ctx,
		events:    make(chan Event, 8),
		lifecycle: playerLifecycle{spawned: true, lifeState: PlayerLifeStateSpawned},
		inVehicle: true,
		passenger: true,
		vehicleID: 42,
		position:  [3]float32{0, 0, 0},
	}
	if _, err := c.DriveTo(42, [3]float32{20, 0, 0}, 12, 0.35); !errors.Is(err, ErrMotionNotDriver) {
		t.Fatalf("DriveTo error = %v, want %v", err, ErrMotionNotDriver)
	}
	select {
	case event := <-c.events:
		t.Fatalf("passenger DriveTo emitted event: %+v", event)
	default:
	}
}

func TestWalkToRequiresSpawn(t *testing.T) {
	c := &Client{}
	_, err := c.WalkTo([3]float32{1, 2, 3}, 1, 0.2)
	if !errors.Is(err, ErrMotionNotSpawned) {
		t.Fatalf("WalkTo error = %v, want %v", err, ErrMotionNotSpawned)
	}
}
