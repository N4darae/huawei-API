import type { ReactNode } from 'react'

export interface Column<T> {
  key: string
  header: string
  width?: string
  align?: 'left' | 'right'
  cell: (row: T) => ReactNode
}

export interface TableProps<T> {
  caption: string
  columns: ReadonlyArray<Column<T>>
  rows: readonly T[]
  rowKey: (row: T) => string
  selectedKey?: string | null
  onRowActivate?: (row: T) => void
  empty: ReactNode
}

export function Table<T>({
  caption,
  columns,
  rows,
  rowKey,
  selectedKey,
  onRowActivate,
  empty,
}: TableProps<T>) {
  if (rows.length === 0) {
    return <div className="tbl-scroll tbl-empty">{empty}</div>
  }
  return (
    <div className="tbl-scroll">
      <table className="tbl">
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.key} scope="col" style={c.width ? { width: c.width } : undefined}>
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const k = rowKey(row)
            return (
              <tr
                key={k}
                data-selected={selectedKey === k}
                onDoubleClick={onRowActivate ? () => onRowActivate(row) : undefined}
              >
                {columns.map((c) => (
                  <td key={c.key} style={c.align === 'right' ? { textAlign: 'right' } : undefined}>
                    {c.cell(row)}
                  </td>
                ))}
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
