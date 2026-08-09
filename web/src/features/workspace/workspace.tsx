import { useEffect, useRef, useState } from 'react'
import { Activity, Car, MessageSquare, PanelsTopLeft, Settings, Trash2, Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api'
import {
  ACTION_AFK,
  ACTION_CHAT,
  ACTION_CLICK_PLAYER,
  ACTION_DISMISS_DIALOG,
  ACTION_ENTER_VEHICLE,
  ACTION_EXIT_VEHICLE,
  ACTION_KEYS,
  ACTION_SHOW_DIALOG,
  ACTION_TELEPORT,
  ACTION_TEXT_DRAW,
  CHAT_BOTTOM_TOLERANCE_PX,
  INVALID_PLAYER_PING,
  INVALID_PLAYER_PING_SIGNED,
  KEY_NAMES,
  STATUS_CONNECTED,
  STATUS_CONNECTING,
  STATUS_ERROR,
  TAB_CHAT,
} from '@/constants'
import type { QuickCommand, Server, Snapshot } from '@/types'
import { EntityGrid } from '@/components/entity-grid'
import { SAMPColoredText } from '@/components/samp-colored-text'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ServerForm } from '@/features/instances/server-form'

export function Workspace({
  value,
  onDelete,
  onLoadOlderChat,
}: {
  value: Snapshot
  onDelete: () => void
  onLoadOlderChat?: () => void
}) {
  const { t } = useTranslation()
  const server = value.server
  const connection = value.connection
  const [message, setMessage] = useState('')
  const [editing, setEditing] = useState(false)
  const [serverForm, setServerForm] = useState<Omit<Server, 'id'>>({ ...server })
  const [commandLabel, setCommandLabel] = useState('')
  const [commandText, setCommandText] = useState('')
  const [showPlayerColors, setShowPlayerColors] = useState(true)
  const [pendingCommandDelete, setPendingCommandDelete] = useState<QuickCommand | null>(null)
  const [activeTab, setActiveTab] = useState(TAB_CHAT)
  const chatViewportRef = useRef<HTMLDivElement>(null)
  const followChatRef = useRef(true)
  const act = (action: string, data: unknown = {}) =>
    api.action(server.id, action, data).catch((error) => toast.error(error.message))

  useEffect(() => {
    if (activeTab !== TAB_CHAT || !followChatRef.current) return
    const frame = requestAnimationFrame(() => {
      if (chatViewportRef.current) chatViewportRef.current.scrollTop = chatViewportRef.current.scrollHeight
    })
    return () => cancelAnimationFrame(frame)
  }, [activeTab, value.chat])
  useEffect(() => {
    if (value.activeDialog) setEditing(false)
  }, [value.activeDialog])

  const stateLabel = value.afk
    ? t('status.afk')
    : !value.spawned
      ? t('status.awaitingSpawn')
      : value.vehicleState.inVehicle
        ? `${t(value.vehicleState.passenger ? 'status.passenger' : 'status.driver')} · ${t('status.vehicle', { id: value.vehicleState.vehicleId })}`
        : t('status.onFoot')

  return (
    <Card className="flex h-[calc(100dvh-8.5rem)] min-h-144 min-w-0 flex-col overflow-hidden shadow-lg">
      <CardHeader className="flex-row flex-wrap items-center gap-3 space-y-0 border-b p-5">
        <div>
          <CardTitle>{connection.serverName || `${server.host}:${server.port}`}</CardTitle>
          <CardDescription>
            {server.host}:{server.port} · {server.nickname}
          </CardDescription>
        </div>
        <Badge
          variant="outline"
          className={
            connection.status === STATUS_CONNECTED
              ? 'border-emerald-500/50 bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'
              : connection.status === STATUS_ERROR
                ? 'text-red-500'
                : ''
          }
        >
          {connection.status}
        </Badge>
        {connection.status === STATUS_CONNECTED && (
          <Badge>
            {connection.playerCount}/{connection.maxPlayers}
          </Badge>
        )}
        {connection.status === STATUS_CONNECTED && <Badge variant="secondary">{stateLabel}</Badge>}
        <div className="ml-auto flex gap-2">
          <Button variant="outline" onClick={() => setEditing(true)}>
            <Settings size={15} />
            {t('common.settings')}
          </Button>
          {connection.status === STATUS_CONNECTED || connection.status === STATUS_CONNECTING ? (
            <Button variant="outline" onClick={() => api.disconnect(server.id)}>
              {t('common.disconnect')}
            </Button>
          ) : (
            <Button onClick={() => api.connect(server.id)}>{t('common.connect')}</Button>
          )}
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="destructive" size="icon" aria-label={t('server.deleteLabel')}>
                <Trash2 size={15} />
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t('server.deleteTitle')}</AlertDialogTitle>
                <AlertDialogDescription>{t('server.deleteDescription')}</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
                <AlertDialogAction onClick={onDelete}>{t('common.delete')}</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
        {connection.error && <p className="w-full text-sm text-red-500">{connection.error}</p>}
      </CardHeader>
      <Tabs
        value={activeTab}
        onValueChange={(tab) => {
          setActiveTab(tab)
          if (tab === TAB_CHAT) followChatRef.current = true
        }}
        className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden p-5 pt-4"
      >
        <TabsList className="w-fit shrink-0">
          <TabsTrigger value={TAB_CHAT}>
            <MessageSquare size={14} />
            {t('tabs.chat')}
          </TabsTrigger>
          <TabsTrigger value="textdraw">
            <Activity size={14} />
            {t('tabs.textdraw')}
          </TabsTrigger>
          <TabsTrigger value="dialogs">
            <PanelsTopLeft size={14} />
            {t('tabs.dialogs')}
          </TabsTrigger>
          <TabsTrigger value="players">
            <Users size={14} />
            {t('tabs.players')}
          </TabsTrigger>
          <TabsTrigger value="nearby">
            <Car size={14} />
            {t('tabs.nearby')}
          </TabsTrigger>
          <TabsTrigger value="controls">{t('tabs.controls')}</TabsTrigger>
        </TabsList>
        <TabsContent value={TAB_CHAT} className="mt-0 flex min-h-0 flex-1 flex-col overflow-hidden">
          <ScrollArea
            viewportRef={chatViewportRef}
            onViewportScroll={(event) => {
              const viewport = event.currentTarget
              if (viewport.scrollTop <= CHAT_BOTTOM_TOLERANCE_PX) onLoadOlderChat?.()
              followChatRef.current =
                viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= CHAT_BOTTOM_TOLERANCE_PX
            }}
            className="min-h-0 flex-1 rounded-lg border p-3"
          >
            <div className="space-y-1 font-mono text-sm">
              {value.chat.map((entry) => (
                <div key={entry.id} style={{ color: entry.color }}>
                  <span className="text-muted-foreground mr-2">
                    {new Date(entry.at).toLocaleTimeString()}
                  </span>
                  <SAMPColoredText text={entry.text} />
                </div>
              ))}
            </div>
          </ScrollArea>
          <form
            className="mt-3 flex min-w-0 gap-2 p-px"
            onSubmit={(event) => {
              event.preventDefault()
              if (message.trim()) {
                void act(ACTION_CHAT, { text: message })
                setMessage('')
              }
            }}
          >
            <Input
              className="min-w-0"
              placeholder={t('chat.placeholder')}
              value={message}
              onChange={(event) => setMessage(event.target.value)}
              disabled={connection.status !== STATUS_CONNECTED}
            />
            <Button disabled={connection.status !== STATUS_CONNECTED}>{t('common.send')}</Button>
          </form>
          <div className="mt-3 flex flex-wrap gap-2">
            {value.commands
              .filter((command) => command.serverId === server.id)
              .map((command) => (
                <div key={command.id} className="flex">
                  <Button
                    size="sm"
                    variant="secondary"
                    className="rounded-r-none"
                    onClick={() => act(ACTION_CHAT, { text: command.command })}
                  >
                    {command.label}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    className="rounded-l-none border-l-0 px-2"
                    onClick={() => setPendingCommandDelete(command)}
                    aria-label={t('chat.deleteQuick', { name: command.label })}
                  >
                    ×
                  </Button>
                </div>
              ))}
          </div>
          <form
            className="mt-3 grid min-w-0 gap-2 p-px sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_auto]"
            onSubmit={async (event) => {
              event.preventDefault()
              try {
                await api.addCommand(server.id, { label: commandLabel, command: commandText })
                setCommandLabel('')
                setCommandText('')
              } catch (error) {
                toast.error((error as Error).message)
              }
            }}
          >
            <Input
              placeholder={t('chat.quickName')}
              value={commandLabel}
              onChange={(event) => setCommandLabel(event.target.value)}
            />
            <Input
              placeholder={t('chat.placeholder')}
              value={commandText}
              onChange={(event) => setCommandText(event.target.value)}
            />
            <Button>{t('chat.addQuick')}</Button>
          </form>
          <AlertDialog
            open={pendingCommandDelete !== null}
            onOpenChange={(open) => {
              if (!open) setPendingCommandDelete(null)
            }}
          >
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t('chat.deleteQuickTitle')}</AlertDialogTitle>
                <AlertDialogDescription>
                  {t('chat.deleteQuickDescription', { name: pendingCommandDelete?.label ?? '' })}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
                <AlertDialogAction
                  onClick={async () => {
                    if (!pendingCommandDelete) return
                    try {
                      await api.removeCommand(server.id, pendingCommandDelete.id)
                      setPendingCommandDelete(null)
                    } catch (error) {
                      toast.error((error as Error).message)
                    }
                  }}
                >
                  {t('common.delete')}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </TabsContent>
        <TabsContent value="textdraw" className="mt-0 min-h-0 flex-1 overflow-hidden">
          <ScrollArea className="h-full pr-3">
            <EntityGrid
              empty={t('textdraw.empty')}
              rows={value.textDraws.map((entry) => ({
                id: entry.id,
                title: entry.text,
                subtitle: t('textdraw.details', {
                  id: entry.id,
                  style: entry.style,
                  x: entry.x.toFixed(1),
                  y: entry.y.toFixed(1),
                }),
                actions: entry.selectable ? (
                  <Button onClick={() => act(ACTION_TEXT_DRAW, { textDrawId: entry.id })}>
                    {t('common.click')}
                  </Button>
                ) : undefined,
              }))}
            />
          </ScrollArea>
        </TabsContent>
        <TabsContent value="dialogs" className="mt-0 min-h-0 flex-1 overflow-auto">
          <EntityGrid
            empty={t('dialogs.deferredEmpty')}
            rows={value.dialogs.map((entry) => ({
              id: entry.id,
              title: entry.title,
              subtitle: entry.message,
              actions: (
                <>
                  <Button variant="outline" onClick={() => act(ACTION_SHOW_DIALOG, { dialogId: entry.id })}>
                    {t('common.open')}
                  </Button>
                  <Button
                    variant="destructive"
                    onClick={() => act(ACTION_DISMISS_DIALOG, { dialogId: entry.id })}
                  >
                    {t('common.remove')}
                  </Button>
                </>
              ),
            }))}
          />
        </TabsContent>
        <TabsContent value="players" className="mt-0 flex min-h-0 flex-1 flex-col overflow-hidden">
          <Label className="mb-3 flex shrink-0 items-center gap-2 self-end text-sm">
            <Checkbox
              checked={showPlayerColors}
              onCheckedChange={(checked) => setShowPlayerColors(checked === true)}
            />
            {t('players.showColors')}
          </Label>
          <ScrollArea className="min-h-0 flex-1 pr-3">
            <div className="overflow-hidden rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>ID</TableHead>
                    <TableHead>{t('players.player')}</TableHead>
                    <TableHead>{t('players.score')}</TableHead>
                    <TableHead>{t('players.skin')}</TableHead>
                    <TableHead>{t('players.ping')}</TableHead>
                    <TableHead className="text-right">{t('players.actions')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {value.players.length ? (
                    value.players.map((player) => (
                      <TableRow key={player.id}>
                        <TableCell>{player.id}</TableCell>
                        <TableCell
                          className="font-medium"
                          style={showPlayerColors && player.color ? { color: player.color } : undefined}
                        >
                          {player.name || `Player #${player.id}`}
                        </TableCell>
                        <TableCell>{player.score}</TableCell>
                        <TableCell>{player.skin}</TableCell>
                        <TableCell>
                          {player.ping === INVALID_PLAYER_PING || player.ping === INVALID_PLAYER_PING_SIGNED
                            ? '—'
                            : `${player.ping} ms`}
                        </TableCell>
                        <TableCell className="text-right">
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => act(ACTION_CLICK_PLAYER, { playerId: player.id })}
                          >
                            {t('common.select')}
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))
                  ) : (
                    <TableRow>
                      <TableCell colSpan={6} className="text-muted-foreground h-40 text-center">
                        {t('players.empty')}
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
          </ScrollArea>
        </TabsContent>
        <TabsContent value="nearby" className="mt-0 min-h-0 flex-1 overflow-hidden">
          <ScrollArea className="h-full pr-3">
            <div className="grid gap-3 md:grid-cols-3">
              <EntityGrid
                empty={t('nearby.playersEmpty')}
                rows={value.nearbyPlayers.map((entry) => ({
                  id: entry.id,
                  title: entry.name,
                  subtitle: t('nearby.playerDetails', {
                    distance: entry.distance.toFixed(1),
                    health: entry.health,
                    skin: entry.skin,
                  }),
                  actions: (
                    <Button variant="outline" onClick={() => act(ACTION_TELEPORT, entry)}>
                      {t('common.teleport')}
                    </Button>
                  ),
                }))}
              />
              <EntityGrid
                empty={t('nearby.vehiclesEmpty')}
                rows={value.vehicles.map((entry) => ({
                  id: entry.id,
                  title: t('nearby.vehicle', { id: entry.id }),
                  subtitle: t('nearby.modelDistance', {
                    model: entry.modelId,
                    distance: entry.distance.toFixed(1),
                  }),
                  actions: (
                    <>
                      <Button variant="outline" onClick={() => act(ACTION_TELEPORT, entry)}>
                        {t('common.teleport')}
                      </Button>
                      <Button
                        onClick={() => act(ACTION_ENTER_VEHICLE, { vehicleId: entry.id, passenger: false })}
                      >
                        {t('nearby.drive')}
                      </Button>
                      <Button
                        variant="secondary"
                        onClick={() => act(ACTION_ENTER_VEHICLE, { vehicleId: entry.id, passenger: true })}
                      >
                        {t('nearby.passenger')}
                      </Button>
                    </>
                  ),
                }))}
              />
              <EntityGrid
                empty={t('nearby.objectsEmpty')}
                rows={value.objects.map((entry) => ({
                  id: entry.id,
                  title: t('nearby.object', { id: entry.id }),
                  subtitle: t('nearby.modelDistance', {
                    model: entry.modelId,
                    distance: entry.distance.toFixed(1),
                  }),
                  actions: (
                    <Button variant="outline" onClick={() => act(ACTION_TELEPORT, entry)}>
                      {t('common.teleport')}
                    </Button>
                  ),
                }))}
              />
            </div>
          </ScrollArea>
        </TabsContent>
        <TabsContent value="controls" className="mt-0 min-h-0 flex-1 overflow-auto">
          <div className="space-y-5">
            <section>
              <h3 className="text-muted-foreground mb-2 text-sm">{t('controls.keys')}</h3>
              <div className="flex flex-wrap gap-2">
                {KEY_NAMES.map((key, index) => (
                  <Button key={key} variant="outline" onClick={() => act(ACTION_KEYS, { mask: 1 << index })}>
                    {key}
                  </Button>
                ))}
              </div>
            </section>
            <section>
              <h3 className="text-muted-foreground mb-2 text-sm">{t('controls.state')}</h3>
              <Button
                variant={value.afk ? 'default' : 'outline'}
                onClick={() => act(ACTION_AFK, { enabled: !value.afk })}
              >
                {t(value.afk ? 'controls.exitAfk' : 'controls.enterAfk')}
              </Button>
              {value.vehicleState.inVehicle && (
                <Button className="ml-2" variant="outline" onClick={() => act(ACTION_EXIT_VEHICLE)}>
                  {t('controls.leaveVehicle', { id: value.vehicleState.vehicleId })}
                </Button>
              )}
            </section>
          </div>
        </TabsContent>
      </Tabs>
      <Dialog open={editing} onOpenChange={setEditing}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('server.edit')}</DialogTitle>
          </DialogHeader>
          <ServerForm
            value={serverForm}
            showAutoConnect
            submitLabel={t('common.save')}
            onChange={setServerForm}
            onCancel={() => setEditing(false)}
            onSubmit={async (event) => {
              event.preventDefault()
              try {
                await api.update(server.id, serverForm)
                setEditing(false)
                toast.success(t('server.saved'))
              } catch (error) {
                toast.error((error as Error).message)
              }
            }}
          />
        </DialogContent>
      </Dialog>
    </Card>
  )
}
