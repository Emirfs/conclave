import { useCallback, useEffect, useState } from 'react'

import { FileDiff, ProjectChanges } from '../../wailsjs/go/main/App'
import { vcs } from '../../wailsjs/go/models'

/** Shows what changed in the card's project and the diff of a chosen file. */
export function Changes({ conversationID, refreshKey }: { conversationID: number; refreshKey: number }) {
  const [status, setStatus] = useState<vcs.Status | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [diff, setDiff] = useState<vcs.Diff | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setStatus(await ProjectChanges(conversationID))
      setError(null)
    } catch (cause) {
      setError(String(cause))
    }
  }, [conversationID])

  // refreshKey changes when the card finishes a turn, so edits a provider just
  // made show up without the user asking.
  useEffect(() => {
    void load()
  }, [load, refreshKey])

  useEffect(() => {
    if (!selected) {
      setDiff(null)
      return
    }
    let cancelled = false
    FileDiff(conversationID, selected)
      .then((result) => {
        if (!cancelled) setDiff(result)
      })
      .catch((cause) => {
        if (!cancelled) setError(String(cause))
      })
    return () => {
      cancelled = true
    }
  }, [conversationID, selected, refreshKey])

  if (error) return <p className="changes__empty">{error}</p>
  if (!status) return <p className="changes__empty">Yükleniyor…</p>
  if (!status.available) {
    return <p className="changes__empty">Bu dizin bir git deposu değil.</p>
  }
  const changes = status.changes ?? []
  if (changes.length === 0) {
    return <p className="changes__empty">Değişiklik yok{status.branch ? ` · ${status.branch}` : ''}</p>
  }

  return (
    <div className="changes">
      <ul className="changes__list">
        {changes.map((change) => (
          <li key={change.path}>
            <button
              className={`changes__file${selected === change.path ? ' changes__file--active' : ''}`}
              onClick={() => setSelected(selected === change.path ? null : change.path)}
              title={change.path}
            >
              <span className={`changes__code changes__code--${codeClass(change)}`}>
                {change.status.trim() || '??'}
              </span>
              <span className="changes__path">{change.path}</span>
            </button>
          </li>
        ))}
      </ul>
      {diff && <Patch patch={diff.patch} truncated={diff.truncated} />}
    </div>
  )
}

function codeClass(change: vcs.Change): string {
  if (change.untracked) return 'new'
  if (change.staged) return 'staged'
  return 'dirty'
}

/** Renders a unified diff. Line prefixes carry all the meaning, so colouring
 *  them is enough to read a patch. */
function Patch({ patch, truncated }: { patch: string; truncated: boolean }) {
  const lines = patch.split('\n')
  return (
    <pre className="patch nowheel">
      {lines.map((line, index) => (
        <span key={index} className={`patch__line patch__line--${lineClass(line)}`}>
          {line || ' '}
        </span>
      ))}
      {truncated && <span className="patch__line patch__line--meta">[kısaltıldı]</span>}
    </pre>
  )
}

function lineClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'meta'
  if (line.startsWith('@@')) return 'hunk'
  if (line.startsWith('diff ') || line.startsWith('index ')) return 'meta'
  if (line.startsWith('+')) return 'add'
  if (line.startsWith('-')) return 'remove'
  return 'context'
}
