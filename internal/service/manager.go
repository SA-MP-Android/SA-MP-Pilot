package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/samp"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/store"
	"github.com/SA-MP-Android/SA-MP-Pilot/plugin"
	"github.com/google/uuid"
)

// statePublishInterval is the minimum gap between state patches that carry
// movement-heavy data (player/vehicle sync, positions). Rapid sync events are
// coalesced by publishWorker into a single patch, cutting websocket traffic and
// slowing the nearby tab without delaying chat or dialogs.
var statePublishInterval = 500 * time.Millisecond

const (
	actionTimeout                   = 5 * time.Second
	maxPluginSafeInteger      int64 = 1<<53 - 1
	reconnectDelay                  = 2 * time.Second
	defaultChatColor                = "#ffffffff"
	errorChatColor                  = "#ff6b6bff"
	dialogStyleList                 = 2
	dialogStyleTabList              = 4
	dialogStyleTabListHeaders       = 5
	maxSubscribers                  = 16
	stateEventQueueSize             = 2
	chatEventQueueSize              = 64
	maxHostBytes                    = 253
	maxNicknameBytes                = 96
	maxPasswordBytes                = 128
	maxCommandLabelBytes            = 128
	maxCommandTextBytes             = 1024
)

var serverInfoRefreshInterval = 5 * time.Second

type instance struct {
	mu        sync.RWMutex
	snap      domain.Snapshot
	published domain.Snapshot
	cancel    context.CancelFunc
	client    *samp.Client
	position  [3]float32
	playerID  int
	dirty     chan struct{}
}
type subscriber struct {
	state chan domain.Event
	chat  chan domain.Event
}
type pluginSubscriber struct {
	events chan domain.Event
}
type Manager struct {
	mu                sync.RWMutex
	instances         map[string]*instance
	store             *store.Store
	subscribers       map[*subscriber]struct{}
	pluginSubscribers map[*pluginSubscriber]struct{}
	msgID             atomic.Int64
	logDir            string
	syncEpoch         string
	pluginMu          sync.RWMutex
	pluginSink        plugin.EventSink
	queryServerInfo   func(context.Context, string, int) (samp.Info, error)
}

type Option func(*Manager)

func WithLogDir(path string) Option {
	return func(manager *Manager) { manager.logDir = path }
}

// SetPluginSink attaches the external plugin host. It is separate from New
// because the host calls back into Manager for API requests.
func (m *Manager) SetPluginSink(sink plugin.EventSink) {
	m.pluginMu.Lock()
	m.pluginSink = sink
	m.pluginMu.Unlock()
}

func New(st *store.Store, options ...Option) *Manager {
	m := &Manager{instances: map[string]*instance{}, store: st, subscribers: map[*subscriber]struct{}{}, pluginSubscribers: map[*pluginSubscriber]struct{}{}, syncEpoch: uuid.NewString(), queryServerInfo: samp.Query}
	for _, option := range options {
		option(m)
	}
	data := st.Data()
	for _, s := range data.Servers {
		if len(m.instances) >= domain.MaxInstances {
			break
		}
		i := newInstance(s, m.syncEpoch)
		for _, command := range data.Commands {
			if command.ServerID == s.ID && len(i.snap.Commands) < domain.MaxCommandsPerInstance {
				i.snap.Commands = append(i.snap.Commands, command)
			}
		}
		// Commands are loaded before any client can observe an update. Keep the
		// publication baseline aligned with the fully hydrated snapshot so the
		// first runtime update only reports fields that actually changed.
		i.published = cloneSnapshot(i.snap)
		m.instances[s.ID] = i
	}
	return m
}
func (m *Manager) StartAutoConnect() {
	for _, snapshot := range m.List() {
		if snapshot.Server.AutoConnect {
			_ = m.Connect(snapshot.Server.ID)
		}
	}
}

// RefreshGPCI rotates the global per-installation client identifier. Active
// clients keep their existing handshake value and use the new value after the
// next connection attempt.
func (m *Manager) RefreshGPCI() (string, error) {
	return m.store.RefreshGPCI()
}

func (m *Manager) Close() {
	m.mu.RLock()
	instances := make([]*instance, 0, len(m.instances))
	for _, i := range m.instances {
		instances = append(instances, i)
	}
	m.mu.RUnlock()
	for _, i := range instances {
		i.mu.Lock()
		if i.cancel != nil {
			i.cancel()
		}
		if i.client != nil {
			_ = i.client.Close()
		}
		i.mu.Unlock()
	}
}
func (m *Manager) Update(id string, s domain.Server) (domain.Snapshot, error) {
	i, ok := m.find(id)
	if !ok {
		return domain.Snapshot{}, errors.New("instance not found")
	}
	if err := validateServer(s); err != nil {
		return domain.Snapshot{}, err
	}
	s.ID = id
	if err := m.store.Update(func(d *store.Data) error {
		for index := range d.Servers {
			if d.Servers[index].ID == id {
				d.Servers[index] = s
				return nil
			}
		}
		return errors.New("instance not found")
	}); err != nil {
		return domain.Snapshot{}, err
	}
	i.mu.Lock()
	i.snap.Server = s
	i.mu.Unlock()
	m.publish(id, i)
	return i.snapshot(), nil
}
func (m *Manager) AddCommand(id string, command domain.QuickCommand) (domain.QuickCommand, error) {
	i, ok := m.find(id)
	if !ok {
		return domain.QuickCommand{}, errors.New("instance not found")
	}
	command.Label = strings.TrimSpace(command.Label)
	command.Command = strings.TrimSpace(command.Command)
	if command.Label == "" || command.Command == "" {
		return domain.QuickCommand{}, errors.New("label and command are required")
	}
	if len(command.Label) > maxCommandLabelBytes || len(command.Command) > maxCommandTextBytes {
		return domain.QuickCommand{}, errors.New("command is too long")
	}
	i.mu.Lock()
	if len(i.snap.Commands) >= domain.MaxCommandsPerInstance {
		i.mu.Unlock()
		return domain.QuickCommand{}, errors.New("command limit reached")
	}
	command.ID, command.ServerID = uuid.NewString(), id
	if err := m.store.Update(func(d *store.Data) error { d.Commands = append(d.Commands, command); return nil }); err != nil {
		i.mu.Unlock()
		return domain.QuickCommand{}, err
	}
	i.snap.Commands = append(i.snap.Commands, command)
	i.mu.Unlock()
	m.publish(id, i)
	return command, nil
}
func (m *Manager) DeleteCommand(id, commandID string) error {
	i, ok := m.find(id)
	if !ok {
		return errors.New("instance not found")
	}
	found := false
	if err := m.store.Update(func(d *store.Data) error {
		out := d.Commands[:0]
		for _, command := range d.Commands {
			if command.ID == commandID && command.ServerID == id {
				found = true
				continue
			}
			out = append(out, command)
		}
		d.Commands = out
		return nil
	}); err != nil {
		return err
	}
	if !found {
		return errors.New("command not found")
	}
	i.mu.Lock()
	commands := i.snap.Commands[:0]
	for _, command := range i.snap.Commands {
		if command.ID != commandID {
			commands = append(commands, command)
		}
	}
	i.snap.Commands = commands
	i.mu.Unlock()
	m.publish(id, i)
	return nil
}
func newInstance(s domain.Server, syncEpoch string) *instance {
	snapshot := domain.Snapshot{Revision: 1, SyncEpoch: syncEpoch, Server: s, Connection: domain.Connection{Status: domain.StatusDisconnected}, Chat: []domain.ChatMessage{}, Players: []domain.Player{}, NearbyPlayers: []domain.Player{}, Vehicles: []domain.Vehicle{}, Objects: []domain.Object{}, TextDraws: []domain.TextDraw{}, Dialogs: []domain.Dialog{}, Commands: []domain.QuickCommand{}, LocalPlayer: domain.LocalPlayer{ID: domain.InvalidPlayerID, LifeState: domain.LifeStateDisconnected}, VehicleState: domain.VehicleState{VehicleID: domain.InvalidVehicleID}}
	return &instance{snap: snapshot, published: cloneSnapshot(snapshot), playerID: domain.InvalidPlayerID, dirty: make(chan struct{}, 1)}
}
func (m *Manager) List() []domain.Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]domain.Snapshot, 0, len(m.instances))
	for _, i := range m.instances {
		out = append(out, i.snapshot())
	}
	return out
}
func (m *Manager) Get(id string) (domain.Snapshot, bool) {
	m.mu.RLock()
	i, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return domain.Snapshot{}, false
	}
	return i.snapshot(), true
}
func (i *instance) snapshot() domain.Snapshot {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return cloneSnapshot(i.snap)
}
func (m *Manager) Create(s domain.Server) (domain.Snapshot, error) {
	if err := validateServer(s); err != nil {
		return domain.Snapshot{}, err
	}
	m.mu.Lock()
	if len(m.instances) >= domain.MaxInstances {
		m.mu.Unlock()
		return domain.Snapshot{}, errors.New("instance limit reached")
	}
	s.ID = uuid.NewString()
	if s.Encoding == "" {
		s.Encoding = domain.EncodingUTF8
	}
	i := newInstance(s, m.syncEpoch)
	if err := m.store.Update(func(d *store.Data) error { d.Servers = append(d.Servers, s); return nil }); err != nil {
		m.mu.Unlock()
		return domain.Snapshot{}, err
	}
	m.instances[s.ID] = i
	m.mu.Unlock()
	snapshot := i.snapshot()
	m.emit(domain.Event{Type: domain.EventInstanceCreated, InstanceID: s.ID, Data: snapshot})
	return snapshot, nil
}

func validateServer(server domain.Server) error {
	if server.Host == "" || server.Nickname == "" || server.Port < 1 || server.Port > 65535 {
		return errors.New("host, nickname and a valid port are required")
	}
	if len(server.Host) > maxHostBytes || len(server.Nickname) > maxNicknameBytes || len(server.Password) > maxPasswordBytes {
		return errors.New("server configuration is too long")
	}
	return nil
}
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	i, ok := m.instances[id]
	if ok {
		delete(m.instances, id)
	}
	m.mu.Unlock()
	if !ok {
		return errors.New("instance not found")
	}
	i.mu.Lock()
	cancel := i.cancel
	client := i.client
	i.cancel = nil
	i.client = nil
	i.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		_ = client.Close()
	}
	if err := m.store.Update(func(d *store.Data) error {
		d.Servers = filterServers(d.Servers, id)
		d.Commands = filterCommands(d.Commands, id)
		return nil
	}); err != nil {
		return err
	}
	m.emit(domain.Event{Type: domain.EventInstanceDeleted, InstanceID: id})
	return nil
}
func filterServers(v []domain.Server, id string) []domain.Server {
	o := v[:0]
	for _, x := range v {
		if x.ID != id {
			o = append(o, x)
		}
	}
	clear(v[len(o):])
	return o
}
func filterCommands(v []domain.QuickCommand, id string) []domain.QuickCommand {
	o := v[:0]
	for _, x := range v {
		if x.ServerID != id {
			o = append(o, x)
		}
	}
	clear(v[len(o):])
	return o
}
func (m *Manager) Connect(id string) error {
	i, ok := m.find(id)
	if !ok {
		return errors.New("instance not found")
	}
	if err := m.resetInstanceLog(id); err != nil {
		return fmt.Errorf("reset instance log: %w", err)
	}
	i.mu.Lock()
	clear(i.snap.Chat)
	i.snap.Chat = i.snap.Chat[:0]
	i.mu.Unlock()
	m.emit(domain.Event{Type: domain.EventChatReset, InstanceID: id})
	i.mu.Lock()
	oldCancel := i.cancel
	oldClient := i.client
	i.cancel = nil
	i.client = nil
	ctx, cancel := context.WithCancel(context.Background())
	i.cancel = cancel
	i.snap.Connection = domain.Connection{Status: domain.StatusConnecting}
	resetConnectionState(i)
	s := i.snap.Server
	m.appendChat(i, fmt.Sprintf("Connecting to %s:%d...", s.Host, s.Port), defaultChatColor)
	i.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}
	if oldClient != nil {
		_ = oldClient.Close()
	}
	m.publish(id, i)
	go m.connect(ctx, id, i, s)
	go m.publishWorker(ctx, id, i)
	return nil
}
func (m *Manager) connect(ctx context.Context, id string, i *instance, s domain.Server) {
	for {
		err := m.connectAttempt(ctx, id, i, s)
		if ctx.Err() != nil {
			return
		}
		message := retryConnectionMessage(err)
		i.mu.Lock()
		i.client = nil
		i.snap.Connection = domain.Connection{
			Status: domain.StatusError,
			Error:  connectionMessage(err),
		}
		m.appendChat(i, message, errorChatColor)
		i.mu.Unlock()
		m.publish(id, i)
		timer := time.NewTimer(reconnectDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		i.mu.Lock()
		m.appendChat(i, fmt.Sprintf("Connecting to %s:%d...", s.Host, s.Port), defaultChatColor)
		i.mu.Unlock()
		m.publish(id, i)
	}
}

func (m *Manager) connectAttempt(ctx context.Context, id string, i *instance, s domain.Server) error {
	address := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	clientGPCI, err := m.store.EnsureGPCI()
	if err != nil {
		return fmt.Errorf("load gpci: %w", err)
	}
	client, err := samp.DialClientWithOptions(
		ctx,
		address,
		s.Nickname,
		s.Password,
		string(s.Encoding),
		samp.ClientOptions{
			EmulatePCClientCheck: s.EmulatePCClientCheck,
			RespawnPolicy:        samp.RespawnPolicyAutomatic,
			GPCI:                 clientGPCI,
		},
	)
	if err != nil {
		return err
	}
	i.mu.Lock()
	if ctx.Err() != nil {
		i.mu.Unlock()
		_ = client.Close()
		return ctx.Err()
	}
	i.client = client
	i.mu.Unlock()
	info, _ := m.queryServerInfo(ctx, s.Host, s.Port)
	go m.refreshServerInfo(ctx, id, i, client, s)
	var disconnectErr error
	for event := range client.Events() {
		// Apply the event to the in-memory snapshot before notifying plugins.
		// Handlers may immediately read the snapshot or respond to a dialog.
		i.mu.Lock()
		publishSnapshot := true
		switch event.Type {
		case samp.EventJoined:
			localPlayer := event.Data.(samp.PlayerEvent)
			i.playerID = int(localPlayer.ID)
			i.snap.LocalPlayer = domain.LocalPlayer{ID: i.playerID, Health: localPlayer.Health, Armour: localPlayer.Armour, LifeState: domain.LifeStateClassSelection}
			i.snap.SpawnReady = false
			i.snap.Players = upsertPlayer(i.snap.Players, domain.Player{ID: int(localPlayer.ID), Name: s.Nickname, Health: localPlayer.Health, Armour: localPlayer.Armour})
			i.snap.Players = sortPlayers(i.snap.Players, i.playerID)
			now := time.Now()
			name := samp.DecodeServerText(string(s.Encoding), info.Hostname)
			if name == "" {
				name = address
			}
			i.snap.Connection = domain.Connection{Status: domain.StatusConnected, ServerName: name, PlayerCount: info.Players, MaxPlayers: info.MaxPlayers, Since: &now}
			m.appendChat(i, fmt.Sprintf("Connected to %s (%d/%d)", name, info.Players, info.MaxPlayers), "#8bd5ca")
		case samp.EventChat:
			chat := event.Data.(samp.ChatEvent)
			text := chat.Text
			if chat.PlayerID != nil {
				name := playerName(i.snap, int(*chat.PlayerID))
				text = fmt.Sprintf("%s: %s", name, chat.Text)
			}
			color := defaultChatColor
			if chat.Color != 0 {
				color = fmt.Sprintf("#%08x", chat.Color)
			}
			m.appendChat(i, text, color)
			publishSnapshot = false
		case samp.EventPlayerJoin:
			p := event.Data.(samp.PlayerEvent)
			existing := findPlayer(i.snap.Players, int(p.ID))
			existing.ID, existing.Name = int(p.ID), p.Name
			if p.HasColor && p.Color != 0 {
				existing.Color = colorHex(p.Color)
			}
			i.snap.Players = upsertPlayer(i.snap.Players, existing)
			i.snap.Players = sortPlayers(i.snap.Players, i.playerID)
		case samp.EventPlayerQuit:
			p := event.Data.(samp.PlayerEvent)
			removePlayerFromSnapshot(i, int(p.ID))
		case samp.EventScores:
			for _, p := range event.Data.([]samp.PlayerEvent) {
				existing := findPlayer(i.snap.Players, int(p.ID))
				existing.ID = int(p.ID)
				existing.Score = int(p.Score)
				existing.Ping = int(p.Ping)
				i.snap.Players = upsertPlayer(i.snap.Players, existing)
			}
			i.snap.Players = sortPlayers(i.snap.Players, i.playerID)
		case samp.EventDialog:
			d := event.Data.(samp.DialogEvent)
			if d.ID < 0 {
				i.snap.ActiveDialog = nil
			} else {
				dialog := domain.Dialog{ID: int(d.ID), Style: int(d.Style), Title: d.Title, Message: d.Message, Button1: d.Button1, Button2: d.Button2, ReceivedAt: time.Now(), RawMessage: d.RawMessage}
				i.snap.ActiveDialog = &dialog
			}
		case samp.EventProtocolError:
			m.appendChat(i, fmt.Sprintf("Protocol error: %v", event.Data), "#ff6b6b")
			publishSnapshot = false
		case samp.EventTextDrawShow:
			v := event.Data.(samp.TextDrawEvent)
			i.snap.TextDraws = upsertTextDraw(i.snap.TextDraws, domain.TextDraw{ID: int(v.ID), Text: v.Text, Style: int(v.Style), LetterColor: colorHex(v.LetterColor), BoxColor: colorHex(v.BoxColor), BackgroundColor: colorHex(v.BackgroundColor), Selectable: v.Selectable != 0, X: v.X, Y: v.Y, LetterWidth: v.LetterWidth, LetterHeight: v.LetterHeight})
		case samp.EventTextDrawHide:
			v := event.Data.(samp.TextDrawEvent)
			i.snap.TextDraws = removeTextDraw(i.snap.TextDraws, int(v.ID))
		case samp.EventTextDrawText:
			v := event.Data.(samp.TextDrawEvent)
			for x := range i.snap.TextDraws {
				if i.snap.TextDraws[x].ID == int(v.ID) {
					i.snap.TextDraws[x].Text = v.Text
				}
			}
		case samp.EventObjectAdd:
			v := event.Data.(samp.ObjectEvent)
			i.snap.Objects = upsertObject(i.snap.Objects, domain.Object{ID: int(v.ID), ModelID: int(v.ModelID), X: v.X, Y: v.Y, Z: v.Z, Distance: distance(i.position, [3]float32{v.X, v.Y, v.Z})})
			sortNearby(i)
		case samp.EventObjectRemove:
			v := event.Data.(samp.ObjectEvent)
			i.snap.Objects = removeObject(i.snap.Objects, int(v.ID))
		case samp.EventVehicleAdd:
			v := event.Data.(samp.VehicleEvent)
			i.snap.Vehicles = upsertVehicle(i.snap.Vehicles, domain.Vehicle{ID: int(v.ID), ModelID: int(v.ModelID), X: v.X, Y: v.Y, Z: v.Z, Health: v.Health, Distance: distance(i.position, [3]float32{v.X, v.Y, v.Z})})
			if i.snap.VehicleState.InVehicle && i.snap.VehicleState.VehicleID == int(v.ID) {
				i.snap.VehicleState.Health = v.Health
				i.snap.VehicleState.HealthKnown = true
			}
			sortNearby(i)
		case samp.EventVehicleRemove:
			v := event.Data.(samp.VehicleEvent)
			i.snap.Vehicles = removeVehicle(i.snap.Vehicles, int(v.ID))
			if i.snap.VehicleState.InVehicle && i.snap.VehicleState.VehicleID == int(v.ID) {
				i.snap.VehicleState = domain.VehicleState{VehicleID: domain.InvalidVehicleID}
			}
		case samp.EventPlayerSync:
			v := event.Data.(samp.PlayerEvent)
			player := findPlayer(i.snap.Players, int(v.ID))
			player.ID, player.X, player.Y, player.Z = int(v.ID), v.X, v.Y, v.Z
			if int(v.ID) == i.playerID {
				// Player sync health is quantized on the wire. Keep the local
				// player's authoritative float values from health RPCs/spawn.
				player.Health, player.Armour = i.snap.LocalPlayer.Health, i.snap.LocalPlayer.Armour
			} else {
				player.Health, player.Armour = v.Health, v.Armour
			}
			if v.HasSkin {
				player.Skin = int(v.Skin)
			}
			if v.HasTeam {
				player.Team = int(v.Team)
			}
			if v.HasRotation {
				player.Rotation = v.Rotation
			}
			if v.HasColor && v.Color != 0 {
				player.Color = colorHex(v.Color)
			}
			player.Distance = distance(i.position, [3]float32{v.X, v.Y, v.Z})
			i.snap.Players = upsertPlayer(i.snap.Players, player)
			i.snap.Players = sortPlayers(i.snap.Players, i.playerID)
			i.snap.NearbyPlayers = upsertPlayer(i.snap.NearbyPlayers, player)
			sortNearby(i)
			publishSnapshot = false
			markDirty(i)
		case samp.EventPosition:
			i.position = event.Data.([3]float32)
			i.snap.VehicleState = domain.VehicleState{VehicleID: domain.InvalidVehicleID}
			recalculateNearby(i)
			publishSnapshot = false
			markDirty(i)
		case samp.EventMovement:
			v := event.Data.(samp.MotionEvent)
			i.position = v.Position
			local := findPlayer(i.snap.Players, i.playerID)
			local.ID, local.X, local.Y, local.Z = i.playerID, v.Position[0], v.Position[1], v.Position[2]
			i.snap.Players = upsertPlayer(i.snap.Players, local)
			i.snap.Players = sortPlayers(i.snap.Players, i.playerID)
			recalculateNearby(i)
			publishSnapshot = false
			markDirty(i)
		case samp.EventVehicleState:
			applyVehicleState(i, event.Data.(samp.VehicleStateEvent))
		case samp.EventPlayerHealth:
			applyLocalPlayerHealth(i, event.Data.(samp.PlayerHealthEvent))
		case samp.EventPlayerLifeState:
			applyLocalPlayerLifeState(i, event.Data.(samp.PlayerLifeStateEvent))
		case samp.EventPlayerDeath:
			applyLocalPlayerDeath(i)
		case samp.EventSpawned:
			i.snap.Spawned = true
			i.snap.SpawnReady = false
			i.snap.LocalPlayer.LifeState = domain.LifeStateSpawned
			if v, ok := event.Data.(samp.SpawnedEvent); ok {
				applyLocalPlayerHealth(i, samp.PlayerHealthEvent{Health: v.Health, Armour: v.Armour})
			}
		case samp.EventAppearance:
			v := event.Data.(samp.PlayerEvent)
			if int(v.ID) == i.playerID && v.HasPosition && v.HasSkin && v.HasTeam && v.HasRotation {
				i.snap.SpawnReady = true
				if !i.snap.Spawned && i.snap.LocalPlayer.LifeState != domain.LifeStateDead && i.snap.LocalPlayer.LifeState != domain.LifeStateSpawnRequestPending {
					i.snap.LocalPlayer.LifeState = domain.LifeStateSpawnReady
				}
			}
			player := findPlayer(i.snap.Players, int(v.ID))
			player.ID = int(v.ID)
			if v.HasSkin {
				player.Skin = int(v.Skin)
			}
			if v.HasTeam {
				player.Team = int(v.Team)
			}
			if v.HasRotation {
				player.Rotation = v.Rotation
			}
			if v.HasColor && v.Color != 0 {
				player.Color = colorHex(v.Color)
			}
			if v.HasPosition {
				player.X, player.Y, player.Z = v.X, v.Y, v.Z
				i.position = [3]float32{v.X, v.Y, v.Z}
			}
			i.snap.Players = upsertPlayer(i.snap.Players, player)
			i.snap.Players = sortPlayers(i.snap.Players, i.playerID)
			recalculateNearby(i)
			publishSnapshot = false
			markDirty(i)
		case samp.EventVehicleSync:
			v := event.Data.(samp.VehicleEvent)
			vehicle := findVehicle(i.snap.Vehicles, int(v.ID))
			vehicle.ID, vehicle.X, vehicle.Y, vehicle.Z, vehicle.Health = int(v.ID), v.X, v.Y, v.Z, v.Health
			vehicle.Distance = distance(i.position, [3]float32{v.X, v.Y, v.Z})
			vehicle.Occupied = true
			i.snap.Vehicles = upsertVehicle(i.snap.Vehicles, vehicle)
			if i.snap.VehicleState.InVehicle && i.snap.VehicleState.VehicleID == int(v.ID) {
				i.snap.VehicleState.Health = v.Health
				i.snap.VehicleState.HealthKnown = true
			}
			sortNearby(i)
			publishSnapshot = false
			markDirty(i)
		case samp.EventVehicleHealth:
			v := event.Data.(samp.VehicleHealthEvent)
			vehicle := findVehicle(i.snap.Vehicles, int(v.ID))
			vehicle.ID, vehicle.Health = int(v.ID), v.Health
			i.snap.Vehicles = upsertVehicle(i.snap.Vehicles, vehicle)
			if i.snap.VehicleState.InVehicle && i.snap.VehicleState.VehicleID == int(v.ID) {
				i.snap.VehicleState.Health = v.Health
				i.snap.VehicleState.HealthKnown = true
			}
		case samp.EventDisconnected:
			if i.client == client {
				i.client = nil
			}
			if ctx.Err() == nil {
				disconnectErr, _ = event.Data.(error)
				i.snap.Connection = domain.Connection{
					Status: domain.StatusError,
					Error:  connectionMessage(disconnectErr),
				}
				resetConnectionState(i)
			}
		}
		i.mu.Unlock()
		if publishSnapshot {
			m.publish(id, i)
		}
		m.emitClientPluginEvent(id, event)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return disconnectErr
}

// refreshServerInfo keeps the server-reported player totals current while the
// gameplay connection is alive. The SA-MP protocol sends join/quit events for
// individual players, but those events do not carry the server's max-player
// value and cannot be relied on as a complete server count.
func (m *Manager) refreshServerInfo(ctx context.Context, id string, i *instance, client *samp.Client, s domain.Server) {
	ticker := time.NewTicker(serverInfoRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := m.queryServerInfo(ctx, s.Host, s.Port)
			if err != nil {
				continue
			}

			i.mu.Lock()
			if ctx.Err() != nil || i.client != client {
				i.mu.Unlock()
				return
			}
			if i.snap.Connection.Status != domain.StatusConnected {
				i.mu.Unlock()
				continue
			}
			changed := i.snap.Connection.PlayerCount != info.Players || i.snap.Connection.MaxPlayers != info.MaxPlayers
			i.snap.Connection.PlayerCount = info.Players
			i.snap.Connection.MaxPlayers = info.MaxPlayers
			i.mu.Unlock()
			if changed {
				m.publish(id, i)
			}
		}
	}
}

func retryConnectionMessage(err error) string {
	switch {
	case errors.Is(err, raknet.ErrServerFull):
		return "The server is full. Retrying..."
	case errors.Is(err, raknet.ErrConnectionLost):
		return "Lost connection to the server. Reconnecting.."
	case errors.Is(err, raknet.ErrServerClosed):
		return "Server closed the connection. Reconnecting..."
	default:
		return connectionMessage(err) + " Retrying..."
	}
}
func connectionMessage(err error) string {
	switch {
	case errors.Is(err, samp.ErrBadVersion):
		return "Connection rejected: incorrect client version."
	case errors.Is(err, samp.ErrBadNickname):
		return "Connection rejected: nickname must be 3-20 characters and use only a-z, A-Z, or 0-9."
	case errors.Is(err, samp.ErrBadMod):
		return "Connection rejected: bad client mod version."
	case errors.Is(err, samp.ErrBadPlayerID):
		return "Connection rejected: unable to allocate a player slot."
	case errors.Is(err, samp.ErrConnectionRejected):
		return "Connection rejected by the server."
	case errors.Is(err, raknet.ErrServerFull):
		return "The server is full."
	case errors.Is(err, raknet.ErrServerClosed):
		return "Server closed the connection."
	case errors.Is(err, raknet.ErrConnectionLost):
		return "Lost connection to the server."
	case errors.Is(err, raknet.ErrEncryption):
		return "Failed to initialize encryption."
	case errors.Is(err, raknet.ErrBanned):
		return "You are banned from this server."
	case errors.Is(err, raknet.ErrInvalidPassword):
		return "Wrong server password."
	case errors.Is(err, raknet.ErrAttemptFailed), errors.Is(err, context.DeadlineExceeded):
		return "The server didn't respond."
	case err == nil:
		return "Server closed the connection."
	default:
		return err.Error()
	}
}
func (m *Manager) appendChat(i *instance, text, color string) {
	now := time.Now()
	message := domain.ChatMessage{ID: m.msgID.Add(1), Text: text, Color: color, At: now}
	i.snap.Chat = append(i.snap.Chat, message)
	m.writeInstanceLog(i.snap.Server.ID, message)
	m.emit(domain.Event{Type: domain.EventChatMessage, InstanceID: i.snap.Server.ID, Data: message})
	if len(i.snap.Chat) > domain.MaxChatMessages {
		dropped := len(i.snap.Chat) - domain.MaxChatMessages
		copy(i.snap.Chat, i.snap.Chat[dropped:])
		clear(i.snap.Chat[domain.MaxChatMessages:])
		i.snap.Chat = i.snap.Chat[:domain.MaxChatMessages]
	}
}
func upsertPlayer(v []domain.Player, p domain.Player) []domain.Player {
	for x := range v {
		if v[x].ID == p.ID {
			v[x] = p
			return v
		}
	}
	if len(v) >= domain.MaxPlayers {
		return v
	}
	return append(v, p)
}
func removePlayer(v []domain.Player, id int) []domain.Player {
	out := v[:0]
	for _, p := range v {
		if p.ID != id {
			out = append(out, p)
		}
	}
	clear(v[len(out):])
	return out
}
func removePlayerFromSnapshot(i *instance, id int) {
	i.snap.Players = removePlayer(i.snap.Players, id)
	i.snap.NearbyPlayers = removePlayer(i.snap.NearbyPlayers, id)
}
func applyLocalPlayerHealth(i *instance, value samp.PlayerHealthEvent) {
	lifeState := i.snap.LocalPlayer.LifeState
	i.snap.LocalPlayer = domain.LocalPlayer{ID: i.playerID, Health: value.Health, Armour: value.Armour, LifeState: lifeState}
	local := findPlayer(i.snap.Players, i.playerID)
	local.ID, local.Health, local.Armour = i.playerID, value.Health, value.Armour
	i.snap.Players = upsertPlayer(i.snap.Players, local)
	i.snap.Players = sortPlayers(i.snap.Players, i.playerID)
	updatePlayerHealth(i.snap.NearbyPlayers, i.playerID, value.Health, value.Armour)
}

func applyLocalPlayerLifeState(i *instance, value samp.PlayerLifeStateEvent) {
	i.snap.LocalPlayer.LifeState = string(value.State)
	switch value.State {
	case samp.PlayerLifeStateClassSelection:
		i.snap.Spawned = false
		i.snap.SpawnReady = false
	case samp.PlayerLifeStateSpawnReady, samp.PlayerLifeStateSpawnRequestPending:
		i.snap.Spawned = false
		i.snap.SpawnReady = true
	case samp.PlayerLifeStateDead:
		i.snap.Spawned = false
		i.snap.SpawnReady = true
		i.snap.KeyMask = 0
		i.snap.VehicleState = domain.VehicleState{VehicleID: domain.InvalidVehicleID}
	case samp.PlayerLifeStateSpawned:
		i.snap.Spawned = true
		i.snap.SpawnReady = false
	case domain.LifeStateDisconnected:
		i.snap.Spawned = false
		i.snap.SpawnReady = false
	}
}

func applyLocalPlayerDeath(i *instance) {
	i.snap.Spawned = false
	i.snap.SpawnReady = true
	i.snap.KeyMask = 0
	i.snap.LocalPlayer.Health = 0
	i.snap.LocalPlayer.LifeState = domain.LifeStateDead
	local := findPlayer(i.snap.Players, i.playerID)
	local.ID, local.Health = i.playerID, 0
	i.snap.Players = upsertPlayer(i.snap.Players, local)
	i.snap.Players = sortPlayers(i.snap.Players, i.playerID)
	updatePlayerHealth(i.snap.NearbyPlayers, i.playerID, 0, i.snap.LocalPlayer.Armour)
	i.snap.VehicleState = domain.VehicleState{VehicleID: domain.InvalidVehicleID}
}
func updatePlayerHealth(players []domain.Player, id int, health, armour float32) {
	for index := range players {
		if players[index].ID == id {
			players[index].Health = health
			players[index].Armour = armour
			return
		}
	}
}
func applyVehicleState(i *instance, value samp.VehicleStateEvent) {
	vehicleID := domain.InvalidVehicleID
	health, healthKnown := float32(0), false
	if value.InVehicle {
		vehicleID = int(value.VehicleID)
		health, healthKnown = value.Health, value.HasHealth
		if !healthKnown {
			if vehicle, ok := findVehicleByID(i.snap.Vehicles, vehicleID); ok {
				health, healthKnown = vehicle.Health, true
			}
		}
	}
	i.snap.VehicleState = domain.VehicleState{
		InVehicle: value.InVehicle, Passenger: value.Passenger, VehicleID: vehicleID,
		Health: health, HealthKnown: healthKnown,
	}
}
func resetConnectionState(i *instance) {
	clear(i.snap.Players)
	i.snap.Players = i.snap.Players[:0]
	clear(i.snap.NearbyPlayers)
	i.snap.NearbyPlayers = i.snap.NearbyPlayers[:0]
	clear(i.snap.Vehicles)
	i.snap.Vehicles = i.snap.Vehicles[:0]
	clear(i.snap.Objects)
	i.snap.Objects = i.snap.Objects[:0]
	clear(i.snap.TextDraws)
	i.snap.TextDraws = i.snap.TextDraws[:0]
	i.snap.ActiveDialog = nil
	clear(i.snap.Dialogs)
	i.snap.Dialogs = i.snap.Dialogs[:0]
	i.snap.VehicleState = domain.VehicleState{VehicleID: domain.InvalidVehicleID}
	i.snap.LocalPlayer = domain.LocalPlayer{ID: domain.InvalidPlayerID, LifeState: domain.LifeStateDisconnected}
	i.snap.KeyMask = 0
	i.snap.AFK = false
	i.snap.Spawned = false
	i.snap.SpawnReady = false
	i.position = [3]float32{}
	i.playerID = domain.InvalidPlayerID
}
func sortPlayers(players []domain.Player, localPlayerID int) []domain.Player {
	sort.SliceStable(players, func(left, right int) bool {
		leftIsLocal := players[left].ID == localPlayerID
		rightIsLocal := players[right].ID == localPlayerID
		if leftIsLocal != rightIsLocal {
			return leftIsLocal
		}
		return players[left].ID < players[right].ID
	})
	return players
}
func findPlayer(v []domain.Player, id int) domain.Player {
	for _, p := range v {
		if p.ID == id {
			return p
		}
	}
	return domain.Player{ID: id}
}
func playerName(snapshot domain.Snapshot, id int) string {
	for _, player := range snapshot.Players {
		if player.ID == id && player.Name != "" {
			return player.Name
		}
	}
	return fmt.Sprintf("Player %d", id)
}
func colorHex(v uint32) string { return fmt.Sprintf("#%08x", v) }
func upsertTextDraw(v []domain.TextDraw, item domain.TextDraw) []domain.TextDraw {
	for x := range v {
		if v[x].ID == item.ID {
			v[x] = item
			return v
		}
	}
	if len(v) >= domain.MaxTextDraws {
		return v
	}
	return append(v, item)
}
func removeTextDraw(v []domain.TextDraw, id int) []domain.TextDraw {
	out := v[:0]
	for _, item := range v {
		if item.ID != id {
			out = append(out, item)
		}
	}
	clear(v[len(out):])
	return out
}
func upsertObject(v []domain.Object, item domain.Object) []domain.Object {
	for x := range v {
		if v[x].ID == item.ID {
			v[x] = item
			return v
		}
	}
	if len(v) >= domain.MaxObjects {
		return v
	}
	return append(v, item)
}
func removeObject(v []domain.Object, id int) []domain.Object {
	out := v[:0]
	for _, item := range v {
		if item.ID != id {
			out = append(out, item)
		}
	}
	clear(v[len(out):])
	return out
}
func upsertVehicle(v []domain.Vehicle, item domain.Vehicle) []domain.Vehicle {
	for x := range v {
		if v[x].ID == item.ID {
			v[x] = item
			return v
		}
	}
	if len(v) >= domain.MaxVehicles {
		return v
	}
	return append(v, item)
}
func removeVehicle(v []domain.Vehicle, id int) []domain.Vehicle {
	out := v[:0]
	for _, item := range v {
		if item.ID != id {
			out = append(out, item)
		}
	}
	clear(v[len(out):])
	return out
}
func findVehicle(v []domain.Vehicle, id int) domain.Vehicle {
	item, _ := findVehicleByID(v, id)
	return item
}
func findVehicleByID(v []domain.Vehicle, id int) (domain.Vehicle, bool) {
	for _, item := range v {
		if item.ID == id {
			return item, true
		}
	}
	return domain.Vehicle{ID: id}, false
}
func distance(a, b [3]float32) float32 {
	dx, dy, dz := float64(a[0]-b[0]), float64(a[1]-b[1]), float64(a[2]-b[2])
	return float32(math.Sqrt(dx*dx + dy*dy + dz*dz))
}

func recalculateNearby(i *instance) {
	for index := range i.snap.NearbyPlayers {
		player := &i.snap.NearbyPlayers[index]
		player.Distance = distance(i.position, [3]float32{player.X, player.Y, player.Z})
	}
	for index := range i.snap.Vehicles {
		vehicle := &i.snap.Vehicles[index]
		vehicle.Distance = distance(i.position, [3]float32{vehicle.X, vehicle.Y, vehicle.Z})
	}
	for index := range i.snap.Objects {
		object := &i.snap.Objects[index]
		object.Distance = distance(i.position, [3]float32{object.X, object.Y, object.Z})
	}
	sortNearby(i)
}

func sortNearby(i *instance) {
	sort.SliceStable(i.snap.NearbyPlayers, func(a, b int) bool {
		return i.snap.NearbyPlayers[a].Distance < i.snap.NearbyPlayers[b].Distance
	})
	sort.SliceStable(i.snap.Vehicles, func(a, b int) bool {
		return i.snap.Vehicles[a].Distance < i.snap.Vehicles[b].Distance
	})
	sort.SliceStable(i.snap.Objects, func(a, b int) bool {
		return i.snap.Objects[a].Distance < i.snap.Objects[b].Distance
	})
}
func (m *Manager) Disconnect(id string) error {
	i, ok := m.find(id)
	if !ok {
		return errors.New("instance not found")
	}
	i.mu.Lock()
	cancel := i.cancel
	client := i.client
	i.cancel = nil
	i.client = nil
	i.snap.Connection = domain.Connection{Status: domain.StatusDisconnected}
	resetConnectionState(i)
	i.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		_ = client.Close()
	}
	m.publish(id, i)
	return nil
}
func actionContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, actionTimeout)
}

func (m *Manager) Action(id, action string, p map[string]any) error {
	return m.action(context.Background(), id, action, p)
}

func (m *Manager) action(ctx context.Context, id, action string, p map[string]any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	i, ok := m.find(id)
	if !ok {
		return errors.New("instance not found")
	}
	i.mu.Lock()
	client := i.client
	if client == nil || i.snap.Connection.Status != domain.StatusConnected {
		i.mu.Unlock()
		return errors.New("instance is not connected")
	}
	switch action {
	case domain.ActionChat:
		t, err := requiredString(p, "text")
		if err != nil {
			i.mu.Unlock()
			return err
		}
		i.mu.Unlock()
		var sendErr error
		requestCtx, cancel := actionContext(ctx)
		defer cancel()
		if strings.HasPrefix(t, "/") {
			sendErr = client.SendCommand(requestCtx, t)
		} else {
			sendErr = client.SendChat(requestCtx, t)
		}
		if sendErr != nil {
			return sendErr
		}
		return nil
	case domain.ActionSpawn:
		i.mu.Unlock()
		requestCtx, cancel := actionContext(ctx)
		err := client.RequestSpawn(requestCtx)
		cancel()
		return err
	case domain.ActionKeys:
		value, err := requiredInteger(p, "mask", 0, int64(^uint32(0)))
		if err != nil {
			i.mu.Unlock()
			return err
		}
		mask := uint32(value)
		i.mu.Unlock()
		requestCtx, cancel := actionContext(ctx)
		actionErr := client.SetKeys(requestCtx, mask)
		cancel()
		if actionErr != nil {
			return actionErr
		}
		i.mu.Lock()
		i.snap.KeyMask = 0
	case domain.ActionAFK:
		enabled, ok := p["enabled"].(bool)
		if !ok {
			i.mu.Unlock()
			return errors.New("enabled must be a boolean")
		}
		client.SetAFK(enabled)
		i.snap.AFK = enabled
		if i.snap.AFK {
			i.snap.KeyMask = 0
		}
	case domain.ActionTeleport:
		xValue, err := requiredFloat(p, "x")
		yValue, errY := requiredFloat(p, "y")
		zValue, errZ := requiredFloat(p, "z")
		if err != nil || errY != nil || errZ != nil {
			i.mu.Unlock()
			if err != nil {
				return err
			}
			if errY != nil {
				return errY
			}
			return errZ
		}
		x, y, z := float32(xValue), float32(yValue), float32(zValue)
		i.mu.Unlock()
		requestCtx, cancel := actionContext(ctx)
		actionErr := client.Teleport(requestCtx, x, y, z)
		cancel()
		if actionErr != nil {
			return actionErr
		}
		i.mu.Lock()
		i.position = [3]float32{x, y, z}
		recalculateNearby(i)
	case domain.ActionEnterVehicle:
		value, err := requiredInteger(p, "vehicleId", 0, int64(^uint16(0)))
		if err != nil {
			i.mu.Unlock()
			return err
		}
		vehicleID := uint16(value)
		passenger, _ := p["passenger"].(bool)
		if rawPassenger, exists := p["passenger"]; exists {
			var ok bool
			passenger, ok = rawPassenger.(bool)
			if !ok {
				i.mu.Unlock()
				return errors.New("passenger must be a boolean")
			}
		}
		entryMode := samp.VehicleEntryDirect
		if rawMode, exists := p["mode"]; exists {
			mode, ok := rawMode.(string)
			if !ok {
				i.mu.Unlock()
				return errors.New("mode must be a string")
			}
			entryMode = samp.VehicleEntryMode(mode)
		}
		entryMode, err = samp.NormalizeVehicleEntryMode(entryMode)
		if err != nil {
			i.mu.Unlock()
			return err
		}
		i.mu.Unlock()
		// A vehicle transition must cancel a previous walk/drive task;
		// otherwise its next tick can overwrite the vehicle position and make
		// the server remove the player immediately after entry.
		client.StopMovement()
		requestCtx, cancel := actionContext(ctx)
		actionErr := client.EnterVehicle(requestCtx, vehicleID, passenger, entryMode)
		cancel()
		if actionErr != nil {
			return actionErr
		}
		i.mu.Lock()
	case domain.ActionExitVehicle:
		i.mu.Unlock()
		client.StopMovement()
		requestCtx, cancel := actionContext(ctx)
		err := client.ExitVehicle(requestCtx)
		cancel()
		if err != nil {
			return err
		}
		i.mu.Lock()
	case domain.ActionStopMovement:
		i.mu.Unlock()
		client.StopMovement()
		return nil
	case domain.ActionDialog:
		dialogValue, err := requiredInteger(p, "dialogId", -1<<15, 1<<15-1)
		buttonValue, errButton := requiredInteger(p, "buttonId", 0, int64(^uint8(0)))
		itemValue, errItem := optionalInteger(p, "listItem", 0, -1<<15, 1<<15-1)
		if err != nil || errButton != nil || errItem != nil {
			i.mu.Unlock()
			if err != nil {
				return err
			}
			if errButton != nil {
				return errButton
			}
			return errItem
		}
		dialogID, button, item := int16(dialogValue), uint8(buttonValue), int16(itemValue)
		input, ok := p["inputText"].(string)
		if _, exists := p["inputText"]; exists && !ok {
			i.mu.Unlock()
			return errors.New("inputText must be a string")
		}
		activeDialog := i.snap.ActiveDialog
		i.mu.Unlock()
		var dialogErr error
		requestCtx, cancel := actionContext(ctx)
		defer cancel()
		if rawInput, ok := rawDialogListInput(activeDialog, item); ok {
			dialogErr = client.RespondDialogBytes(requestCtx, dialogID, button, item, rawInput)
		} else {
			dialogErr = client.RespondDialog(requestCtx, dialogID, button, item, input)
		}
		if dialogErr != nil {
			return dialogErr
		}
		i.mu.Lock()
		i.snap.ActiveDialog = nil
	case domain.ActionClickPlayer:
		value, err := requiredInteger(p, "playerId", 0, int64(^uint16(0)))
		if err != nil {
			i.mu.Unlock()
			return err
		}
		playerID := uint16(value)
		i.mu.Unlock()
		requestCtx, cancel := actionContext(ctx)
		actionErr := client.ClickPlayer(requestCtx, playerID)
		cancel()
		if actionErr != nil {
			return actionErr
		}
		i.mu.Lock()
	case domain.ActionTextDraw:
		value, err := requiredInteger(p, "textDrawId", 0, int64(^uint16(0)))
		if err != nil {
			i.mu.Unlock()
			return err
		}
		textDrawID := uint16(value)
		i.mu.Unlock()
		requestCtx, cancel := actionContext(ctx)
		actionErr := client.ClickTextDraw(requestCtx, textDrawID)
		cancel()
		if actionErr != nil {
			return actionErr
		}
		i.mu.Lock()
	case domain.ActionDeferDialog:
		if i.snap.ActiveDialog != nil {
			i.snap.Dialogs = append(i.snap.Dialogs, *i.snap.ActiveDialog)
			if len(i.snap.Dialogs) > domain.MaxDeferredDialogs {
				dropped := len(i.snap.Dialogs) - domain.MaxDeferredDialogs
				copy(i.snap.Dialogs, i.snap.Dialogs[dropped:])
				clear(i.snap.Dialogs[domain.MaxDeferredDialogs:])
				i.snap.Dialogs = i.snap.Dialogs[:domain.MaxDeferredDialogs]
			}
			i.snap.ActiveDialog = nil
		}
	case domain.ActionShowDialog:
		value, err := requiredInteger(p, "dialogId", -1<<31, 1<<31-1)
		if err != nil {
			i.mu.Unlock()
			return err
		}
		dialogID := int(value)
		for index, dialog := range i.snap.Dialogs {
			if dialog.ID == dialogID {
				i.snap.ActiveDialog = &dialog
				copy(i.snap.Dialogs[index:], i.snap.Dialogs[index+1:])
				clear(i.snap.Dialogs[len(i.snap.Dialogs)-1:])
				i.snap.Dialogs = i.snap.Dialogs[:len(i.snap.Dialogs)-1]
				break
			}
		}
	case domain.ActionDismissDialog:
		value, err := requiredInteger(p, "dialogId", -1<<31, 1<<31-1)
		if err != nil {
			i.mu.Unlock()
			return err
		}
		dialogID := int(value)
		dialogs := i.snap.Dialogs[:0]
		for _, dialog := range i.snap.Dialogs {
			if dialog.ID != dialogID {
				dialogs = append(dialogs, dialog)
			}
		}
		clear(i.snap.Dialogs[len(dialogs):])
		i.snap.Dialogs = dialogs
	default:
		i.mu.Unlock()
		return errors.New("unknown action")
	}
	i.mu.Unlock()
	m.publish(id, i)
	return nil
}

func (m *Manager) RefreshScores(ctx context.Context, id string) error {
	i, ok := m.find(id)
	if !ok {
		return errors.New("instance not found")
	}
	i.mu.RLock()
	client := i.client
	connected := i.snap.Connection.Status == domain.StatusConnected
	i.mu.RUnlock()
	if client == nil || !connected {
		return errors.New("instance is not connected")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	return client.RefreshScores(requestCtx)
}

// InvokePluginAPI is the intentionally broad API boundary exposed to
// out-of-process plugins. Keep the method names stable and make the generic
// action endpoint available as an escape hatch for new client capabilities.
func (m *Manager) InvokePluginAPI(ctx context.Context, instanceID, method string, raw json.RawMessage) (any, error) {
	params, err := pluginParams(raw)
	if err != nil {
		return nil, err
	}
	switch method {
	case plugin.MethodListInstances:
		return m.List(), nil
	case plugin.MethodCreateInstance:
		var server domain.Server
		if err := decodePluginObject(params, &server); err != nil {
			return nil, err
		}
		return m.Create(server)
	case plugin.MethodUpdateInstance:
		if instanceID == "" {
			return nil, errors.New("instanceId is required")
		}
		var server domain.Server
		if err := decodePluginObject(params, &server); err != nil {
			return nil, err
		}
		return m.Update(instanceID, server)
	case plugin.MethodDeleteInstance:
		if instanceID == "" {
			return nil, errors.New("instanceId is required")
		}
		return nil, m.Delete(instanceID)
	case plugin.MethodGetInstance:
		if instanceID == "" {
			instanceID, _ = params["instanceId"].(string)
		}
		if instanceID == "" {
			return nil, errors.New("instanceId is required")
		}
		snapshot, ok := m.Get(instanceID)
		if !ok {
			return nil, errors.New("instance not found")
		}
		return snapshot, nil
	case plugin.MethodGetChat:
		before, err := optionalInteger(params, "before", 0, 0, maxPluginSafeInteger)
		if err != nil {
			return nil, err
		}
		limit, err := optionalInteger(params, "limit", 0, 0, 100)
		if err != nil {
			return nil, err
		}
		return m.Chat(instanceID, before, int(limit))
	case plugin.MethodConnect:
		if instanceID == "" {
			return nil, errors.New("instanceId is required")
		}
		return nil, m.Connect(instanceID)
	case plugin.MethodDisconnect:
		if instanceID == "" {
			return nil, errors.New("instanceId is required")
		}
		return nil, m.Disconnect(instanceID)
	case plugin.MethodAddCommand:
		if instanceID == "" {
			return nil, errors.New("instanceId is required")
		}
		var command domain.QuickCommand
		if err := decodePluginObject(params, &command); err != nil {
			return nil, err
		}
		return m.AddCommand(instanceID, command)
	case plugin.MethodDeleteCommand:
		if instanceID == "" {
			return nil, errors.New("instanceId is required")
		}
		commandID, err := requiredString(params, "commandId")
		if err != nil {
			return nil, err
		}
		return nil, m.DeleteCommand(instanceID, commandID)
	case plugin.MethodAction:
		action, _ := params["action"].(string)
		if action == "" {
			return nil, errors.New("action is required")
		}
		actionParams := map[string]any{}
		if rawActionParams, exists := params["params"]; exists {
			var ok bool
			actionParams, ok = rawActionParams.(map[string]any)
			if !ok {
				return nil, errors.New("params must be an object")
			}
		}
		if action == domain.ActionWalkTo || action == domain.ActionDriveTo {
			return m.startMotion(ctx, instanceID, action, actionParams)
		}
		return nil, m.action(ctx, instanceID, action, actionParams)
	case plugin.MethodSendChat:
		text, err := requiredString(params, "text")
		if err != nil {
			return nil, err
		}
		return nil, m.action(ctx, instanceID, domain.ActionChat, map[string]any{"text": text})
	case plugin.MethodSendCommand:
		command, err := requiredString(params, "command")
		if err != nil {
			return nil, err
		}
		if command != "" && !strings.HasPrefix(command, "/") {
			command = "/" + command
		}
		return nil, m.action(ctx, instanceID, domain.ActionChat, map[string]any{"text": command})
	case plugin.MethodRequestSpawn:
		return nil, m.action(ctx, instanceID, domain.ActionSpawn, nil)
	case plugin.MethodRefreshScores:
		return nil, m.RefreshScores(ctx, instanceID)
	case plugin.MethodWalkTo:
		return m.startMotion(ctx, instanceID, domain.ActionWalkTo, params)
	case plugin.MethodDriveTo:
		return m.startMotion(ctx, instanceID, domain.ActionDriveTo, params)
	case plugin.MethodStopMovement:
		return nil, m.action(ctx, instanceID, domain.ActionStopMovement, params)
	case plugin.MethodSetKeys, plugin.MethodSetAFK, plugin.MethodTeleport, plugin.MethodEnterVehicle, plugin.MethodExitVehicle, plugin.MethodRespondDialog, plugin.MethodClickPlayer, plugin.MethodClickTextDraw:
		return nil, m.action(ctx, instanceID, pluginAction(method), params)
	default:
		return nil, fmt.Errorf("unknown plugin API method %q", method)
	}
}

func (m *Manager) startMotion(ctx context.Context, id, action string, p map[string]any) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	i, ok := m.find(id)
	if !ok {
		return nil, errors.New("instance not found")
	}
	xValue, err := requiredFloat(p, "x")
	yValue, errY := requiredFloat(p, "y")
	zValue, errZ := requiredFloat(p, "z")
	if err != nil || errY != nil || errZ != nil {
		if err != nil {
			return nil, err
		}
		if errY != nil {
			return nil, errY
		}
		return nil, errZ
	}
	speed, err := optionalFloat(p, "speed", 0)
	if err != nil {
		return nil, err
	}
	tolerance, err := optionalFloat(p, "tolerance", 0)
	if err != nil {
		return nil, err
	}
	target := [3]float32{float32(xValue), float32(yValue), float32(zValue)}
	i.mu.Lock()
	client := i.client
	connected := i.snap.Connection.Status == domain.StatusConnected
	vehicleID := uint16(0)
	if action == domain.ActionDriveTo {
		if i.snap.VehicleState.InVehicle && i.snap.VehicleState.Passenger {
			i.mu.Unlock()
			return nil, samp.ErrMotionNotDriver
		}
		if _, exists := p["vehicleId"]; exists {
			vehicleValue, vehicleErr := requiredInteger(p, "vehicleId", 0, int64(^uint16(0)))
			if vehicleErr != nil {
				i.mu.Unlock()
				return nil, vehicleErr
			}
			vehicleID = uint16(vehicleValue)
		} else if i.snap.VehicleState.InVehicle && !i.snap.VehicleState.Passenger && i.snap.VehicleState.VehicleID >= 0 {
			vehicleID = uint16(i.snap.VehicleState.VehicleID)
		} else {
			i.mu.Unlock()
			return nil, errors.New("vehicleId is required when the client is not driving")
		}
	}
	if client == nil || !connected {
		i.mu.Unlock()
		return nil, errors.New("instance is not connected")
	}
	i.mu.Unlock()

	var taskID uint64
	if action == domain.ActionWalkTo {
		taskID, err = client.WalkTo(target, speed, tolerance)
	} else {
		taskID, err = client.DriveTo(vehicleID, target, speed, tolerance)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"taskId": taskID}, nil
}

func pluginParams(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("invalid plugin parameters: %w", err)
	}
	if params == nil {
		params = map[string]any{}
	}
	return params, nil
}

func decodePluginObject(params map[string]any, target any) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode plugin parameters: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid plugin parameters: %w", err)
	}
	return nil
}

func requiredString(params map[string]any, key string) (string, error) {
	value, ok := params[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func requiredFloat(params map[string]any, key string) (float64, error) {
	value, ok := params[key].(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxFloat32 {
		return 0, fmt.Errorf("%s must be a finite number", key)
	}
	return value, nil
}

func optionalFloat(params map[string]any, key string, fallback float32) (float32, error) {
	if _, exists := params[key]; !exists {
		return fallback, nil
	}
	value, err := requiredFloat(params, key)
	if err != nil {
		return 0, err
	}
	return float32(value), nil
}

func requiredInteger(params map[string]any, key string, minimum, maximum int64) (int64, error) {
	value, ok := params[key].(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || math.Abs(value) > float64(maxPluginSafeInteger) || value < float64(minimum) || value > float64(maximum) {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, minimum, maximum)
	}
	return int64(value), nil
}

func optionalInteger(params map[string]any, key string, fallback, minimum, maximum int64) (int64, error) {
	if _, exists := params[key]; !exists {
		return fallback, nil
	}
	return requiredInteger(params, key, minimum, maximum)
}

func pluginAction(method string) string {
	switch method {
	case plugin.MethodSetKeys:
		return domain.ActionKeys
	case plugin.MethodSetAFK:
		return domain.ActionAFK
	case plugin.MethodTeleport:
		return domain.ActionTeleport
	case plugin.MethodEnterVehicle:
		return domain.ActionEnterVehicle
	case plugin.MethodExitVehicle:
		return domain.ActionExitVehicle
	case plugin.MethodRespondDialog:
		return domain.ActionDialog
	case plugin.MethodClickPlayer:
		return domain.ActionClickPlayer
	case plugin.MethodClickTextDraw:
		return domain.ActionTextDraw
	default:
		return ""
	}
}

func (m *Manager) find(id string) (*instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	i, ok := m.instances[id]
	return i, ok
}
func (m *Manager) publish(id string, i *instance) {
	i.mu.Lock()
	previous := i.published
	current := cloneSnapshot(i.snap)
	operations := snapshotPatch(previous, current)
	if len(operations) == 0 {
		i.mu.Unlock()
		return
	}
	i.snap.Revision++
	current.Revision = i.snap.Revision
	i.published = cloneSnapshot(current)
	i.mu.Unlock()
	m.emit(domain.Event{Type: domain.EventInstanceUpdated, InstanceID: id, Data: domain.InstancePatch{Revision: current.Revision, SyncEpoch: current.SyncEpoch, Operations: operations}})
}

// markDirty notifies the instance's publishWorker that movement-heavy state
// changed. It never blocks; the channel only needs to remember that a flush
// is pending. A nil channel (test-constructed instances) is simply ignored.
func markDirty(i *instance) {
	select {
	case i.dirty <- struct{}{}:
	default:
	}
}

// publishWorker coalesces movement sync updates into at most one state patch
// per statePublishInterval. Chat and low-frequency state keep publishing
// immediately through publish; only the hot paths (player/vehicle sync, local
// position, appearance) are routed here to avoid flooding the websocket.
func (m *Manager) publishWorker(ctx context.Context, id string, i *instance) {
	ticker := time.NewTicker(statePublishInterval)
	defer ticker.Stop()
	dirty := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-i.dirty:
			dirty = true
		case <-ticker.C:
			if dirty {
				dirty = false
				m.publish(id, i)
			}
		}
	}
}

// snapshotPatch compares explicit public fields. This makes the wire contract
// auditable and prevents future internal Snapshot fields leaking by accident.
func snapshotPatch(previous, current domain.Snapshot) []domain.PatchOperation {
	operations := make([]domain.PatchOperation, 0, 15)
	add := func(path string, before, after any) {
		if !reflect.DeepEqual(before, after) {
			operations = append(operations, domain.PatchOperation{Op: "replace", Path: path, Value: after})
		}
	}
	add("/server", previous.Server, current.Server)
	add("/connection", previous.Connection, current.Connection)
	add("/players", previous.Players, current.Players)
	add("/nearbyPlayers", previous.NearbyPlayers, current.NearbyPlayers)
	add("/vehicles", previous.Vehicles, current.Vehicles)
	add("/objects", previous.Objects, current.Objects)
	add("/textDraws", previous.TextDraws, current.TextDraws)
	add("/dialogs", previous.Dialogs, current.Dialogs)
	add("/commands", previous.Commands, current.Commands)
	add("/activeDialog", previous.ActiveDialog, current.ActiveDialog)
	add("/localPlayer", previous.LocalPlayer, current.LocalPlayer)
	add("/vehicleState", previous.VehicleState, current.VehicleState)
	add("/keyMask", previous.KeyMask, current.KeyMask)
	add("/afk", previous.AFK, current.AFK)
	add("/spawned", previous.Spawned, current.Spawned)
	add("/spawnReady", previous.SpawnReady, current.SpawnReady)
	return operations
}
func (m *Manager) emit(e domain.Event) {
	m.pluginMu.RLock()
	sink := m.pluginSink
	m.pluginMu.RUnlock()
	if sink != nil {
		sink.Emit(plugin.Event{Name: e.Type, InstanceID: e.InstanceID, Time: time.Now(), Data: e.Data})
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for subscription := range m.subscribers {
		ch := subscription.state
		if e.Type == domain.EventChatMessage || e.Type == domain.EventChatReset {
			ch = subscription.chat
		}
		select {
		case ch <- e:
		default:
		}
	}
}

func (m *Manager) emitClientPluginEvent(instanceID string, event samp.Event) {
	m.pluginMu.RLock()
	sink := m.pluginSink
	m.pluginMu.RUnlock()
	if sink == nil {
		return
	}
	now := time.Now()
	sink.Emit(plugin.Event{Name: clientPluginEventName(event), InstanceID: instanceID, Time: now, Data: pluginEventData(event)})
	if event.Type == samp.EventSpawned {
		if value, ok := event.Data.(samp.SpawnedEvent); ok {
			sink.Emit(plugin.Event{
				Name: plugin.EventClientPlayerHealth, InstanceID: instanceID, Time: now,
				Data: plugin.PlayerHealthEventData{Health: value.Health, Armour: value.Armour},
			})
		}
	}
}

func clientPluginEventName(event samp.Event) string {
	if event.Type != samp.EventMovement {
		return plugin.EventClientPrefix + string(event.Type)
	}
	value, ok := event.Data.(samp.MotionEvent)
	if !ok {
		return plugin.EventClientMovement
	}
	switch value.State {
	case samp.MotionStarted:
		return plugin.EventClientMovementStart
	case samp.MotionProgress:
		return plugin.EventClientMovementProgress
	case samp.MotionCompleted:
		return plugin.EventClientMovementComplete
	case samp.MotionStopped:
		return plugin.EventClientMovementStopped
	case samp.MotionFailed:
		return plugin.EventClientMovementFailed
	default:
		return plugin.EventClientMovement
	}
}

// pluginEventData is the compatibility boundary between the internal SA-MP
// decoder and the public plugin protocol. Keep this mapping explicit: raw Go
// structs intentionally do not cross the boundary because their exported
// field names and implementation-only flags are not a stable JSON contract.
func pluginEventData(event samp.Event) any {
	switch event.Type {
	case samp.EventJoined:
		value := event.Data.(samp.PlayerEvent)
		return plugin.JoinedEventData{PlayerID: int(value.ID)}
	case samp.EventChat:
		value := event.Data.(samp.ChatEvent)
		data := plugin.ChatEventData{Text: value.Text}
		if value.PlayerID != nil {
			playerID := int(*value.PlayerID)
			data.PlayerID = &playerID
		}
		if value.Color != 0 {
			data.Color = colorHex(value.Color)
		}
		return data
	case samp.EventPlayerJoin:
		value := event.Data.(samp.PlayerEvent)
		data := plugin.PlayerJoinEventData{ID: int(value.ID), Name: value.Name}
		if value.HasColor {
			data.Color = colorHex(value.Color)
		}
		return data
	case samp.EventPlayerQuit:
		value := event.Data.(samp.PlayerEvent)
		return plugin.PlayerQuitEventData{ID: int(value.ID)}
	case samp.EventScores:
		values := event.Data.([]samp.PlayerEvent)
		data := make([]plugin.ScoreEventData, 0, len(values))
		for _, value := range values {
			data = append(data, plugin.ScoreEventData{ID: int(value.ID), Score: int(value.Score), Ping: int(value.Ping)})
		}
		return data
	case samp.EventDialog:
		value := event.Data.(samp.DialogEvent)
		return plugin.DialogEventData{ID: int(value.ID), Style: int(value.Style), Title: value.Title, Message: value.Message, Button1: value.Button1, Button2: value.Button2}
	case samp.EventDisconnected:
		return plugin.ReasonEventData{Reason: eventErrorText(event.Data)}
	case samp.EventProtocolError:
		return plugin.ErrorEventData{Message: eventErrorText(event.Data)}
	case samp.EventTextDrawShow:
		value := event.Data.(samp.TextDrawEvent)
		return plugin.TextDrawEventData{
			ID: int(value.ID), Text: value.Text, Style: int(value.Style), Flags: int(value.Flags), Shadow: int(value.Shadow), Outline: int(value.Outline),
			Selectable: value.Selectable != 0, LetterColor: colorHex(value.LetterColor), BoxColor: colorHex(value.BoxColor), BackgroundColor: colorHex(value.BackgroundColor),
			X: value.X, Y: value.Y, LetterWidth: value.LetterWidth, LetterHeight: value.LetterHeight, LineWidth: value.LineWidth, LineHeight: value.LineHeight, ModelID: int(value.ModelID),
		}
	case samp.EventTextDrawHide:
		value := event.Data.(samp.TextDrawEvent)
		return plugin.IDEventData{ID: int(value.ID)}
	case samp.EventTextDrawText:
		value := event.Data.(samp.TextDrawEvent)
		return plugin.TextDrawTextEventData{ID: int(value.ID), Text: value.Text}
	case samp.EventObjectAdd:
		value := event.Data.(samp.ObjectEvent)
		return plugin.ObjectEventData{ID: int(value.ID), ModelID: int(value.ModelID), X: value.X, Y: value.Y, Z: value.Z}
	case samp.EventObjectRemove:
		value := event.Data.(samp.ObjectEvent)
		return plugin.IDEventData{ID: int(value.ID)}
	case samp.EventVehicleAdd:
		value := event.Data.(samp.VehicleEvent)
		return plugin.VehicleEventData{ID: int(value.ID), ModelID: int(value.ModelID), X: value.X, Y: value.Y, Z: value.Z, Health: value.Health}
	case samp.EventVehicleRemove:
		value := event.Data.(samp.VehicleEvent)
		return plugin.IDEventData{ID: int(value.ID)}
	case samp.EventPlayerSync:
		value := event.Data.(samp.PlayerEvent)
		data := plugin.PlayerSyncEventData{ID: int(value.ID), X: value.X, Y: value.Y, Z: value.Z, Health: value.Health, Armour: value.Armour, Skin: int(value.Skin), Team: int(value.Team), Rotation: value.Rotation}
		if value.HasColor {
			data.Color = colorHex(value.Color)
		}
		return data
	case samp.EventPosition:
		value := event.Data.([3]float32)
		return plugin.PositionEventData{X: value[0], Y: value[1], Z: value[2]}
	case samp.EventAppearance:
		value := event.Data.(samp.PlayerEvent)
		data := plugin.AppearanceEventData{ID: int(value.ID)}
		if value.HasPosition {
			data.X, data.Y, data.Z = float32Ptr(value.X), float32Ptr(value.Y), float32Ptr(value.Z)
		}
		if value.HasSkin {
			skin := int(value.Skin)
			data.Skin = &skin
		}
		if value.HasTeam {
			team := int(value.Team)
			data.Team = &team
		}
		if value.HasRotation {
			data.Rotation = float32Ptr(value.Rotation)
		}
		if value.HasColor {
			color := colorHex(value.Color)
			data.Color = &color
		}
		return data
	case samp.EventPlayerHealth:
		value := event.Data.(samp.PlayerHealthEvent)
		return plugin.PlayerHealthEventData{Health: value.Health, Armour: value.Armour}
	case samp.EventPlayerLifeState:
		value := event.Data.(samp.PlayerLifeStateEvent)
		return plugin.PlayerLifeStateEventData{State: string(value.State)}
	case samp.EventPlayerDeath:
		value := event.Data.(samp.PlayerDeathEvent)
		killerID := -1
		if value.KillerID != samp.InvalidSAMPPlayerID {
			killerID = int(value.KillerID)
		}
		return plugin.PlayerDeathEventData{Reason: int(value.Reason), KillerID: killerID, ReasonKnown: value.ReasonKnown, Source: string(value.Source)}
	case samp.EventVehicleState:
		value := event.Data.(samp.VehicleStateEvent)
		vehicleID := -1
		if value.InVehicle {
			vehicleID = int(value.VehicleID)
		}
		return plugin.VehicleStateEventData{InVehicle: value.InVehicle, Passenger: value.Passenger, VehicleID: vehicleID, Health: value.Health, HealthKnown: value.HasHealth}
	case samp.EventVehicleHealth:
		value := event.Data.(samp.VehicleHealthEvent)
		return plugin.VehicleHealthEventData{ID: int(value.ID), Health: value.Health}
	case samp.EventMovement:
		value := event.Data.(samp.MotionEvent)
		return plugin.MovementEventData{
			TaskID: value.TaskID, Kind: string(value.Kind), State: string(value.State),
			X: value.Position[0], Y: value.Position[1], Z: value.Position[2],
			TargetX: value.Target[0], TargetY: value.Target[1], TargetZ: value.Target[2],
			Progress: value.Progress, Error: value.Error,
		}
	case samp.EventSpawned:
		return struct{}{}
	case samp.EventVehicleSync:
		value := event.Data.(samp.VehicleEvent)
		return plugin.VehicleEventData{ID: int(value.ID), ModelID: int(value.ModelID), X: value.X, Y: value.Y, Z: value.Z, Health: value.Health}
	default:
		// Unknown internal event types must not leak decoder structs into the
		// public contract. Add an explicit DTO mapping when a new event lands.
		return struct{}{}
	}
}

func eventErrorText(value any) string {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func float32Ptr(value float32) *float32 { return &value }

// PublishPluginEvent forwards plugin-host diagnostics and event envelopes to
// browser subscribers without feeding them back into the plugin host.
func (m *Manager) PublishPluginEvent(event plugin.Event) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for subscription := range m.pluginSubscribers {
		select {
		case subscription.events <- domain.Event{Type: event.Name, InstanceID: event.InstanceID, Data: event.Data}:
		default:
		}
	}
}

func (m *Manager) SubscribePluginEvents() (<-chan domain.Event, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pluginSubscribers) >= maxSubscribers {
		return nil, nil, errors.New("plugin subscriber limit reached")
	}
	subscription := &pluginSubscriber{events: make(chan domain.Event, chatEventQueueSize)}
	m.pluginSubscribers[subscription] = struct{}{}
	cleanup := func() {
		m.mu.Lock()
		delete(m.pluginSubscribers, subscription)
		close(subscription.events)
		m.mu.Unlock()
	}
	return subscription.events, cleanup, nil
}

func (m *Manager) Subscribe() (<-chan domain.Event, <-chan domain.Event, func(), error) {
	m.mu.Lock()
	if len(m.subscribers) >= maxSubscribers {
		m.mu.Unlock()
		return nil, nil, nil, errors.New("subscriber limit reached")
	}
	subscription := &subscriber{state: make(chan domain.Event, stateEventQueueSize), chat: make(chan domain.Event, chatEventQueueSize)}
	m.subscribers[subscription] = struct{}{}
	m.mu.Unlock()
	cleanup := func() {
		m.mu.Lock()
		delete(m.subscribers, subscription)
		close(subscription.state)
		close(subscription.chat)
		m.mu.Unlock()
	}
	return subscription.state, subscription.chat, cleanup, nil
}
