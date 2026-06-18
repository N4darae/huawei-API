export interface MeterProps {
  label: string
  value: number
  max: number
  format: (n: number) => string
  dangerAt?: number
  warnAt?: number
}

export function Meter({ label, value, max, format, dangerAt = 0.9, warnAt = 0.75 }: MeterProps) {
  const ratio = max > 0 ? Math.min(value / max, 1) : 0
  const pct = Math.round(ratio * 100)
  const tone = max > 0 && ratio >= dangerAt ? 'danger' : max > 0 && ratio >= warnAt ? 'warn' : 'ok'
  const text = max > 0 ? `${format(value)} / ${format(max)} · ${pct}%` : format(value)
  return (
    <div className="meter">
      <div
        className="meter-track"
        role="meter"
        aria-label={label}
        aria-valuenow={pct}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuetext={text}
      >
        <div className="meter-fill" data-tone={tone} style={{ width: pct + '%' }} />
      </div>
      <span className="meter-label" data-tone={tone}>
        {text}
        {tone === 'danger' && max > 0 ? ' over quota' : ''}
      </span>
    </div>
  )
}
