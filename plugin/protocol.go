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
	EventInstanceCreated = "instance.created"
	EventInstanceUpdated = "instance.updated"
	EventInstanceDeleted = "instance.deleted"
	EventChatMessage     = "chat.message"
	EventChatReset       = "chat.reset"
	EventClientPrefix    = "client."
	EventPluginEvent     = "plugin.event"
	EventPluginLog       = "plugin.log"
	EventPluginStatus    = "plugin.status"
)

const (
	MethodListInstances  = "instances.list"
	MethodGetInstance    = "instance.getSnapshot"
	MethodGetInstanceOld = "instance.get"
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
}

// Message is the JSON Lines envelope. Data and Params deliberately remain
// open-ended so new protocol fields can be added without changing the host.
type Message struct {
	Type       string          `json:"type"`
	Protocol   int             `json:"protocol,omitempty"`
	ID         uint64          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	InstanceID string          `json:"instanceId,omitempty"`
	Time       time.Time       `json:"time,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Method     string          `json:"method,omitempty"`
	Params     json.RawMessage `json:"params,omitempty"`
	Events     []string        `json:"events,omitempty"`
	Level      string          `json:"level,omitempty"`
	Message    string          `json:"message,omitempty"`
	Code       string          `json:"code,omitempty"`
	Result     any             `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// Event is the host-to-plugin event shape. Data is intentionally any; the
// host serializes it as JSON and preserves the decoded SA-MP event payload.
type Event struct {
	Name       string    `json:"name"`
	InstanceID string    `json:"instanceId,omitempty"`
	Time       time.Time `json:"time"`
	Data       any       `json:"data,omitempty"`
}

// Invoker is implemented by the client manager. It is the complete API
// boundary between a plugin process and SA-MP-Pilot.
type Invoker interface {
	InvokePluginAPI(context.Context, string, string, json.RawMessage) (any, error)
}

// EventSink receives plugin-visible events. The host implements this and the
// manager uses it for both high-level state events and raw SA-MP events.
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
