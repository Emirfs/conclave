import { useCallback, useEffect, useState } from 'react'

import { ApplyUpdate, OpenReleasePage, UpdateStatus } from '../wailsjs/go/main/App'
import { update } from '../wailsjs/go/models'

/** The daemon checks GitHub once a day; reading its cached answer more often
 *  than that costs nothing, but there is no reason to. */
const POLL_INTERVAL = 60_000

/** A dismissal is remembered per version, so declining 0.3.0 does not also
 *  hide 0.4.0 when it arrives. */
const DISMISSED_KEY = 'conclave.update.dismissed'

export function UpdateBanner({ online }: { online: boolean }) {
  const [status, setStatus] = useState<update.Status | null>(null)
  const [dismissed, setDismissed] = useState(() => readDismissed())
  const [applying, setApplying] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  useEffect(() => {
    if (!online) return
    const poll = async () => {
      try {
        setStatus(await UpdateStatus())
      } catch {
        // An unreachable daemon is already reported by the title bar; a second
        // complaint about it here would say nothing new.
      }
    }
    void poll()
    const timer = window.setInterval(() => void poll(), POLL_INTERVAL)
    return () => window.clearInterval(timer)
  }, [online])

  const dismiss = useCallback(() => {
    if (status?.latest) writeDismissed(status.latest)
    setDismissed(status?.latest ?? null)
  }, [status])

  const apply = useCallback(async () => {
    setApplying(true)
    setFailure(null)
    try {
      // On success the window closes on its own: the installer cannot replace
      // the files of a running app, so it waits for this process to exit.
      await ApplyUpdate()
    } catch (cause) {
      setApplying(false)
      setFailure(String(cause))
    }
  }, [])

  if (!status?.available || !status.latest) return null
  if (dismissed === status.latest) return null

  return (
    <div className="update" role="status">
      <span className="update__dot" />
      <span className="update__text">
        <strong>Conclave {status.latest} çıktı</strong>
        <span className="update__current">şu an {status.current}</span>
      </span>
      {failure && <span className="update__error">{failure}</span>}
      {status.url && (
        <button
          className="update__link"
          onClick={() => void OpenReleasePage(status.url as string)}
          disabled={applying}
        >
          Notları oku
        </button>
      )}
      <button className="update__apply" onClick={() => void apply()} disabled={applying}>
        {applying ? 'Güncelleniyor…' : 'Güncelle'}
      </button>
      <button className="update__close" onClick={dismiss} disabled={applying} title="Şimdilik gizle">
        <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
          <path d="M4 4l8 8M12 4l-8 8" fill="none" stroke="currentColor" strokeWidth="1.2" />
        </svg>
      </button>
    </div>
  )
}

function readDismissed(): string | null {
  try {
    return window.localStorage.getItem(DISMISSED_KEY)
  } catch {
    return null
  }
}

function writeDismissed(version: string) {
  try {
    window.localStorage.setItem(DISMISSED_KEY, version)
  } catch {
    // A browser that refuses storage just means the banner comes back; that is
    // a smaller problem than a crash.
  }
}
