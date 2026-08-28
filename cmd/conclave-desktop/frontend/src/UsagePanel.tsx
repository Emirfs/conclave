import { useCallback, useEffect, useState } from 'react'

import { Usage } from '../wailsjs/go/main/App'
import { domain } from '../wailsjs/go/models'
import { providerStyle } from './providers'

const WINDOWS = [1, 7, 30]

/** How often the panel refreshes while it is open. Usage is written as turns
 *  finish, so this only has to be fast enough to feel live, not instant. */
const POLL = 5000

/** What the board spent, per provider, next to what each provider says is left.
 *  The two answer different questions — one is what was done, the other how
 *  full a window is — and only together do they say whether to keep going. */
export function UsagePanel({ online, onClose }: { online: boolean; onClose: () => void }) {
  const [days, setDays] = useState(7)
  const [report, setReport] = useState<domain.UsageReport | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    if (!online) return
    try {
      setReport(await Usage(days))
      setError(null)
    } catch (cause) {
      setError(String(cause))
    }
  }, [days, online])

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), POLL)
    return () => window.clearInterval(timer)
  }, [load])

  const providers = report?.providers ?? []
  const total = providers.reduce(
    (sum, item) => sum + item.input_tokens + item.output_tokens,
    0,
  )

  return (
    <div className="usage">
      <div className="usage__head">
        <h2 className="usage__title">Kullanım</h2>
        <span className="usage__windows">
          {WINDOWS.map((value) => (
            <button
              key={value}
              className={`usage__window${days === value ? ' usage__window--active' : ''}`}
              onClick={() => setDays(value)}
            >
              {value === 1 ? 'bugün' : `${value} gün`}
            </button>
          ))}
        </span>
        <button className="usage__close" onClick={onClose} title="Paneli kapat">
          ✕
        </button>
      </div>

      {error && <p className="usage__error">{error}</p>}
      {!error && providers.length === 0 && (
        <p className="usage__empty">Bu aralıkta kayıtlı tur yok.</p>
      )}

      {providers.map((item) => {
        const style = providerStyle(item.provider)
        const spent = item.input_tokens + item.output_tokens
        return (
          <div
            key={item.provider}
            className="usage__row"
            style={{ ['--usage-accent' as string]: style.accent }}
          >
            <div className="usage__row-head">
              <span className="usage__provider">{style.label}</span>
              <span className="usage__tokens">{formatTokens(spent)}</span>
            </div>
            {/* Each provider's share of the window, so one bar comparison says
                where the work actually went. */}
            <div className="usage__bar">
              <span
                className="usage__bar-fill"
                style={{ width: total > 0 ? `${(spent / total) * 100}%` : '0%' }}
              />
            </div>
            <div className="usage__detail">
              {item.turns} tur · {item.cards} kart · giriş {formatTokens(item.input_tokens)} ·
              {' '}çıkış {formatTokens(item.output_tokens)}
            </div>
            {item.quota && (
              <div className="usage__quota">
                <QuotaLine
                  label={item.quota.short_label}
                  utilization={item.quota.short_utilization}
                />
                <QuotaLine
                  label={item.quota.long_label}
                  utilization={item.quota.long_utilization}
                />
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

/** A provider's own allowance report. Absent for a provider that does not
 *  offer one, which is not the same as an empty window. */
function QuotaLine({ label, utilization }: { label?: string; utilization?: number }) {
  if (!label) return null
  const percent = Math.min(100, Math.max(0, Math.round((utilization ?? 0) * 100)))
  return (
    <div className="usage__quota-line">
      <span className="usage__quota-label">{label}</span>
      <span className="usage__quota-bar">
        <span className="usage__quota-fill" style={{ width: `${percent}%` }} />
      </span>
      <span className="usage__quota-value">%{percent}</span>
    </div>
  )
}

/** Token counts are read at a glance or not at all, so they are rounded rather
 *  than reported to the digit. */
function formatTokens(value: number): string {
  if (value === 0) return '—'
  if (value < 1000) return String(value)
  if (value < 1_000_000) return `${Math.round(value / 100) / 10}k`
  return `${Math.round(value / 100_000) / 10}M`
}
