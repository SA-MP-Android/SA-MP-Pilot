export type Status = 'disconnected' | 'connecting' | 'connected' | 'error'
export interface Server {
  id: string
  host: string
  port: number
  nickname: string
  password: string
  encoding: 'utf-8' | 'gbk' | 'windows-1251'
  autoConnect: boolean
  emulatePcClientCheck: boolean
}
export interface Connection {
  status: Status
  serverName: string
  error: string
  playerCount: number
  maxPlayers: number
  since?: string
}
export interface Player {
  id: number
  name: string
  score: number
  ping: number
  distance: number
  health: number
  armour: number
  skin: number
  team: number
  rotation: number
  color: string
  x: number
  y: number
  z: number
}
export interface Vehicle {
  id: number
  modelId: number
  distance: number
  health: number
  occupied: boolean
  driverName: string
  x: number
  y: number
  z: number
}
export interface WorldObject {
  id: number
  modelId: number
  distance: number
  x: number
  y: number
  z: number
}
export interface TextDraw {
  id: number
  text: string
  style: number
  letterColor: string
  selectable: boolean
  x: number
  y: number
}
export interface Dialog {
  id: number
  title: string
  message: string
  style: number
  button1: string
  button2: string
  receivedAt: string
}
export interface QuickCommand {
  id: string
  serverId: string
  label: string
  command: string
}
export interface Snapshot {
  revision: number
  syncEpoch: string
  server: Server
  connection: Connection
  chat: { id: number; text: string; color: string; at: string }[]
  players: Player[]
  nearbyPlayers: Player[]
  vehicles: Vehicle[]
  objects: WorldObject[]
  textDraws: TextDraw[]
  dialogs: Dialog[]
  commands: QuickCommand[]
  activeDialog: Dialog | null
  vehicleState: { inVehicle: boolean; passenger: boolean; vehicleId: number }
  keyMask: number
  afk: boolean
  spawned: boolean
  spawnReady: boolean
}
export type SnapshotPath =
  | '/server'
  | '/connection'
  | '/players'
  | '/nearbyPlayers'
  | '/vehicles'
  | '/objects'
  | '/textDraws'
  | '/dialogs'
  | '/commands'
  | '/activeDialog'
  | '/vehicleState'
  | '/keyMask'
  | '/afk'
  | '/spawned'
  | '/spawnReady'
export interface PatchOperation {
  op: 'replace'
  path: SnapshotPath
  value: unknown
}
export interface InstancePatch {
  revision: number
  syncEpoch: string
  operations: PatchOperation[]
}
export interface ChatMessage {
  id: number
  text: string
  color: string
  at: string
}
export interface ChatPage {
  items: ChatMessage[]
  nextBefore?: number
}
export interface Event {
  type: string
  instanceId: string
  data?: Snapshot | InstancePatch | ChatMessage
}
