import type { FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import type { Server } from '@/types'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

export type ServerFormValue = Omit<Server, 'id'>

export function ServerForm({
  value,
  submitLabel,
  showAutoConnect = false,
  onChange,
  onCancel,
  onSubmit,
}: {
  value: ServerFormValue
  submitLabel: string
  showAutoConnect?: boolean
  onChange: (value: ServerFormValue) => void
  onCancel: () => void
  onSubmit: (event: FormEvent) => void
}) {
  const { t } = useTranslation()
  return (
    <form onSubmit={onSubmit} className="space-y-3">
      <Label className="block space-y-2">
        {t('server.host')}
        <Input
          required
          value={value.host}
          onChange={(event) => onChange({ ...value, host: event.target.value })}
        />
      </Label>
      <div className="grid gap-3 sm:grid-cols-2">
        <Label className="block space-y-2">
          {t('server.port')}
          <Input
            inputMode="numeric"
            required
            value={value.port}
            onChange={(event) => onChange({ ...value, port: Number(event.target.value) })}
          />
        </Label>
        <Label className="block space-y-2">
          {t('server.nickname')}
          <Input
            required
            value={value.nickname}
            onChange={(event) => onChange({ ...value, nickname: event.target.value })}
          />
        </Label>
      </div>
      <Label className="block space-y-2">
        {t('server.password')}
        <Input
          type="password"
          value={value.password}
          onChange={(event) => onChange({ ...value, password: event.target.value })}
        />
      </Label>
      <Label className="block space-y-2">
        {t('server.encoding')}
        <Select
          value={value.encoding}
          onValueChange={(encoding) => onChange({ ...value, encoding: encoding as Server['encoding'] })}
        >
          <SelectTrigger className="mt-1 h-9 w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="utf-8">UTF-8</SelectItem>
            <SelectItem value="gbk">GBK</SelectItem>
            <SelectItem value="windows-1251">Windows-1251</SelectItem>
          </SelectContent>
        </Select>
      </Label>
      {showAutoConnect && (
        <Label className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={value.autoConnect}
            onCheckedChange={(checked) => onChange({ ...value, autoConnect: checked === true })}
          />
          {t('server.autoConnect')}
        </Label>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t('common.cancel')}
        </Button>
        <Button type="submit">{submitLabel}</Button>
      </div>
    </form>
  )
}
