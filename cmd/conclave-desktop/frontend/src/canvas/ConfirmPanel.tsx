import { useEffect, useRef } from 'react'

/** What a pending confirmation carries: what to say, and what to do if the
 *  answer is yes. */
export type PendingConfirm = {
  message: string
  /** Wording of the accepting button. Naming the act beats a bare "Tamam". */
  action: string
  run: () => void
}

/** An in-app replacement for window.confirm. The window is frameless, so a
 *  browser modal is not guaranteed to appear at all; and a native confirm
 *  blocks the event loop, which would freeze the board's polling for as long
 *  as it stays open. This does neither. */
export function ConfirmPanel({
  pending,
  onCancel,
}: {
  pending: PendingConfirm
  onCancel: () => void
}) {
  const accept = useRef<HTMLButtonElement>(null)
  // Focus the accepting button so Enter confirms and Tab reaches Vazgeç, the
  // two things anyone tries without being told.
  useEffect(() => accept.current?.focus(), [pending])

  return (
    <div className="confirm nodrag" role="alertdialog" aria-live="assertive">
      <span className="confirm__text">{pending.message}</span>
      <button
        ref={accept}
        className="confirm__accept"
        onClick={() => {
          pending.run()
          onCancel()
        }}
      >
        {pending.action}
      </button>
      <button className="confirm__cancel" onClick={onCancel}>
        Vazgeç
      </button>
    </div>
  )
}
