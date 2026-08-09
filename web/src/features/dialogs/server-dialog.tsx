import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api'
import {
  ACTION_DEFER_DIALOG,
  ACTION_DIALOG,
  DIALOG_STYLE_INPUT,
  DIALOG_STYLE_LIST,
  DIALOG_STYLE_PASSWORD,
  DIALOG_STYLE_TABLIST,
  DIALOG_STYLE_TABLIST_HEADERS,
} from '@/constants'
import type { Snapshot } from '@/types'
import { stripSAMPColors } from '@/lib/utils'
import { SAMPColoredText } from '@/components/samp-colored-text'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'

export function ServerDialog({ value }: { value: Snapshot }) {
  const { t } = useTranslation()
  const dialog = value.activeDialog
  const [input, setInput] = useState('')
  const [item, setItem] = useState(-1)
  const rows = useMemo(
    () =>
      (dialog?.message ?? '')
        .split('\n')
        .map((row) => row.replace(/\r$/, ''))
        .filter((row) => row.trim()),
    [dialog?.message],
  )
  const isList = dialog
    ? [DIALOG_STYLE_LIST, DIALOG_STYLE_TABLIST, DIALOG_STYLE_TABLIST_HEADERS].includes(dialog.style)
    : false
  const isTable = dialog ? [DIALOG_STYLE_TABLIST, DIALOG_STYLE_TABLIST_HEADERS].includes(dialog.style) : false
  const hasHeader = dialog?.style === DIALOG_STYLE_TABLIST_HEADERS && rows.length > 0
  const selectableRows = hasHeader ? rows.slice(1) : rows
  const columnCount = Math.max(1, ...rows.map((row) => row.split('\t').length))
  const gridStyle = isTable
    ? { gridTemplateColumns: `repeat(${columnCount}, minmax(max-content, 1fr))` }
    : undefined

  useEffect(() => {
    setInput('')
    setItem(isList && selectableRows.length ? 0 : -1)
  }, [dialog?.id, dialog?.receivedAt, isList, selectableRows.length])

  if (!dialog) return null
  const act = (action: string, data: unknown = {}) =>
    api.action(value.server.id, action, data).catch((error) => toast.error(error.message))
  const selectedRow = item >= 0 ? (selectableRows[item] ?? '') : ''
  const selectedInput = stripSAMPColors(isTable ? selectedRow.split('\t')[0] : selectedRow)
  const respond = (buttonId: number) =>
    act(ACTION_DIALOG, {
      dialogId: dialog.id,
      buttonId,
      listItem: item,
      inputText:
        dialog.style === DIALOG_STYLE_INPUT || dialog.style === DIALOG_STYLE_PASSWORD ? input : selectedInput,
    })

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) void act(ACTION_DEFER_DIALOG)
      }}
    >
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            <SAMPColoredText text={dialog.title} />
          </DialogTitle>
        </DialogHeader>
        {isList ? (
          <ScrollArea className="bg-background/60 max-h-80 rounded-md border p-2">
            <div className="space-y-1">
              {hasHeader && (
                <div
                  className="text-muted-foreground mb-1 grid min-w-max gap-x-6 border-b px-4 py-2 text-sm font-medium"
                  style={gridStyle}
                >
                  {rows[0].split('\t').map((cell, column) => (
                    <span key={column} className="whitespace-pre-wrap">
                      <SAMPColoredText text={cell} />
                    </span>
                  ))}
                </div>
              )}
              {selectableRows.map((row, index) => (
                <Button
                  key={`${index}-${row}`}
                  variant={item === index ? 'secondary' : 'ghost'}
                  className={
                    isTable
                      ? 'grid h-auto w-full min-w-max justify-stretch gap-x-6 py-2 text-left'
                      : 'h-auto w-full justify-start py-2 text-left whitespace-pre-wrap'
                  }
                  style={gridStyle}
                  onClick={() => setItem(index)}
                >
                  {isTable ? (
                    row.split('\t').map((cell, column) => (
                      <span key={column} className="whitespace-pre-wrap">
                        <SAMPColoredText text={cell} />
                      </span>
                    ))
                  ) : (
                    <SAMPColoredText text={row} />
                  )}
                </Button>
              ))}
            </div>
          </ScrollArea>
        ) : (
          <p className="text-muted-foreground max-h-80 overflow-auto text-sm whitespace-pre-wrap">
            <SAMPColoredText text={dialog.message} />
          </p>
        )}
        {(dialog.style === DIALOG_STYLE_INPUT || dialog.style === DIALOG_STYLE_PASSWORD) && (
          <Input
            type={dialog.style === DIALOG_STYLE_PASSWORD ? 'password' : 'text'}
            value={input}
            onChange={(event) => setInput(event.target.value)}
            placeholder={t('dialogs.inputPlaceholder')}
            autoFocus
          />
        )}
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="ghost" onClick={() => act(ACTION_DEFER_DIALOG)}>
            {t('dialogs.defer')}
          </Button>
          {dialog.button2 && (
            <Button variant="outline" onClick={() => respond(0)}>
              {dialog.button2}
            </Button>
          )}
          <Button onClick={() => respond(1)}>{dialog.button1 || t('common.confirm')}</Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
