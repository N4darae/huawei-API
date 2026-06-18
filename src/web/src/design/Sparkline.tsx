export interface SparklineProps {
  values: readonly number[]
  label: string
  width?: number
  height?: number
}

export function Sparkline({ values, label, width = 72, height = 18 }: SparklineProps) {
  if (values.length < 2) {
    return (
      <span className="faint mono" aria-label={label}>
        no data
      </span>
    )
  }
  let lo = values[0] as number
  let hi = values[0] as number
  for (const v of values) {
    if (v < lo) lo = v
    if (v > hi) hi = v
  }
  const span = hi - lo || 1
  const stepX = width / (values.length - 1)
  const points = values
    .map((v, i) => {
      const x = i * stepX
      const y = height - ((v - lo) / span) * (height - 2) - 1
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
  return (
    <svg
      className="spark"
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label={`${label}: ${values.join(', ')}`}
    >
      <polyline
        points={points}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  )
}
