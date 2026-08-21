package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/samp"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/store"
	"github.com/SA-MP-Android/SA-MP-Pilot/plugin"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	s, e := store.Open(filepath.Join(t.TempDir(), "data.json"))
	if e != nil {
		t.Fatal(e)
	}
	return New(s)
}

func TestConnectionMessages(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{raknet.ErrServerFull, "The server is full."},
		{raknet.ErrBanned, "You are banned from this server."},
		{raknet.ErrInvalidPassword, "Wrong server password."},
		{fmt.Errorf("wrapped: %w", raknet.ErrConnectionLost), "Lost connection to the server."},
	}
	for _, test := range tests {
		if got := connectionMessage(test.err); got != test.want {
			t.Errorf("connectionMessage(%v) = %q, want %q", test.err, got, test.want)
		}
	}
	if got := connectionMessage(errors.New("custom")); got != "custom" {
		t.Fatalf("custom message = %q", got)
	}
}

func TestRetryConnectionMessages(t *testing.T) {
	if got := retryConnectionMessage(raknet.ErrServerFull); got != "The server is full. Retrying..." {
		t.Fatalf("server-full retry message = %q", got)
	}
	if got := retryConnectionMessage(raknet.ErrConnectionLost); got != "Lost connection to the server. Reconnecting.." {
		t.Fatalf("connection-lost retry message = %q", got)
	}
}

func TestRecalculateNearbyUsesCurrentPositionAndSorts(t *testing.T) {
	i := &instance{
		position: [3]float32{10, 0, 0},
		snap: domain.Snapshot{
			NearbyPlayers: []domain.Player{{ID: 1, X: 30}, {ID: 2, X: 11}},
			Vehicles:      []domain.Vehicle{{ID: 1, X: 50}, {ID: 2, X: 12}},
			Objects:       []domain.Object{{ID: 1, X: 13}, {ID: 2, X: 100}},
		},
	}
	recalculateNearby(i)
	if i.snap.NearbyPlayers[0].ID != 2 || i.snap.NearbyPlayers[0].Distance != 1 {
		t.Fatalf("players not recalculated and sorted: %+v", i.snap.NearbyPlayers)
	}
	if i.snap.Vehicles[0].ID != 2 || i.snap.Objects[0].ID != 1 {
		t.Fatalf("entities not sorted: vehicles=%+v objects=%+v", i.snap.Vehicles, i.snap.Objects)
	}
}

func TestPlayerQuitRemovesPlayerFromAllLists(t *testing.T) {
	i := &instance{snap: domain.Snapshot{
		Players:       []domain.Player{{ID: 1}, {ID: 2}},
		NearbyPlayers: []domain.Player{{ID: 1}, {ID: 2}},
	}}
	removePlayerFromSnapshot(i, 1)
	players := i.snap.Players
	nearbyPlayers := i.snap.NearbyPlayers
	vehicles := removeVehicle([]domain.Vehicle{{ID: 1}, {ID: 2}}, 1)
	objects := removeObject([]domain.Object{{ID: 1}, {ID: 2}}, 1)
	if len(players) != 1 || players[0].ID != 2 {
		t.Fatalf("players after removal = %+v", players)
	}
	if len(nearbyPlayers) != 1 || nearbyPlayers[0].ID != 2 {
		t.Fatalf("nearby players after removal = %+v", nearbyPlayers)
	}
	if len(vehicles) != 1 || vehicles[0].ID != 2 {
		t.Fatalf("vehicles after removal = %+v", vehicles)
	}
	if len(objects) != 1 || objects[0].ID != 2 {
		t.Fatalf("objects after removal = %+v", objects)
	}
}

func TestResetConnectionStateClearsTransientInstanceData(t *testing.T) {
	i := &instance{snap: domain.Snapshot{
		Players:       []domain.Player{{ID: 1}},
		NearbyPlayers: []domain.Player{{ID: 2}},
		Vehicles:      []domain.Vehicle{{ID: 3}},
		Objects:       []domain.Object{{ID: 4}},
		TextDraws:     []domain.TextDraw{{ID: 5}},
		ActiveDialog:  &domain.Dialog{ID: 1, Title: "active"},
		Dialogs:       []domain.Dialog{{ID: 2, Title: "deferred"}},
		VehicleState:  domain.VehicleState{InVehicle: true, Passenger: true, VehicleID: 6},
		KeyMask:       7,
		AFK:           true,
		Spawned:       true,
		SpawnReady:    true,
	}}
	i.position = [3]float32{1, 2, 3}
	i.playerID = 8
	resetConnectionState(i)
	if len(i.snap.Players) != 0 || len(i.snap.NearbyPlayers) != 0 || len(i.snap.Vehicles) != 0 || len(i.snap.Objects) != 0 || len(i.snap.TextDraws) != 0 || i.snap.ActiveDialog != nil || len(i.snap.Dialogs) != 0 {
		t.Fatalf("transient collections were not cleared: %+v", i.snap)
	}
	if i.snap.VehicleState.VehicleID != domain.InvalidVehicleID || i.snap.KeyMask != 0 || i.snap.AFK || i.snap.Spawned || i.snap.SpawnReady {
		t.Fatalf("transient state was not reset: %+v", i.snap)
	}
	if i.position != [3]float32{} || i.playerID != domain.InvalidPlayerID {
		t.Fatalf("local connection state was not reset: position=%v playerID=%d", i.position, i.playerID)
	}
}

func TestRawDialogListInputPreservesServerEncodingAndTableOrder(t *testing.T) {
	dialog := &domain.Dialog{
		Style:      dialogStyleTabListHeaders,
		RawMessage: []byte("Header\tValue\n{FF0000}First\tA\n\x80Second\tB"),
	}
	got, ok := rawDialogListInput(dialog, 1)
	if !ok {
		t.Fatal("dialog was not recognized as a list")
	}
	want := []byte("\x80Second")
	if !bytes.Equal(got, want) {
		t.Fatalf("raw input = %v, want %v", got, want)
	}
}

func TestSortPlayersPlacesLocalPlayerFirstAndOthersByID(t *testing.T) {
	players := []domain.Player{{ID: 8}, {ID: 3}, {ID: 7}, {ID: 2}}
	got := sortPlayers(players, 3)
	want := []int{3, 2, 7, 8}
	for index, id := range want {
		if got[index].ID != id {
			t.Fatalf("players = %+v, want IDs %v", got, want)
		}
	}
}
func TestInstanceLifecycle(t *testing.T) {
	m := newManager(t)
	v, e := m.Create(domain.Server{Host: "127.0.0.1", Port: 7777, Nickname: "tester"})
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Delete(v.Server.ID); e != nil {
		t.Fatal(e)
	}
	if len(m.List()) != 0 {
		t.Fatal("instance was not deleted")
	}
}

func TestPluginEventsArePublishedToDedicatedSubscribers(t *testing.T) {
	m := newManager(t)
	events, cleanup, err := m.SubscribePluginEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	m.PublishPluginEvent(plugin.Event{Name: plugin.EventPluginLog, Data: map[string]string{"message": "hello"}})
	select {
	case event := <-events:
		if event.Type != plugin.EventPluginLog {
			t.Fatalf("event type = %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("plugin event was not published")
	}
}

func TestPluginClientEventsUseStableCamelCasePayloads(t *testing.T) {
	playerID := uint16(23)
	data := pluginEventData(samp.Event{Type: samp.EventChat, Data: samp.ChatEvent{PlayerID: &playerID, Text: "hello", Color: 0x11223344}})
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"playerId":23,"text":"hello","color":"#11223344"}`; got != want {
		t.Fatalf("chat plugin payload = %s, want %s", got, want)
	}

	dialog := pluginEventData(samp.Event{Type: samp.EventDialog, Data: samp.DialogEvent{ID: 7, Style: 2, Title: "Title", RawMessage: []byte("private")}})
	encoded, err = json.Marshal(dialog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "RawMessage") || strings.Contains(string(encoded), "private") {
		t.Fatalf("dialog payload leaked decoder internals: %s", encoded)
	}

	movement := samp.MotionEvent{
		TaskID: 7, Kind: samp.MotionWalk, State: samp.MotionStarted,
		Position: [3]float32{1, 2, 3}, Target: [3]float32{4, 5, 6},
	}
	event := samp.Event{Type: samp.EventMovement, Data: movement}
	if got := clientPluginEventName(event); got != plugin.EventClientMovementStart {
		t.Fatalf("movement event name = %q, want %q", got, plugin.EventClientMovementStart)
	}
	encoded, err = json.Marshal(pluginEventData(event))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); got != `{"taskId":7,"kind":"walk","state":"started","x":1,"y":2,"z":3,"targetX":4,"targetY":5,"targetZ":6,"progress":0}` {
		t.Fatalf("movement plugin payload = %s", got)
	}
}

func TestPluginAPIManagesInstancesAndCommands(t *testing.T) {
	m := newManager(t)
	createdValue, err := m.InvokePluginAPI(context.Background(), "", plugin.MethodCreateInstance, json.RawMessage(`{"host":"127.0.0.1","port":7777,"nickname":"plugin"}`))
	if err != nil {
		t.Fatal(err)
	}
	created := createdValue.(domain.Snapshot)
	if created.Server.ID == "" {
		t.Fatal("plugin-created instance has no id")
	}

	if _, err := m.InvokePluginAPI(context.Background(), created.Server.ID, plugin.MethodUpdateInstance, json.RawMessage(`{"host":"127.0.0.1","port":7778,"nickname":"updated"}`)); err != nil {
		t.Fatal(err)
	}
	commandValue, err := m.InvokePluginAPI(context.Background(), created.Server.ID, plugin.MethodAddCommand, json.RawMessage(`{"label":"Help","command":"/help"}`))
	if err != nil {
		t.Fatal(err)
	}
	command := commandValue.(domain.QuickCommand)
	if _, err := m.InvokePluginAPI(context.Background(), created.Server.ID, plugin.MethodDeleteCommand, json.RawMessage(fmt.Sprintf(`{"commandId":%q}`, command.ID))); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InvokePluginAPI(context.Background(), created.Server.ID, plugin.MethodDeleteInstance, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPluginAPIRejectsInvalidParameterTypes(t *testing.T) {
	if _, err := requiredInteger(map[string]any{"mask": "not-a-number"}, "mask", 0, 10); err == nil {
		t.Fatal("invalid integer parameter unexpectedly succeeded")
	}
	if _, err := requiredFloat(map[string]any{"x": "not-a-number"}, "x"); err == nil {
		t.Fatal("invalid float parameter unexpectedly succeeded")
	}
	if _, err := requiredString(map[string]any{"text": " "}, "text"); err == nil {
		t.Fatal("blank string parameter unexpectedly succeeded")
	}
	if _, err := requiredInteger(map[string]any{"before": float64(1 << 53)}, "before", 0, maxPluginSafeInteger); err == nil {
		t.Fatal("integer beyond JavaScript's safe range unexpectedly succeeded")
	}
	if _, err := requiredInteger(map[string]any{"before": float64(^uint64(0) >> 1)}, "before", 0, int64(^uint64(0)>>1)); err == nil {
		t.Fatal("int64 boundary value unexpectedly succeeded")
	}
}

func TestActionContextHonorsParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	ctx, cancel := actionContext(parent)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("action context did not inherit parent cancellation")
	}
}

func TestSyncEpochChangesWhenManagerRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := New(st)
	created, err := first.Create(domain.Server{Host: "127.0.0.1", Port: 7777, Nickname: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	second := New(st)
	restarted, ok := second.Get(created.Server.ID)
	if !ok {
		t.Fatal("persisted instance was not loaded")
	}
	if created.SyncEpoch == "" || created.SyncEpoch == restarted.SyncEpoch {
		t.Fatalf("manager restart did not create a new sync epoch: first=%q second=%q", created.SyncEpoch, restarted.SyncEpoch)
	}
}

func TestQuickCommandsAreIsolatedByInstance(t *testing.T) {
	m := newManager(t)
	first, err := m.Create(domain.Server{Host: "127.0.0.1", Port: 7777, Nickname: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Create(domain.Server{Host: "127.0.0.1", Port: 7778, Nickname: "second"})
	if err != nil {
		t.Fatal(err)
	}
	command, err := m.AddCommand(first.Server.ID, domain.QuickCommand{Label: "first only", Command: "/first"})
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot, _ := m.Get(first.Server.ID)
	secondSnapshot, _ := m.Get(second.Server.ID)
	if len(firstSnapshot.Commands) != 1 || firstSnapshot.Commands[0].ServerID != first.Server.ID {
		t.Fatalf("first instance commands = %+v", firstSnapshot.Commands)
	}
	if len(secondSnapshot.Commands) != 0 {
		t.Fatalf("second instance received foreign commands: %+v", secondSnapshot.Commands)
	}
	if err = m.DeleteCommand(second.Server.ID, command.ID); err == nil {
		t.Fatal("cross-instance command deletion must fail")
	}
	firstSnapshot, _ = m.Get(first.Server.ID)
	if len(firstSnapshot.Commands) != 1 {
		t.Fatal("cross-instance deletion removed the owner's command")
	}
}

func TestSnapshotDoesNotShareMutableState(t *testing.T) {
	i := &instance{snap: domain.Snapshot{
		Chat:         []domain.ChatMessage{{Text: "original"}},
		Commands:     []domain.QuickCommand{{Label: "original"}},
		ActiveDialog: &domain.Dialog{Title: "original", RawMessage: []byte("original")},
	}}
	snapshot := i.snapshot()
	if snapshot.Chat != nil {
		t.Fatal("published snapshots must not include chat history")
	}
	snapshot.Commands[0].Label = "changed"
	snapshot.ActiveDialog.Title = "changed"
	snapshot.ActiveDialog.RawMessage[0] = 'X'
	if i.snap.Chat[0].Text != "original" || i.snap.Commands[0].Label != "original" || i.snap.ActiveDialog.Title != "original" || string(i.snap.ActiveDialog.RawMessage) != "original" {
		t.Fatal("snapshot shares mutable state with the live instance")
	}
}

func TestPublishWorkerCoalescesMovementUpdates(t *testing.T) {
	old := statePublishInterval
	statePublishInterval = 10 * time.Millisecond
	defer func() { statePublishInterval = old }()

	m := newManager(t)
	snapshot, err := m.Create(domain.Server{Host: "127.0.0.1", Port: 7777, Nickname: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	stateEvents, _, cleanup, err := m.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	i, ok := m.find(snapshot.Server.ID)
	if !ok {
		t.Fatal("instance not found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.publishWorker(ctx, snapshot.Server.ID, i)

	for range 100 {
		markDirty(i)
	}
	i.mu.Lock()
	i.snap.AFK = true
	i.mu.Unlock()

	received := 0
	afkSeen := false
	window := time.After(300 * time.Millisecond)
	for {
		select {
		case event := <-stateEvents:
			received++
			patch, ok := event.Data.(domain.InstancePatch)
			if !ok {
				t.Fatalf("event data type = %T, want InstancePatch", event.Data)
			}
			for _, operation := range patch.Operations {
				if operation.Path == "/afk" {
					afkSeen = true
				}
			}
			if received > 20 {
				t.Fatalf("burst produced %d patches, want only a few coalesced ones", received)
			}
		case <-window:
			if !afkSeen {
				t.Fatal("coalesced patch did not include the movement update")
			}
			return
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for coalesced publish")
		}
	}
}

func TestPublishEmitsOnlyChangedSnapshotFieldsWithSequentialRevision(t *testing.T) {
	m := newManager(t)
	snapshot, err := m.Create(domain.Server{Host: "127.0.0.1", Port: 7777, Nickname: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	stateEvents, _, cleanup, err := m.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	i, ok := m.find(snapshot.Server.ID)
	if !ok {
		t.Fatal("instance not found")
	}
	i.mu.Lock()
	i.snap.AFK = true
	i.mu.Unlock()
	m.publish(snapshot.Server.ID, i)
	event := <-stateEvents
	patch, ok := event.Data.(domain.InstancePatch)
	if !ok {
		t.Fatalf("event data type = %T, want InstancePatch", event.Data)
	}
	if patch.Revision != snapshot.Revision+1 || len(patch.Operations) != 1 || patch.Operations[0].Path != "/afk" || patch.Operations[0].Value != true {
		t.Fatalf("unexpected patch: %+v", patch)
	}
	if patch.SyncEpoch == "" || patch.SyncEpoch != snapshot.SyncEpoch {
		t.Fatalf("patch sync epoch = %q, want %q", patch.SyncEpoch, snapshot.SyncEpoch)
	}
	got, _ := m.Get(snapshot.Server.ID)
	if got.Revision != patch.Revision {
		t.Fatalf("snapshot revision = %d, want %d", got.Revision, patch.Revision)
	}
}

func TestEntityCollectionsStopAtProtocolLimits(t *testing.T) {
	players := make([]domain.Player, domain.MaxPlayers)
	vehicles := make([]domain.Vehicle, domain.MaxVehicles)
	objects := make([]domain.Object, domain.MaxObjects)
	textDraws := make([]domain.TextDraw, domain.MaxTextDraws)
	if got := upsertPlayer(players, domain.Player{ID: domain.MaxPlayers + 1}); len(got) != domain.MaxPlayers {
		t.Fatalf("players grew to %d", len(got))
	}
	if got := upsertVehicle(vehicles, domain.Vehicle{ID: domain.MaxVehicles + 1}); len(got) != domain.MaxVehicles {
		t.Fatalf("vehicles grew to %d", len(got))
	}
	if got := upsertObject(objects, domain.Object{ID: domain.MaxObjects + 1}); len(got) != domain.MaxObjects {
		t.Fatalf("objects grew to %d", len(got))
	}
	if got := upsertTextDraw(textDraws, domain.TextDraw{ID: domain.MaxTextDraws + 1}); len(got) != domain.MaxTextDraws {
		t.Fatalf("text draws grew to %d", len(got))
	}
}

func TestSubscriberLimitAndCleanup(t *testing.T) {
	m := newManager(t)
	cleanups := make([]func(), 0, maxSubscribers)
	for range maxSubscribers {
		_, _, cleanup, err := m.Subscribe()
		if err != nil {
			t.Fatal(err)
		}
		cleanups = append(cleanups, cleanup)
	}
	if _, _, _, err := m.Subscribe(); err == nil {
		t.Fatal("subscriber limit was not enforced")
	}
	cleanups[0]()
	_, _, cleanup, err := m.Subscribe()
	if err != nil {
		t.Fatalf("released subscriber slot was not reusable: %v", err)
	}
	cleanup()
	for _, release := range cleanups[1:] {
		release()
	}
}

func TestChatEventsHaveAnIndependentQueue(t *testing.T) {
	m := newManager(t)
	stateEvents, chatEvents, cleanup, err := m.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for range stateEventQueueSize + 1 {
		m.emit(domain.Event{Type: domain.EventInstanceUpdated})
	}
	m.emit(domain.Event{Type: domain.EventChatMessage})
	select {
	case event := <-chatEvents:
		if event.Type != domain.EventChatMessage {
			t.Fatalf("unexpected chat event: %+v", event)
		}
	default:
		t.Fatal("chat event was blocked by state events")
	}
	if len(stateEvents) != stateEventQueueSize {
		t.Fatalf("state queue size = %d", len(stateEvents))
	}
}
func TestCreateValidatesInput(t *testing.T) {
	m := newManager(t)
	if _, e := m.Create(domain.Server{Port: 70000}); e == nil {
		t.Fatal("expected validation error")
	}
}
func TestPlayerNameUsesKnownPlayerAndVisibleFallback(t *testing.T) {
	snapshot := domain.Snapshot{Players: []domain.Player{{ID: 7, Name: "Alice"}}}
	if got := playerName(snapshot, 7); got != "Alice" {
		t.Fatalf("known player name = %q", got)
	}
	if got := playerName(snapshot, 8); got != "Player 8" {
		t.Fatalf("fallback player name = %q", got)
	}
}

func TestInstanceLogsAreIsolatedAndResetOnConnect(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")
	m := newManager(t)
	m.logDir = logDir
	first := &instance{snap: domain.Snapshot{Server: domain.Server{ID: "first"}}}
	second := &instance{snap: domain.Snapshot{Server: domain.Server{ID: "second"}}}
	m.appendChat(first, "first message", defaultChatColor)
	m.appendChat(second, "second message", errorChatColor)
	firstData, err := os.ReadFile(m.instanceLogPath("first"))
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(m.instanceLogPath("second"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(firstData, []byte("second message")) || bytes.Contains(secondData, []byte("first message")) {
		t.Fatal("instance logs are not isolated")
	}
	var entry instanceLogEntry
	if err = json.Unmarshal(bytes.TrimSpace(firstData), &entry); err != nil || entry.Text != "first message" {
		t.Fatalf("invalid log entry: entry=%+v error=%v", entry, err)
	}
	if err = m.resetInstanceLog("first"); err != nil {
		t.Fatal(err)
	}
	cleared, err := os.ReadFile(m.instanceLogPath("first"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared) != 0 {
		t.Fatalf("reset log contains %d bytes", len(cleared))
	}
}

func TestChatLogPaginationReadsNewestFirstWithoutLoadingWholeFile(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")
	m := newManager(t)
	m.logDir = logDir
	snapshot, err := m.Create(domain.Server{Host: "127.0.0.1", Port: 7777, Nickname: "test", Encoding: domain.EncodingUTF8})
	if err != nil {
		t.Fatal(err)
	}
	i, _ := m.find(snapshot.Server.ID)
	for index := 1; index <= 5; index++ {
		m.appendChat(i, fmt.Sprintf("message %d", index), defaultChatColor)
	}
	page, err := m.Chat(snapshot.Server.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Text != "message 4" || page.Items[1].Text != "message 5" || page.NextBefore == 0 {
		t.Fatalf("unexpected first page: %+v", page)
	}
	page, err = m.Chat(snapshot.Server.ID, page.NextBefore, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Text != "message 2" || page.Items[1].Text != "message 3" {
		t.Fatalf("unexpected second page: %+v", page)
	}
	for index := 6; index <= 80; index++ {
		m.appendChat(i, fmt.Sprintf("message %d %s", index, strings.Repeat("x", 96)), defaultChatColor)
	}
	before := int64(0)
	count := 0
	for {
		page, err = m.Chat(snapshot.Server.ID, before, 7)
		if err != nil {
			t.Fatal(err)
		}
		count += len(page.Items)
		if page.NextBefore == 0 {
			break
		}
		before = page.NextBefore
	}
	if count != 80 {
		t.Fatalf("paginated message count = %d", count)
	}
}
