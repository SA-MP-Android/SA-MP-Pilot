package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/store"
	"os"
	"path/filepath"
	"testing"
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

func TestMovePlayerFirstPreservesOtherPlayerOrder(t *testing.T) {
	players := []domain.Player{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}
	got := movePlayerFirst(players, 3)
	want := []int{3, 1, 2, 4}
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
	snapshot.Chat[0].Text = "changed"
	snapshot.Commands[0].Label = "changed"
	snapshot.ActiveDialog.Title = "changed"
	snapshot.ActiveDialog.RawMessage[0] = 'X'
	if i.snap.Chat[0].Text != "original" || i.snap.Commands[0].Label != "original" || i.snap.ActiveDialog.Title != "original" || string(i.snap.ActiveDialog.RawMessage) != "original" {
		t.Fatal("snapshot shares mutable state with the live instance")
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
