import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { ChevronRight, Code2, Globe2, Plus, Plug, Radio, Settings2, Telescope } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Toaster, toast } from 'sonner'
import '@/i18n'
import { api, events } from '@/api'
import {
  DEFAULT_HOST,
  DEFAULT_NICKNAME,
  DEFAULT_PORT,
  MAX_CHAT_MESSAGES,
  PLUGIN_EVENT,
  PLUGIN_LOG,
  PLUGIN_STATUS,
  STATUS_CONNECTED,
  STATUS_ERROR,
} from '@/constants'
import { changeLanguage, supportedLanguages } from '@/i18n'
import type { SupportedLanguage } from '@/i18n'
import type { Event, Server, Snapshot } from '@/types'
import { Button } from '@/components/ui/button'
import { Card, CardDescription, CardHeader } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ServerForm } from '@/features/instances/server-form'
import { Workspace } from '@/features/workspace/workspace'
import { SettingsDialog } from '@/features/settings/settings-dialog'
import { InstanceSyncController } from '@/lib/instance-sync'

const LazyServerDialog = lazy(() =>
  import('@/features/dialogs/server-dialog').then(({ ServerDialog }) => ({ default: ServerDialog })),
)
const LazyPluginConsole = lazy(() =>
  import('@/features/plugins/plugin-console').then(({ PluginConsole }) => ({ default: PluginConsole })),
)
const LazyPluginManager = lazy(() =>
  import('@/features/plugins/plugin-manager').then(({ PluginManager }) => ({ default: PluginManager })),
)

const defaultServer: Omit<Server, 'id'> = {
  host: DEFAULT_HOST,
  port: DEFAULT_PORT,
  nickname: DEFAULT_NICKNAME,
  password: '',
  encoding: 'utf-8',
  autoConnect: false,
  emulatePcClientCheck: false,
}

export default function App() {
  const { t, i18n } = useTranslation()
  const [items, setItems] = useState<Snapshot[]>([])
  const [selected, setSelected] = useState('')
  const [adding, setAdding] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [pluginsOpen, setPluginsOpen] = useState(false)
  const [pluginsLoaded, setPluginsLoaded] = useState(false)
  const [debugOpen, setDebugOpen] = useState(false)
  const [debugLoaded, setDebugLoaded] = useState(false)
  const [pluginEvents, setPluginEvents] = useState<Event[]>([])
  const [form, setForm] = useState(defaultServer)
  const chatBefore = useRef<Record<string, number | undefined>>({})
  const loadingChat = useRef(new Set<string>())
  const sync = useRef<InstanceSyncController | null>(null)
  const current = useMemo(() => items.find((item) => item.server.id === selected), [items, selected])

  const openPlugins = () => {
    setPluginsLoaded(true)
    setPluginsOpen(true)
  }

  const openDebug = () => {
    setDebugLoaded(true)
    setDebugOpen(true)
  }

  useEffect(() => {
    const controller = new InstanceSyncController(
      { list: api.list, get: api.get },
      setItems,
      (error) => toast.error(error.message),
      (id) => {
        chatBefore.current[id] = undefined
      },
    )
    sync.current = controller
    void controller.reconcile()
    const stop = events(
      (event) => {
        controller.handle(event)
        if (event.type === PLUGIN_EVENT || event.type === PLUGIN_LOG || event.type === PLUGIN_STATUS) {
          setPluginEvents((current) => [...current, event].slice(-200))
        }
      },
      () => void controller.reconcile(),
    )
    return () => {
      stop()
      controller.dispose()
      if (sync.current === controller) sync.current = null
    }
  }, [])

  useEffect(() => {
    if (items.some((item) => item.server.id === selected)) return
    setSelected(items[0]?.server.id ?? '')
  }, [items, selected])

  const loadChat = useCallback(async (id: string, older = false) => {
    if (loadingChat.current.has(id)) return
    const before = older ? chatBefore.current[id] : undefined
    if (older && !before) return
    loadingChat.current.add(id)
    try {
      const page = await api.chat(id, before)
      sync.current?.update(id, (item) => {
        const messages = new Map(item.chat.map((message) => [message.id, message]))
        page.items.forEach((message) => messages.set(message.id, message))
        return {
          ...item,
          chat: [...messages.values()].sort((a, b) => a.id - b.id).slice(-MAX_CHAT_MESSAGES),
        }
      })
      chatBefore.current[id] = page.nextBefore
    } catch (error) {
      toast.error((error as Error).message)
    } finally {
      loadingChat.current.delete(id)
    }
  }, [])

  useEffect(() => {
    if (selected) void loadChat(selected)
  }, [selected, loadChat])

  async function create(event: FormEvent) {
    event.preventDefault()
    try {
      const snapshot = await api.create(form)
      sync.current?.acceptSnapshot(snapshot)
      setSelected(snapshot.server.id)
      setAdding(false)
      setForm(defaultServer)
      toast.success(t('app.created'))
    } catch (error) {
      toast.error((error as Error).message)
    }
  }

  return (
    <div className="from-background via-background to-muted/30 text-foreground min-h-screen bg-linear-to-br">
      <Toaster richColors />
      <header className="bg-background/80 sticky top-0 z-30 border-b px-3 py-3 backdrop-blur-xl sm:px-6 sm:py-4">
        <div className="mx-auto flex max-w-375 flex-wrap items-center gap-3">
          <Radio className="text-primary" />
          <div>
            <h1 className="font-semibold tracking-wide">SA-MP-Pilot</h1>
            <p className="text-muted-foreground text-xs">{t('app.subtitle')}</p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <Button variant="outline" className="px-2 sm:px-4" onClick={() => setSettingsOpen(true)}>
              <Settings2 size={16} />
              <span className="hidden sm:inline">{t('common.settings')}</span>
            </Button>
            <Button variant="outline" className="px-2 sm:px-4" onClick={openPlugins}>
              <Plug size={16} />
              <span className="hidden sm:inline">{t('plugins.open')}</span>
            </Button>
            <Button variant="outline" className="px-2 sm:px-4" onClick={openDebug}>
              <Code2 size={16} />
              <span className="hidden sm:inline">{t('plugins.debugOpen')}</span>
            </Button>
            <Globe2 className="text-muted-foreground" size={16} />
            <Select
              value={i18n.resolvedLanguage ?? 'en'}
              onValueChange={(language) => void changeLanguage(language as SupportedLanguage)}
            >
              <SelectTrigger className="w-24 sm:w-36" aria-label={t('language.label')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {supportedLanguages.map((language) => (
                  <SelectItem key={language} value={language}>
                    {t(`language.${language}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button className="px-2 sm:px-4" onClick={() => setAdding(true)}>
              <Plus size={16} />
              {t('app.newInstance')}
            </Button>
          </div>
        </div>
      </header>
      <main className="mx-auto flex max-w-375 flex-col gap-5 p-3 sm:gap-8 sm:p-6 lg:flex-row lg:items-start lg:gap-10">
        <div className="lg:hidden">
          {items.length ? (
            <Select value={selected} onValueChange={setSelected}>
              <SelectTrigger className="w-full" aria-label={t('app.selectInstance')}>
                <SelectValue placeholder={t('app.selectInstance')} />
              </SelectTrigger>
              <SelectContent>
                {items.map((snapshot) => (
                  <SelectItem key={snapshot.server.id} value={snapshot.server.id}>
                    {snapshot.connection.serverName || `${snapshot.server.host}:${snapshot.server.port}`}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <Card className="text-muted-foreground p-4 text-center text-sm">{t('app.empty')}</Card>
          )}
        </div>
        <aside className="instance-list-scrollbar hidden w-full shrink-0 [scrollbar-gutter:stable] space-y-4 lg:sticky lg:top-24 lg:block lg:max-h-[calc(100dvh-7.5rem)] lg:w-75 lg:overflow-y-auto lg:overscroll-contain lg:py-2 lg:pr-2">
          {items.map((snapshot) => (
            <Card
              key={snapshot.server.id}
              onClick={() => setSelected(snapshot.server.id)}
              className={`hover:border-primary/40 cursor-pointer transition-all hover:-translate-y-0.5 hover:shadow-md ${selected === snapshot.server.id ? 'border-primary bg-primary/5 ring-primary/20 ring-1' : ''}`}
            >
              <CardHeader className="flex-row items-center gap-3 space-y-0 p-4">
                <span
                  className={`mr-2 h-2 w-2 rounded-full ${snapshot.connection.status === STATUS_CONNECTED ? 'bg-emerald-500' : snapshot.connection.status === STATUS_ERROR ? 'bg-red-500' : 'bg-muted-foreground'}`}
                />
                <div className="min-w-0">
                  <div className="truncate font-medium">
                    {snapshot.connection.serverName || `${snapshot.server.host}:${snapshot.server.port}`}
                  </div>
                  <CardDescription>{snapshot.server.nickname}</CardDescription>
                </div>
                <ChevronRight className="text-muted-foreground ml-auto" size={16} />
              </CardHeader>
            </Card>
          ))}
          {!items.length && (
            <Card className="text-muted-foreground p-8 text-center text-sm">{t('app.empty')}</Card>
          )}
        </aside>
        <section className="min-w-0 flex-1">
          {current ? (
            <Workspace
              key={current.server.id}
              value={current}
              onLoadOlderChat={() => void loadChat(current.server.id, true)}
              onDelete={async () => {
                await api.remove(current.server.id)
                setSelected('')
              }}
            />
          ) : (
            <Card className="text-muted-foreground grid min-h-[65vh] place-content-center text-center">
              <Telescope className="mx-auto mb-3" />
              <p>{t('app.selectInstance')}</p>
            </Card>
          )}
        </section>
      </main>
      {!adding && current?.activeDialog && (
        <Suspense fallback={null}>
          <LazyServerDialog key={`${current.server.id}:${current.activeDialog.id}`} value={current} />
        </Suspense>
      )}
      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('server.add')}</DialogTitle>
          </DialogHeader>
          <ServerForm
            value={form}
            submitLabel={t('common.create')}
            onChange={setForm}
            onCancel={() => setAdding(false)}
            onSubmit={create}
          />
        </DialogContent>
      </Dialog>
      <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} />
      {debugLoaded && (
        <Suspense fallback={null}>
          <LazyPluginConsole
            open={debugOpen}
            onOpenChange={setDebugOpen}
            initialInstanceId={selected}
            instances={items}
            streamEvents={pluginEvents}
          />
        </Suspense>
      )}
      {pluginsLoaded && (
        <Suspense fallback={null}>
          <LazyPluginManager open={pluginsOpen} onOpenChange={setPluginsOpen} streamEvents={pluginEvents} />
        </Suspense>
      )}
    </div>
  )
}
