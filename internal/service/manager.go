package service

import (
	"context"
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
	"github.com/google/uuid"
)

// statePublishInterval is the minimum gap between state patches that carry
// movement-heavy data (player/vehicle sync, positions). Rapid sync events are
// coalesced by publishWorker into a single patch, cutting websocket traffic and
// slowing the nearby tab without delaying chat or dialogs.
var statePublishInterval = 500 * time.Millisecond

const (
	actionTimeout             = 5 * time.Second
	reconnectDelay            = 2 * time.Second
	defaultChatColor          = "#ffffffff"
	errorChatColor            = "#ff6b6bff"
	dialogStyleList           = 2
	dialogStyleTabList        = 4
	dialogStyleTabListHeaders = 5
	maxSubscribers            = 16
	stateEventQueueSize       = 2
	chatEventQueueSize        = 64
	maxHostBytes              = 253
	maxNicknameBytes          = 96
	maxPasswordBytes          = 128
	maxCommandLabelBytes      = 128
	maxCommandTextBytes       = 1024
)

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
type Manager struct {
	mu          sync.RWMutex
	instances   map[string]*instance
	store       *store.Store
	subscribers map[*subscriber]struct{}
	msgID       atomic.Int64
	logDir      string
	syncEpoch   string
}

type Option func(*Manager)

func WithLogDir(path string) Option {
	return func(manager *Manager) { manager.logDir = path }
}

func New(st *store.Store, options ...Option) *Manager {
	m := &Manager{instances: map[string]*instance{}, store: st, subscribers: map[*subscriber]struct{}{}, syncEpoch: uuid.NewString()}
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
	snapshot := domain.Snapshot{Revision: 1, SyncEpoch: syncEpoch, Server: s, Connection: domain.Connection{Status: domain.StatusDisconnected}, Chat: []domain.ChatMessage{}, Players: []domain.Player{}, NearbyPlayers: []domain.Player{}, Vehicles: []domain.Vehicle{}, Objects: []domain.Object{}, TextDraws: []domain.TextDraw{}, Dialogs: []domain.Dialog{}, Commands: []domain.QuickCommand{}, VehicleState: domain.VehicleState{VehicleID: domain.InvalidVehicleID}}
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
	if i.cancel != nil {
		i.cancel()
	}
	if i.client != nil {
		_ = i.client.Close()
	}
	i.mu.Unlock()
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
	if i.cancel != nil {
		i.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	i.cancel = cancel
	i.snap.Connection = domain.Connection{Status: domain.StatusConnecting}
	resetConnectionState(i)
	s := i.snap.Server
	m.appendChat(i, fmt.Sprintf("Connecting to %s:%d...", s.Host, s.Port), defaultChatColor)
	i.mu.Unlock()
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
		i.snap.Connection = domain.Connection{Status: domain.StatusConnecting}
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
	client, err := samp.DialClient(ctx, address, s.Nickname, s.Password, string(s.Encoding))
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
	info, _ := samp.Query(ctx, s.Host, s.Port)
	var disconnectErr error
	for event := range client.Events() {
		i.mu.Lock()
		publishSnapshot := true
		switch event.Type {
		case samp.EventJoined:
			localPlayer := event.Data.(samp.PlayerEvent)
			i.playerID = int(localPlayer.ID)
			i.snap.Players = upsertPlayer(i.snap.Players, domain.Player{ID: int(localPlayer.ID), Name: s.Nickname})
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
			sortNearby(i)
		case samp.EventVehicleRemove:
			v := event.Data.(samp.VehicleEvent)
			i.snap.Vehicles = removeVehicle(i.snap.Vehicles, int(v.ID))
		case samp.EventPlayerSync:
			v := event.Data.(samp.PlayerEvent)
			player := findPlayer(i.snap.Players, int(v.ID))
			player.ID, player.X, player.Y, player.Z = int(v.ID), v.X, v.Y, v.Z
			player.Health, player.Armour = v.Health, v.Armour
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
		case samp.EventVehicleState:
			v := event.Data.(samp.VehicleStateEvent)
			vehicleID := domain.InvalidVehicleID
			if v.InVehicle {
				vehicleID = int(v.VehicleID)
			}
			i.snap.VehicleState = domain.VehicleState{InVehicle: v.InVehicle, Passenger: v.Passenger, VehicleID: vehicleID}
		case samp.EventSpawned:
			i.snap.Spawned = true
		case samp.EventAppearance:
			v := event.Data.(samp.PlayerEvent)
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
			sortNearby(i)
			publishSnapshot = false
			markDirty(i)
		case samp.EventDisconnected:
			i.client = nil
			if ctx.Err() == nil {
				i.snap.Connection = domain.Connection{Status: domain.StatusDisconnected}
				resetConnectionState(i)
				disconnectErr, _ = event.Data.(error)
			}
		}
		i.mu.Unlock()
		if publishSnapshot {
			m.publish(id, i)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return disconnectErr
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
	i.snap.KeyMask = 0
	i.snap.AFK = false
	i.snap.Spawned = false
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
	for _, item := range v {
		if item.ID == id {
			return item
		}
	}
	return domain.Vehicle{ID: id}
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
	if i.cancel != nil {
		i.cancel()
		i.cancel = nil
	}
	if i.client != nil {
		_ = i.client.Close()
		i.client = nil
	}
	i.snap.Connection = domain.Connection{Status: domain.StatusDisconnected}
	resetConnectionState(i)
	i.mu.Unlock()
	m.publish(id, i)
	return nil
}
func (m *Manager) Action(id, action string, p map[string]any) error {
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
		t, _ := p["text"].(string)
		if t == "" {
			i.mu.Unlock()
			return errors.New("text is required")
		}
		i.mu.Unlock()
		var err error
		if strings.HasPrefix(t, "/") {
			err = client.SendCommand(context.Background(), t)
		} else {
			err = client.SendChat(context.Background(), t)
		}
		if err != nil {
			return err
		}
		return nil
	case domain.ActionKeys:
		mask := uint32(number(p["mask"]))
		i.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		err := client.SetKeys(ctx, mask)
		cancel()
		if err != nil {
			return err
		}
		i.mu.Lock()
		i.snap.KeyMask = 0
	case domain.ActionAFK:
		enabled, _ := p["enabled"].(bool)
		client.SetAFK(enabled)
		i.snap.AFK = enabled
		if i.snap.AFK {
			i.snap.KeyMask = 0
		}
	case domain.ActionTeleport:
		x, y, z := float32(number(p["x"])), float32(number(p["y"])), float32(number(p["z"]))
		i.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		err := client.Teleport(ctx, x, y, z)
		cancel()
		if err != nil {
			return err
		}
		i.mu.Lock()
		i.position = [3]float32{x, y, z}
		recalculateNearby(i)
	case domain.ActionEnterVehicle:
		vehicleID := uint16(number(p["vehicleId"]))
		passenger, _ := p["passenger"].(bool)
		i.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		err := client.EnterVehicle(ctx, vehicleID, passenger)
		cancel()
		if err != nil {
			return err
		}
		i.mu.Lock()
		i.snap.VehicleState = domain.VehicleState{InVehicle: true, VehicleID: int(vehicleID), Passenger: passenger}
	case domain.ActionExitVehicle:
		i.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		err := client.ExitVehicle(ctx)
		cancel()
		if err != nil {
			return err
		}
		i.mu.Lock()
		i.snap.VehicleState = domain.VehicleState{VehicleID: domain.InvalidVehicleID}
	case domain.ActionDialog:
		dialogID := int16(number(p["dialogId"]))
		button := uint8(number(p["buttonId"]))
		item := int16(number(p["listItem"]))
		input, _ := p["inputText"].(string)
		activeDialog := i.snap.ActiveDialog
		i.mu.Unlock()
		var err error
		if rawInput, ok := rawDialogListInput(activeDialog, item); ok {
			err = client.RespondDialogBytes(context.Background(), dialogID, button, item, rawInput)
		} else {
			err = client.RespondDialog(context.Background(), dialogID, button, item, input)
		}
		if err != nil {
			return err
		}
		i.mu.Lock()
		i.snap.ActiveDialog = nil
	case domain.ActionClickPlayer:
		playerID := uint16(number(p["playerId"]))
		i.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		err := client.ClickPlayer(ctx, playerID)
		cancel()
		if err != nil {
			return err
		}
		i.mu.Lock()
	case domain.ActionTextDraw:
		textDrawID := uint16(number(p["textDrawId"]))
		i.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		err := client.ClickTextDraw(ctx, textDrawID)
		cancel()
		if err != nil {
			return err
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
		dialogID := int(number(p["dialogId"]))
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
		dialogID := int(number(p["dialogId"]))
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
func number(v any) float64 { n, _ := v.(float64); return n }
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
	operations := make([]domain.PatchOperation, 0, 14)
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
	add("/vehicleState", previous.VehicleState, current.VehicleState)
	add("/keyMask", previous.KeyMask, current.KeyMask)
	add("/afk", previous.AFK, current.AFK)
	add("/spawned", previous.Spawned, current.Spawned)
	return operations
}
func (m *Manager) emit(e domain.Event) {
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
