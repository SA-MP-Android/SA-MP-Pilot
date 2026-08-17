import { useCallback, useEffect, useMemo, useState } from 'react'
import { Plug, RefreshCw, RotateCw, Square, Play } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api'
import {
  PLUGIN_ACTION_RESTART,
  PLUGIN_ACTION_START,
  PLUGIN_ACTION_STOP,
  PLUGIN_LOG,
  PLUGIN_STATUS,
  PLUGIN_STATUS_RUNNING,
} from '@/constants'
import type { Event, PluginInfo, PluginLogEvent, PluginStatusEvent } from '@/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'

interface PluginManagerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  streamEvents: Event[]
}

export function PluginManager({ open, onOpenChange, streamEvents }: PluginManagerProps) {
  const { t } = useTranslation()
  const [plugins, setPlugins] = useState<PluginInfo[]>([])
  const [selected, setSelected] = useState('')
  const plugin = useMemo(() => plugins.find((item) => item.manifest.id === selected), [plugins, selected])

  const refresh = useCallback(async () => {
    try {
      const items = await api.plugins()
      setPlugins(items)
      setSelected((current) =>
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
    const latest = streamEvents.at(-1)
    if (!latest || (latest.type !== PLUGIN_LOG && latest.type !== PLUGIN_STATUS)) return
    const data = latest.data as PluginLogEvent | PluginStatusEvent
    if (!data?.pluginId) return
    setPlugins((items) =>
      items.map((item) => {
        if (item.manifest.id !== data.pluginId) return item
        if (latest.type === PLUGIN_STATUS) {
          const status = data as PluginStatusEvent
          return { ...item, status: status.status, error: status.error }
        }
        const log = data as PluginLogEvent
        return {
          ...item,
          logs: [...(item.logs ?? []), { at: log.at, level: log.level, message: log.message }].slice(-20),
        }
      }),
    )
  }, [streamEvents])

  async function lifecycle(
    action: typeof PLUGIN_ACTION_START | typeof PLUGIN_ACTION_STOP | typeof PLUGIN_ACTION_RESTART,
  ) {
    if (!plugin) return
    try {
      if (action === PLUGIN_ACTION_START) await api.startPlugin(plugin.manifest.id)
      if (action === PLUGIN_ACTION_STOP) await api.stopPlugin(plugin.manifest.id)
      if (action === PLUGIN_ACTION_RESTART) await api.restartPlugin(plugin.manifest.id)
      await refresh()
    } catch (error) {
      toast.error((error as Error).message)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[calc(100dvh-2rem)] max-h-[90dvh] min-h-0 max-w-5xl flex-col overflow-hidden">
        <DialogHeader className="shrink-0">
          <DialogTitle className="flex items-center gap-2">
            <Plug size={18} />
            {t('plugins.manageTitle')}
          </DialogTitle>
          <DialogDescription>{t('plugins.manageDescription')}</DialogDescription>
        </DialogHeader>
        <ScrollArea className="min-h-0 flex-1">
          <div className="grid gap-4 pr-3 lg:grid-cols-[15rem_1fr]">
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                  {t('plugins.installed')}
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => void refresh()}
                  aria-label={t('plugins.refresh')}
                >
                  <RefreshCw size={15} />
                </Button>
              </div>
              {plugins.length ? (
                <div className="space-y-2">
                  {plugins.map((item) => (
                    <Card
                      key={item.manifest.id}
                      className={`cursor-pointer p-3 ${selected === item.manifest.id ? 'border-primary bg-primary/5' : ''}`}
                      onClick={() => setSelected(item.manifest.id)}
                    >
                      <div className="flex items-center gap-2 text-sm font-medium">
                        <span
                          className={`h-2 w-2 rounded-full ${item.status === PLUGIN_STATUS_RUNNING ? 'bg-emerald-500' : 'bg-muted-foreground'}`}
                        />
                        <span className="truncate">{item.manifest.name || item.manifest.id}</span>
                      </div>
                      <div className="text-muted-foreground mt-1 text-xs">{item.status}</div>
                    </Card>
                  ))}
                </div>
              ) : (
                <p className="text-muted-foreground text-sm">{t('plugins.empty')}</p>
              )}
            </div>
            <div className="min-w-0 space-y-4">
              {plugin ? (
                <>
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <h2 className="font-semibold">{plugin.manifest.name || plugin.manifest.id}</h2>
                        <Badge variant={plugin.status === PLUGIN_STATUS_RUNNING ? 'default' : 'outline'}>
                          {plugin.status}
                        </Badge>
                      </div>
                      <p className="text-muted-foreground mt-1 text-sm">
                        {plugin.manifest.description || t('plugins.noDescription')}
                      </p>
                    </div>
                    <span className="text-muted-foreground text-xs">
                      {t('plugins.events', { count: plugin.eventsSent })}
                      {plugin.eventsDropped > 0 &&
                        ` · ${t('plugins.droppedEvents', { count: plugin.eventsDropped })}`}
                    </span>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Button variant="outline" size="sm" onClick={() => void lifecycle(PLUGIN_ACTION_START)}>
                      <Play size={14} />
                      {t('plugins.start')}
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => void lifecycle(PLUGIN_ACTION_STOP)}>
                      <Square size={14} />
                      {t('plugins.stop')}
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => void lifecycle(PLUGIN_ACTION_RESTART)}>
                      <RotateCw size={14} />
                      {t('plugins.restart')}
                    </Button>
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Card className="p-4">
                      <div className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                        {t('plugins.pluginId')}
                      </div>
                      <div className="mt-2 truncate font-mono text-sm">{plugin.manifest.id}</div>
                    </Card>
                    <Card className="p-4">
                      <div className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                        {t('plugins.version')}
                      </div>
                      <div className="mt-2 font-mono text-sm">{plugin.manifest.version || '-'}</div>
                    </Card>
                  </div>
                  <Card className="p-4">
                    <div className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                      {t('plugins.subscriptions')}
                    </div>
                    <div className="mt-2 font-mono text-xs break-all">
                      {plugin.manifest.events?.length ? plugin.manifest.events.join(', ') : '*'}
                    </div>
                  </Card>
                  {plugin.error && (
                    <Card className="border-red-500/40 bg-red-500/5 p-4">
                      <div className="text-sm font-medium">{t('plugins.error')}</div>
                      <div className="text-muted-foreground mt-1 text-xs break-words">{plugin.error}</div>
                    </Card>
                  )}
                  <div className="space-y-2">
                    <div className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                      {t('plugins.logs')}
                    </div>
                    <ScrollArea className="bg-muted/50 h-52 rounded-md">
                      <div className="space-y-1 p-3 font-mono text-[11px] break-all whitespace-pre-wrap">
                        {plugin.logs?.length
                          ? plugin.logs.slice(-20).map((log, index) => (
                              <div key={`${log.at}-${index}`}>
                                <span className="text-muted-foreground">{log.level}</span> {log.message}
                              </div>
                            ))
                          : t('plugins.logsPlaceholder')}
                      </div>
                    </ScrollArea>
                  </div>
                </>
              ) : (
                <div className="text-muted-foreground grid min-h-64 place-content-center text-center text-sm">
                  {t('plugins.select')}
                </div>
              )}
            </div>
          </div>
        </ScrollArea>
      </DialogContent>
    </Dialog>
  )
}
