import type { ReactNode } from 'react'

export interface EntityGridRow {
  id: number | string
  title: ReactNode
  subtitle: ReactNode
  actions?: ReactNode
}

export function EntityGrid({ rows, empty }: { rows: EntityGridRow[]; empty: string }) {
  if (!rows.length) {
    return <div className="text-muted-foreground grid h-52 place-content-center text-sm">{empty}</div>
  }

  return (
    <div className="space-y-2">
      {rows.map((row) => (
        <div
          key={row.id}
          className="bg-card flex flex-wrap items-center gap-3 rounded-lg border p-3 sm:flex-nowrap"
        >
          <div className="min-w-0 flex-1">
            <div>{row.title}</div>
            <div className="text-muted-foreground mt-1 text-xs">{row.subtitle}</div>
          </div>
          {row.actions && <div className="flex flex-wrap gap-2">{row.actions}</div>}
        </div>
      ))}
    </div>
  )
}
