import { useCallback, useEffect, useMemo, useState } from 'react'
import { Code2, RefreshCw, Terminal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api'
import { PLUGIN_EVENT, PLUGIN_LOG, PLUGIN_STATUS, PLUGIN_STATUS_RUNNING } from '@/constants'
import type {
  Event,
  PluginEventEnvelope,
  PluginInfo,
  PluginLogEvent,
  PluginStatusEvent,
  Snapshot,
} from '@/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { JavaScriptEditor } from './javascript-editor'

const defaultCode = `const snapshot = await api.getSnapshot()
return {
  status: snapshot.connection.status,
  players: snapshot.players.length,
  position: snapshot.players.find((player) => player.id === 0),
}`

interface PluginConsoleProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialInstanceId: string
  instances: Snapshot[]
  streamEvents: Event[]
}

export function PluginConsole({
  open,
  onOpenChange,
  initialInstanceId,
  instances,
  streamEvents,
}: PluginConsoleProps) {
  const { t } = useTranslation()
  const [plugins, setPlugins] = useState<PluginInfo[]>([])
  const [selectedPlugin, setSelectedPlugin] = useState('')
  const [selectedInstance, setSelectedInstance] = useState(initialInstanceId)
  const [code, setCode] = useState(defaultCode)
  const [output, setOutput] = useState('')
  const [running, setRunning] = useState(false)
  const plugin = useMemo(
    () => plugins.find((item) => item.manifest.id === selectedPlugin),
    [plugins, selectedPlugin],
  )
  const instance = useMemo(
    () => instances.find((item) => item.server.id === selectedInstance),
    [instances, selectedInstance],
  )
  const liveEvents = useMemo(
    () =>
      streamEvents
        .filter((item) => {
          if (item.type === PLUGIN_EVENT) {
            return (item.data as PluginEventEnvelope | undefined)?.pluginId === selectedPlugin
          }
          if (item.type === PLUGIN_LOG || item.type === PLUGIN_STATUS) {
            return (item.data as PluginLogEvent | PluginStatusEvent | undefined)?.pluginId === selectedPlugin
          }
          return false
        })
        .slice(-40),
    [selectedPlugin, streamEvents],
  )

  const refresh = useCallback(async () => {
    try {
      const items = await api.plugins()
      setPlugins(items)
      setSelectedPlugin((current) =>
        items.some((item) => item.manifest.id === current) ? current : (items[0]?.manifest.id ?? ''),
      )
    } catch (error) {
      toast.error((error as Error).message)
    }
  }, [])

  useEffect(() => {
    if (open) void refresh()
  }, [open, refresh])

  useEffect(() => {
    if (!open) return
    setSelectedInstance((current) => {
      if (instances.some((item) => item.server.id === current)) return current
      if (instances.some((item) => item.server.id === initialInstanceId)) return initialInstanceId
      return instances[0]?.server.id ?? ''
    })
  }, [open, initialInstanceId, instances])

  useEffect(() => {
    const latest = streamEvents.at(-1)
    if (!latest || latest.type !== PLUGIN_STATUS) return
    const data = latest.data as PluginStatusEvent
    if (!data?.pluginId) return
    setPlugins((items) =>
      items.map((item) =>
        item.manifest.id === data.pluginId ? { ...item, status: data.status, error: data.error } : item,
      ),
    )
  }, [streamEvents])

  async function evaluate() {
    if (!plugin || !selectedInstance) return
    setRunning(true)
    try {
      const result = await api.debugPlugin(plugin.manifest.id, selectedInstance, code)
      setOutput(JSON.stringify(result.result ?? null, null, 2))
      await refresh()
    } catch (error) {
      const message = (error as Error).message
      setOutput(message)
      toast.error(message)
    } finally {
      setRunning(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[calc(100dvh-2rem)] max-h-[90dvh] min-h-0 max-w-6xl flex-col overflow-hidden">
        <DialogHeader className="shrink-0">
          <DialogTitle className="flex items-center gap-2">
            <Code2 size={18} />
            {t('plugins.title')}
          </DialogTitle>
          <DialogDescription>{t('plugins.description')}</DialogDescription>
        </DialogHeader>
        <ScrollArea className="min-h-0 flex-1">
          <div className="grid gap-4 pr-3 xl:grid-cols-[minmax(0,1fr)_19rem]">
            <div className="min-w-0 space-y-4">
              <Card className="grid gap-4 p-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="plugin-debug-target">{t('plugins.targetPlugin')}</Label>
                  {plugins.length ? (
                    <Select value={selectedPlugin} onValueChange={setSelectedPlugin}>
                      <SelectTrigger id="plugin-debug-target">
                        <SelectValue placeholder={t('plugins.selectPlugin')} />
                      </SelectTrigger>
                      <SelectContent>
                        {plugins.map((item) => (
                          <SelectItem key={item.manifest.id} value={item.manifest.id}>
                            <span className="flex items-center gap-2">
                              <span
                                className={`h-2 w-2 rounded-full ${item.status === PLUGIN_STATUS_RUNNING ? 'bg-emerald-500' : 'bg-muted-foreground'}`}
                              />
                              {item.manifest.name || item.manifest.id}
                            </span>
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <div className="border-input text-muted-foreground flex h-9 items-center rounded-md border px-3 text-sm">
                      {t('plugins.empty')}
                    </div>
                  )}
                </div>
                <div className="space-y-2">
                  <Label htmlFor="plugin-debug-instance">{t('plugins.targetInstance')}</Label>
                  {instances.length ? (
                    <Select value={selectedInstance} onValueChange={setSelectedInstance}>
                      <SelectTrigger id="plugin-debug-instance">
                        <SelectValue placeholder={t('plugins.selectInstance')} />
                      </SelectTrigger>
                      <SelectContent>
                        {instances.map((item) => (
                          <SelectItem key={item.server.id} value={item.server.id}>
                            {item.connection.serverName || `${item.server.host}:${item.server.port}`}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <div className="border-input text-muted-foreground flex h-9 items-center rounded-md border px-3 text-sm">
                      {t('plugins.noInstances')}
                    </div>
                  )}
                </div>
              </Card>
              <div className="flex items-center justify-between gap-3">
                <div className="text-muted-foreground flex min-w-0 items-center gap-2 text-xs">
                  {plugin ? (
                    <>
                      <Badge variant={plugin.status === PLUGIN_STATUS_RUNNING ? 'default' : 'outline'}>
                        {plugin.status}
                      </Badge>
                      <span className="truncate">
                        {plugin.manifest.description || t('plugins.noDescription')}
                      </span>
                    </>
                  ) : (
                    t('plugins.selectPlugin')
                  )}
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => void refresh()}
                  aria-label={t('plugins.refresh')}
                >
                  <RefreshCw size={15} />
                </Button>
              </div>
              <JavaScriptEditor value={code} onChange={setCode} ariaLabel={t('plugins.code')} />
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  onClick={() => void evaluate()}
                  disabled={running || !selectedInstance || plugin?.status !== PLUGIN_STATUS_RUNNING}
                >
                  <Terminal size={15} />
                  {running ? t('plugins.running') : t('plugins.run')}
                </Button>
                <span className="text-muted-foreground text-xs">
                  {instance ? t('plugins.instance', { id: instance.server.id }) : t('plugins.noInstances')}
                </span>
              </div>
              <div className="space-y-2">
                <div className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                  {t('plugins.output')}
                </div>
                <ScrollArea className="bg-muted/50 h-48 min-h-20 rounded-md">
                  <div className="p-3 font-mono text-xs break-all whitespace-pre-wrap">
                    {output || t('plugins.outputPlaceholder')}
                  </div>
                </ScrollArea>
              </div>
            </div>
            <div className="min-w-0 space-y-4">
              <Card className="space-y-3 p-4">
                <div className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                  {t('plugins.debugTarget')}
                </div>
                {plugin ? (
                  <div>
                    <div className="font-medium">{plugin.manifest.name || plugin.manifest.id}</div>
                    <div className="text-muted-foreground mt-1 font-mono text-xs">{plugin.manifest.id}</div>
                  </div>
                ) : (
                  <div className="text-muted-foreground text-sm">{t('plugins.selectPlugin')}</div>
                )}
                <div className="border-border border-t pt-3">
                  <div className="text-muted-foreground text-xs">{t('plugins.targetInstance')}</div>
                  <div className="mt-1 truncate text-sm">
                    {instance
                      ? instance.connection.serverName || `${instance.server.host}:${instance.server.port}`
                      : t('plugins.noInstances')}
                  </div>
                </div>
              </Card>
              <div className="space-y-2">
                <div className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                  {t('plugins.liveEvents')}
                </div>
                <ScrollArea className="bg-muted/50 h-[28rem] rounded-md">
                  <div className="p-3 font-mono text-[11px] break-all whitespace-pre-wrap">
                    {liveEvents.length
                      ? liveEvents
                          .map((event) => JSON.stringify({ type: event.type, data: event.data }))
                          .join('\n')
                      : t('plugins.liveEventsPlaceholder')}
                  </div>
                </ScrollArea>
              </div>
            </div>
          </div>
        </ScrollArea>
      </DialogContent>
    </Dialog>
  )
}
