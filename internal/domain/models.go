package domain

import "time"

type Encoding string

const (
	EncodingUTF8        Encoding = "utf-8"
	EncodingGBK         Encoding = "gbk"
	EncodingWindows1251 Encoding = "windows-1251"
)
const (
	StatusDisconnected     = "disconnected"
	StatusConnecting       = "connecting"
	StatusConnected        = "connected"
	StatusError            = "error"
	ActionChat             = "chat"
	ActionKeys             = "keys"
	ActionAFK              = "afk"
	ActionTeleport         = "teleport"
	ActionEnterVehicle     = "enterVehicle"
	ActionExitVehicle      = "exitVehicle"
	ActionDialog           = "dialog"
	ActionTextDraw         = "textDraw"
	ActionClickPlayer      = "clickPlayer"
	ActionDeferDialog      = "deferDialog"
	ActionShowDialog       = "showDialog"
	ActionDismissDialog    = "dismissDialog"
	EventInstanceCreated   = "instance.created"
	EventInstanceUpdated   = "instance.updated"
	EventInstanceDeleted   = "instance.deleted"
	EventChatMessage       = "chat.message"
	EventChatReset         = "chat.reset"
	MaxChatMessages        = 64
	MaxInstances           = 64
	MaxCommandsPerInstance = 128
	MaxPlayers             = 1004
	MaxVehicles            = 2000
	MaxObjects             = 1000
	MaxTextDraws           = 2048
	MaxDeferredDialogs     = 64
	InvalidPlayerID        = -1
	InvalidVehicleID       = -1
)

type Server struct {
	ID          string   `json:"id"`
	Host        string   `json:"host"`
	Nickname    string   `json:"nickname"`
	Password    string   `json:"password"`
	Port        int      `json:"port"`
	Encoding    Encoding `json:"encoding"`
	AutoConnect bool     `json:"autoConnect"`
}
type Connection struct {
	Status      string     `json:"status"`
	ServerName  string     `json:"serverName"`
	Error       string     `json:"error"`
	PlayerCount int        `json:"playerCount"`
	MaxPlayers  int        `json:"maxPlayers"`
	Since       *time.Time `json:"since,omitempty"`
}
type ChatMessage struct {
	ID    int64     `json:"id"`
	Text  string    `json:"text"`
	Color string    `json:"color"`
	At    time.Time `json:"at"`
}
type Player struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Score    int     `json:"score"`
	Ping     int     `json:"ping"`
	Distance float32 `json:"distance"`
	Health   float32 `json:"health"`
	Armour   float32 `json:"armour"`
	Skin     int     `json:"skin"`
	Team     int     `json:"team"`
	Rotation float32 `json:"rotation"`
	Color    string  `json:"color"`
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
	Z        float32 `json:"z"`
}
type Vehicle struct {
	ID         int     `json:"id"`
	ModelID    int     `json:"modelId"`
	Distance   float32 `json:"distance"`
	Health     float32 `json:"health"`
	X          float32 `json:"x"`
	Y          float32 `json:"y"`
	Z          float32 `json:"z"`
	Occupied   bool    `json:"occupied"`
	DriverName string  `json:"driverName"`
}
type Object struct {
	ID       int     `json:"id"`
	ModelID  int     `json:"modelId"`
	Distance float32 `json:"distance"`
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
	Z        float32 `json:"z"`
}
type TextDraw struct {
	ID              int     `json:"id"`
	Style           int     `json:"style"`
	Text            string  `json:"text"`
	LetterColor     string  `json:"letterColor"`
	BoxColor        string  `json:"boxColor"`
	BackgroundColor string  `json:"backgroundColor"`
	Selectable      bool    `json:"selectable"`
	X               float32 `json:"x"`
	Y               float32 `json:"y"`
	LetterWidth     float32 `json:"letterWidth"`
	LetterHeight    float32 `json:"letterHeight"`
}
type Dialog struct {
	ID         int       `json:"id"`
	Style      int       `json:"style"`
	Title      string    `json:"title"`
	Message    string    `json:"message"`
	Button1    string    `json:"button1"`
	Button2    string    `json:"button2"`
	ReceivedAt time.Time `json:"receivedAt"`
	RawMessage []byte    `json:"-"`
}
type QuickCommand struct {
	ID       string `json:"id"`
	ServerID string `json:"serverId"`
	Label    string `json:"label"`
	Command  string `json:"command"`
}
type VehicleState struct {
	InVehicle bool `json:"inVehicle"`
	Passenger bool `json:"passenger"`
	VehicleID int  `json:"vehicleId"`
}
type Snapshot struct {
	Server        Server         `json:"server"`
	Connection    Connection     `json:"connection"`
	Chat          []ChatMessage  `json:"-"`
	Players       []Player       `json:"players"`
	NearbyPlayers []Player       `json:"nearbyPlayers"`
	Vehicles      []Vehicle      `json:"vehicles"`
	Objects       []Object       `json:"objects"`
	TextDraws     []TextDraw     `json:"textDraws"`
	Dialogs       []Dialog       `json:"dialogs"`
	Commands      []QuickCommand `json:"commands"`
	ActiveDialog  *Dialog        `json:"activeDialog"`
	VehicleState  VehicleState   `json:"vehicleState"`
	KeyMask       int            `json:"keyMask"`
	AFK           bool           `json:"afk"`
	Spawned       bool           `json:"spawned"`
}

type ChatPage struct {
	Items      []ChatMessage `json:"items"`
	NextBefore int64         `json:"nextBefore,omitempty"`
}
type Event struct {
	Type       string `json:"type"`
	InstanceID string `json:"instanceId"`
	Data       any    `json:"data,omitempty"`
}
