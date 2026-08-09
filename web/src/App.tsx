import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { ChevronRight, Globe2, Plus, Radio, Telescope } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Toaster, toast } from 'sonner'
import '@/i18n'
import { api, events, upsertSnapshot } from '@/api'
import {
  DEFAULT_HOST,
  DEFAULT_NICKNAME,
  DEFAULT_PORT,
  EVENT_INSTANCE_DELETED,
  EVENT_CHAT_MESSAGE,
  EVENT_CHAT_RESET,
  MAX_CHAT_MESSAGES,
  STATUS_CONNECTED,
  STATUS_ERROR,
} from '@/constants'
import { changeLanguage, supportedLanguages } from '@/i18n'
import type { SupportedLanguage } from '@/i18n'
import type { ChatMessage, Server, Snapshot } from '@/types'
import { Button } from '@/components/ui/button'
import { Card, CardDescription, CardHeader } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ServerDialog } from '@/features/dialogs/server-dialog'
import { ServerForm } from '@/features/instances/server-form'
import { Workspace } from '@/features/workspace/workspace'

const defaultServer: Omit<Server, 'id'> = {
  host: DEFAULT_HOST,
  port: DEFAULT_PORT,
  nickname: DEFAULT_NICKNAME,
  password: '',
  encoding: 'utf-8',
  autoConnect: false,
}

export { ServerDialog } from '@/features/dialogs/server-dialog'

export default function App() {
  const { t, i18n } = useTranslation()
  const [items, setItems] = useState<Snapshot[]>([])
  const [selected, setSelected] = useState('')
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState(defaultServer)
  const chatBefore = useRef<Record<string, number | undefined>>({})
  const loadingChat = useRef(new Set<string>())
  const current = useMemo(() => items.find((item) => item.server.id === selected), [items, selected])

  useEffect(() => {
    api
      .list()
      .then((snapshots) => {
        setItems(snapshots)
        setSelected((value) => value || snapshots[0]?.server.id || '')
      })
      .catch((error) => toast.error(error.message))
    return events((event) => {
      if (event.type === EVENT_INSTANCE_DELETED) {
        setItems((value) => value.filter((item) => item.server.id !== event.instanceId))
      } else if (event.type === EVENT_CHAT_RESET) {
        chatBefore.current[event.instanceId] = undefined
        setItems((value) =>
          value.map((item) => (item.server.id === event.instanceId ? { ...item, chat: [] } : item)),
        )
      } else if (event.type === EVENT_CHAT_MESSAGE && event.data) {
        const message = event.data as ChatMessage
        setItems((value) =>
          value.map((item) =>
            item.server.id === event.instanceId && !item.chat.some((entry) => entry.id === message.id)
              ? { ...item, chat: [...item.chat, message].slice(-MAX_CHAT_MESSAGES) }
              : item,
          ),
        )
      } else if (event.data) {
        setItems((value) => upsertSnapshot(value, event.data as Snapshot))
      }
    })
  }, [])

  const loadChat = useCallback(async (id: string, older = false) => {
    if (loadingChat.current.has(id)) return
    const before = older ? chatBefore.current[id] : undefined
    if (older && !before) return
    loadingChat.current.add(id)
    try {
      const page = await api.chat(id, before)
      setItems((value) =>
        value.map((item) => {
          if (item.server.id !== id) return item
          const messages = new Map(item.chat.map((message) => [message.id, message]))
          page.items.forEach((message) => messages.set(message.id, message))
          return {
            ...item,
            chat: [...messages.values()].sort((a, b) => a.id - b.id).slice(-MAX_CHAT_MESSAGES),
          }
        }),
      )
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
      setItems((value) => upsertSnapshot(value, snapshot))
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
      <header className="bg-background/80 sticky top-0 z-30 border-b px-6 py-4 backdrop-blur-xl">
        <div className="mx-auto flex max-w-375 items-center gap-3">
          <Radio className="text-primary" />
          <div>
            <h1 className="font-semibold tracking-wide">SA-MP-Pilot</h1>
            <p className="text-muted-foreground text-xs">{t('app.subtitle')}</p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <Globe2 className="text-muted-foreground" size={16} />
            <Select
              value={i18n.resolvedLanguage ?? 'en'}
              onValueChange={(language) => void changeLanguage(language as SupportedLanguage)}
            >
              <SelectTrigger className="w-36" aria-label={t('language.label')}>
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
            <Button onClick={() => setAdding(true)}>
              <Plus size={16} />
              {t('app.newInstance')}
            </Button>
          </div>
        </div>
      </header>
      <main className="mx-auto flex max-w-375 flex-col gap-8 p-6 lg:flex-row lg:items-start lg:gap-10">
        <aside className="w-full shrink-0 space-y-4 lg:w-75">
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
        <ServerDialog key={`${current.server.id}:${current.activeDialog.id}`} value={current} />
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
    </div>
  )
}
