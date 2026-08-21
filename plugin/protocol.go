// Package plugin contains the public wire contract used by SA-MP-Pilot
// plugins. Plugins are regular processes and may therefore be written in
// JavaScript, Python, Go, or any other language that can read and write JSON
// Lines on stdin/stdout.
package plugin

import (
	"context"
	"encoding/json"
	"time"
)

// ProtocolVersion 1 is the pre-release canonical plugin contract. It uses
// camelCase event payloads and explicit empty-subscription semantics.
const ProtocolVersion = 1

const MaxDebugCodeBytes = 256 << 10

const (
	MessageHello       = "hello"
	MessageReady       = "ready"
	MessageEvent       = "event"
	MessageCall        = "call"
	MessageResult      = "result"
	MessageSubscribe   = "subscribe"
	MessageLog         = "log"
	MessageDebug       = "debug"
	MessageDebugResult = "debug.result"
)

const (
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusStopped  = "stopped"
	StatusDisabled = "disabled"
	StatusError    = "error"
)

const (
	EventInstanceCreated     = "instance.created"
	EventInstanceUpdated     = "instance.updated"
	EventInstanceDeleted     = "instance.deleted"
	EventChatMessage         = "chat.message"
	EventChatReset           = "chat.reset"
	EventClientPrefix        = "client."
	EventClientJoined        = "client.joined"
	EventClientChat          = "client.chat"
	EventClientPlayerJoin    = "client.player.join"
	EventClientPlayerQuit    = "client.player.quit"
	EventClientScores        = "client.scores"
	EventClientDialog        = "client.dialog"
	EventClientDisconnected  = "client.disconnected"
	EventClientProtocolError = "client.protocol.error"
	EventClientTextDrawShow  = "client.textdraw.show"
	EventClientTextDrawHide  = "client.textdraw.hide"
	EventClientTextDrawText  = "client.textdraw.text"
	EventClientObjectAdd     = "client.object.add"
	EventClientObjectRemove  = "client.object.remove"
	EventClientVehicleAdd    = "client.vehicle.add"
	EventClientVehicleRemove = "client.vehicle.remove"
	EventClientPlayerSync    = "client.player.sync"
	EventClientPosition      = "client.position"
	EventClientAppearance    = "client.appearance"
	EventClientVehicleState  = "client.vehicle.state"
	EventClientSpawned       = "client.spawned"
	EventClientVehicleSync   = "client.vehicle.sync"
	EventPluginEvent         = "plugin.event"
	EventPluginLog           = "plugin.log"
	EventPluginStatus        = "plugin.status"
)

const (
	MethodListInstances  = "instances.list"
	MethodGetInstance    = "instance.getSnapshot"
	MethodGetChat        = "instance.getChat"
	MethodConnect        = "instance.connect"
	MethodDisconnect     = "instance.disconnect"
	MethodSendChat       = "instance.sendChat"
	MethodSendCommand    = "instance.sendCommand"
	MethodRequestSpawn   = "instance.requestSpawn"
	MethodRefreshScores  = "instance.refreshScores"
	MethodSetKeys        = "instance.setKeys"
	MethodSetAFK         = "instance.setAFK"
	MethodTeleport       = "instance.teleport"
	MethodEnterVehicle   = "instance.enterVehicle"
	MethodExitVehicle    = "instance.exitVehicle"
	MethodRespondDialog  = "instance.respondDialog"
	MethodClickPlayer    = "instance.clickPlayer"
	MethodClickTextDraw  = "instance.clickTextDraw"
	MethodAction         = "instance.action"
	MethodCreateInstance = "instances.create"
	MethodUpdateInstance = "instances.update"
	MethodDeleteInstance = "instances.delete"
	MethodAddCommand     = "instance.commands.add"
	MethodDeleteCommand  = "instance.commands.delete"
)

// Manifest describes one plugin executable. The host reads plugin.json from
// each direct child directory of the plugin directory.
type Manifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Events      []string `json:"events,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
	Restart     *bool    `json:"restart,omitempty"`
}

// Message is the JSON Lines envelope. Data and Params deliberately remain
// open-ended so new protocol fields can be added without changing the host.
type Message struct {
	Type       string          `json:"type"`
	Protocol   int             `json:"protocol,omitempty"`
	ID         uint64          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	InstanceID string          `json:"instanceId,omitempty"`
	Time       *time.Time      `json:"time,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Method     string          `json:"method,omitempty"`
	Params     json.RawMessage `json:"params,omitempty"`
	// A nil Events pointer means the field was not provided; a non-nil empty
	// slice explicitly clears subscriptions. The pointer preserves that
	// distinction on both JSON decode and encode.
	Events  *[]string `json:"events,omitempty"`
	Level   string    `json:"level,omitempty"`
	Message string    `json:"message,omitempty"`
	Code    string    `json:"code,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// Event is the host-to-plugin event shape. Data is intentionally any; the
// host serializes a stable public DTO as JSON.
type Event struct {
	Name       string    `json:"name"`
	InstanceID string    `json:"instanceId,omitempty"`
	Time       time.Time `json:"time"`
	Data       any       `json:"data,omitempty"`
}

// Client event payloads are the stable, plugin-facing DTOs. They deliberately
// use camelCase JSON fields and must not expose internal SA-MP decoder structs.
type JoinedEventData struct {
	PlayerID int `json:"playerId"`
}

type ChatEventData struct {
	PlayerID *int   `json:"playerId,omitempty"`
	Text     string `json:"text"`
	Color    string `json:"color,omitempty"`
}

type PlayerJoinEventData struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type PlayerQuitEventData struct {
	ID int `json:"id"`
}

type ScoreEventData struct {
	ID    int `json:"id"`
	Score int `json:"score"`
	Ping  int `json:"ping"`
}

type DialogEventData struct {
	ID      int    `json:"id"`
	Style   int    `json:"style"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Button1 string `json:"button1"`
	Button2 string `json:"button2"`
}

type TextDrawEventData struct {
	ID              int     `json:"id"`
	Text            string  `json:"text"`
	Style           int     `json:"style"`
	Flags           int     `json:"flags"`
	Shadow          int     `json:"shadow"`
	Outline         int     `json:"outline"`
	Selectable      bool    `json:"selectable"`
	LetterColor     string  `json:"letterColor"`
	BoxColor        string  `json:"boxColor"`
	BackgroundColor string  `json:"backgroundColor"`
	X               float32 `json:"x"`
	Y               float32 `json:"y"`
	LetterWidth     float32 `json:"letterWidth"`
	LetterHeight    float32 `json:"letterHeight"`
	LineWidth       float32 `json:"lineWidth"`
	LineHeight      float32 `json:"lineHeight"`
	ModelID         int     `json:"modelId"`
}

type IDEventData struct {
	ID int `json:"id"`
}

type TextDrawTextEventData struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

type ObjectEventData struct {
	ID      int     `json:"id"`
	ModelID int     `json:"modelId"`
	X       float32 `json:"x"`
	Y       float32 `json:"y"`
	Z       float32 `json:"z"`
}

type VehicleEventData struct {
	ID      int     `json:"id"`
	ModelID int     `json:"modelId"`
	X       float32 `json:"x"`
	Y       float32 `json:"y"`
	Z       float32 `json:"z"`
	Health  float32 `json:"health"`
}

type PlayerSyncEventData struct {
	ID       int     `json:"id"`
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
	Z        float32 `json:"z"`
	Health   float32 `json:"health"`
	Armour   float32 `json:"armour"`
	Skin     int     `json:"skin"`
	Team     int     `json:"team"`
	Rotation float32 `json:"rotation"`
	Color    string  `json:"color,omitempty"`
}

type PositionEventData struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	Z float32 `json:"z"`
}

type AppearanceEventData struct {
	ID       int      `json:"id"`
	X        *float32 `json:"x,omitempty"`
	Y        *float32 `json:"y,omitempty"`
	Z        *float32 `json:"z,omitempty"`
	Skin     *int     `json:"skin,omitempty"`
	Team     *int     `json:"team,omitempty"`
	Rotation *float32 `json:"rotation,omitempty"`
	Color    *string  `json:"color,omitempty"`
}

type VehicleStateEventData struct {
	InVehicle bool `json:"inVehicle"`
	Passenger bool `json:"passenger"`
	VehicleID int  `json:"vehicleId"`
}

type ReasonEventData struct {
	Reason string `json:"reason"`
}

type ErrorEventData struct {
	Message string `json:"message"`
}

// Invoker is implemented by the client manager. It is the complete API
// boundary between a plugin process and SA-MP-Pilot.
type Invoker interface {
	InvokePluginAPI(context.Context, string, string, json.RawMessage) (any, error)
}

// EventSink receives plugin-visible events. The host implements this and the
// manager uses it for both high-level state events and canonical client DTOs.
type EventSink interface {
	Emit(Event)
}

type Info struct {
	Manifest      Manifest `json:"manifest"`
	Status        string   `json:"status"`
	Error         string   `json:"error,omitempty"`
	EventsSent    uint64   `json:"eventsSent"`
	EventsDropped uint64   `json:"eventsDropped"`
	Logs          []Log    `json:"logs,omitempty"`
}

type Log struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type DebugResult struct {
	PluginID   string `json:"pluginId"`
	InstanceID string `json:"instanceId,omitempty"`
	Result     any    `json:"result,omitempty"`
}
