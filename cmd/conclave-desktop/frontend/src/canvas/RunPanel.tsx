import { useCallback, useEffect, useState } from 'react'

import { domain } from '../../wailsjs/go/models'

type Props = {
  runs: domain.FlowRun[]
  onStop: (runID: number) => Promise<void>
  onDetail: (runID: number) => Promise<domain.FlowRunDetail | null>
  onReport: (runID: number) => Promise<void>
  onJump: (conversationID: number) => void
  onClose: () => void
}

/** How long a run has been going, or how long it took. */
function elapsed(run: domain.FlowRun): string {
  const start = new Date(run.started_at).getTime()
  const end = run.finished_at ? new Date(run.finished_at).getTime() : Date.now()
  if (Number.isNaN(start) || Number.isNaN(end)) return ''
  const seconds = Math.max(0, Math.round((end - start) / 1000))
  if (seconds < 60) return `${seconds} sn`
  if (seconds < 3600) return `${Math.round(seconds / 60)} dk`
  return `${Math.round(seconds / 3600)} sa`
}

function started(run: domain.FlowRun): string {
  const at = new Date(run.started_at)
  if (Number.isNaN(at.getTime())) return ''
  return at.toLocaleString([], {
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** The runs the board has been through, the ones still going first. A routine
 *  fires while nobody is watching, so what happened has to be readable
 *  afterwards and not only visible while it happens. */
export function RunPanel({ runs, onStop, onDetail, onReport, onJump, onClose }: Props) {
  const [open, setOpen] = useState<number | null>(null)
  const [detail, setDetail] = useState<domain.FlowRunDetail | null>(null)
  const [loading, setLoading] = useState(false)

  const show = useCallback(
    async (runID: number) => {
      if (open === runID) {
        setOpen(null)
        setDetail(null)
        return
      }
      setOpen(runID)
      setDetail(null)
      setLoading(true)
      setDetail(await onDetail(runID))
      setLoading(false)
    },
    [onDetail, open],
  )

  // A run that is still going keeps producing steps, so the open one refreshes
  // rather than freezing at whatever it held when it was opened.
  useEffect(() => {
    if (open === null) return
    const current = runs.find((run) => run.id === open)
    if (!current || current.status !== 'running') return
    const timer = window.setInterval(() => {
      void onDetail(open).then((fresh) => fresh && setDetail(fresh))
    }, 1500)
    return () => window.clearInterval(timer)
  }, [open, onDetail, runs])

  return (
    <div className="runs nodrag nowheel">
      <header className="runs__head">
        <span className="runs__title">Akışlar</span>
        <button className="runs__close" onClick={onClose} title="Paneli kapat" aria-label="Paneli kapat">
          ✕
        </button>
      </header>

      {runs.length === 0 && <p className="runs__empty">Henüz akış yok.</p>}

      <ul className="runs__list">
        {runs.map((run) => {
          const running = run.status === 'running'
          return (
            <li key={run.id} className={`runs__row${running ? ' runs__row--running' : ''}`}>
              <button className="runs__summary" onClick={() => void show(run.id)}>
                <span className={`runs__dot${running ? ' runs__dot--live' : ''}`} aria-hidden="true" />
                <span className="runs__name">
                  {run.origin_label || `Akış #${run.id}`}
                  {run.origin_kind === 'trigger' && <span className="runs__badge">tetikleyici</span>}
                </span>
                <span className="runs__meta">
                  {started(run)} · {run.cards ?? 0} kart · {run.steps} adım · {elapsed(run)}
                </span>
              </button>
              <span className="runs__actions">
                <button
                  className="runs__action"
                  onClick={() => void onReport(run.id)}
                  title="Bu akışı panoya not olarak yaz"
                >
                  rapor
                </button>
                {running && (
                  <button
                    className="runs__action runs__action--stop"
                    onClick={() => void onStop(run.id)}
                    title="Bu akıştaki her kartı durdur"
                  >
                    durdur
                  </button>
                )}
              </span>

              {open === run.id && (
                <div className="runs__detail">
                  {loading && <span className="runs__loading">okunuyor…</span>}
                  {!loading && detail && detail.steps.length === 0 && (
                    <span className="runs__loading">Bu akışta hiçbir kart konuşmadı.</span>
                  )}
                  {!loading &&
                    detail?.steps.map((step) => (
                      <div className="runs__step" key={step.turn_id}>
                        <button
                          className="runs__step-card"
                          onClick={() => onJump(step.conversation_id)}
                          title="Karta git"
                        >
                          {step.card}
                        </button>
                        <span className={`runs__step-status runs__step-status--${step.status}`}>
                          {step.status}
                        </span>
                        <p className="runs__step-text">{step.answer || 'Cevap yok.'}</p>
                      </div>
                    ))}
                </div>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
