import { useCallback, useEffect, useState } from 'react'

import { FileDiff, ProjectChanges } from '../../wailsjs/go/main/App'
import { vcs } from '../../wailsjs/go/models'

/** Shows what changed in the card's project and the diff of a chosen file. */
export function Changes({
  conversationID,
  refreshKey,
  onPin,
}: {
  conversationID: number
  refreshKey: number
  onPin: (title: string, body: string) => void
}) {
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
      <div className="changes__bar">
        <span className="changes__summary">
          {changes.length} değişiklik{status.branch ? ` · ${status.branch}` : ''}
        </span>
        {/* A diff read next to the conversation that produced it beats a diff
            read inside a tab you have to keep switching back to. */}
        <button
          className="changes__pin"
          onClick={() =>
            onPin(
              selected ? `Değişiklik · ${selected}` : 'Değişiklikler',
              selected && diff ? diffBody(selected, diff) : statusBody(status),
            )
          }
          title="Panoya kart olarak çıkar"
        >
          ↗ karta çıkar
        </button>
      </div>
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

/** One file's patch, as markdown a note card can render. */
function diffBody(path: string, diff: vcs.Diff): string {
  const patch = diff.truncated ? `${diff.patch}\n[kısaltıldı]` : diff.patch
  return `## ${path}\n\n\`\`\`diff\n${patch}\n\`\`\``
}

/** The whole working tree as a checklist, when no single file is selected. */
function statusBody(status: vcs.Status): string {
  const lines = (status.changes ?? []).map((change) => {
    const code = change.status.trim() || '??'
    return `- \`${code}\` ${change.path}`
  })
  const heading = status.branch ? `## Değişiklikler · ${status.branch}` : '## Değişiklikler'
  return `${heading}\n\n${lines.join('\n')}`
}

function codeClass(change: vcs.Change): string {
  if (change.untracked) return 'new'
  if (change.staged) return 'staged'
  return 'dirty'
}

interface ParsedDiffLine {
  type: 'meta' | 'hunk' | 'add' | 'remove' | 'context'
  oldLine: number | null
  newLine: number | null
  text: string
}

function parseUnifiedDiff(patch: string): {
  lines: ParsedDiffLine[]
  additions: number
  deletions: number
} {
  const rawLines = patch.split('\n')
  const lines: ParsedDiffLine[] = []
  let additions = 0
  let deletions = 0

  let curOld = 0
  let curNew = 0

  for (const line of rawLines) {
    if (line.startsWith('@@')) {
      const match = /@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line)
      if (match) {
        curOld = parseInt(match[1], 10)
        curNew = parseInt(match[2], 10)
      }
      lines.push({ type: 'hunk', oldLine: null, newLine: null, text: line })
    } else if (line.startsWith('+') && !line.startsWith('+++')) {
      additions++
      lines.push({ type: 'add', oldLine: null, newLine: curNew++, text: line.slice(1) })
    } else if (line.startsWith('-') && !line.startsWith('---')) {
      deletions++
      lines.push({ type: 'remove', oldLine: curOld++, newLine: null, text: line.slice(1) })
    } else if (line.startsWith(' ') || line === '') {
      lines.push({
        type: 'context',
        oldLine: curOld++,
        newLine: curNew++,
        text: line.startsWith(' ') ? line.slice(1) : line,
      })
    } else {
      lines.push({ type: 'meta', oldLine: null, newLine: null, text: line })
    }
  }

  return { lines, additions, deletions }
}

/** Renders a syntax-highlighted unified diff with line number gutters. */
function Patch({ patch, truncated }: { patch: string; truncated: boolean }) {
  const { lines, additions, deletions } = parseUnifiedDiff(patch)

  return (
    <div className="diff-viewer nowheel">
      <div className="diff-viewer__stats">
        <span className="diff-viewer__pill diff-viewer__pill--add">+{additions} satır</span>
        <span className="diff-viewer__pill diff-viewer__pill--del">-{deletions} satır</span>
      </div>
      <div className="diff-viewer__table">
        {lines.map((item, index) => {
          if (item.type === 'meta') {
            return (
              <div key={index} className="diff-row diff-row--meta">
                <span className="diff-row__gutter" />
                <span className="diff-row__gutter" />
                <span className="diff-row__marker"> </span>
                <span className="diff-row__content">{item.text}</span>
              </div>
            )
          }
          if (item.type === 'hunk') {
            return (
              <div key={index} className="diff-row diff-row--hunk">
                <span className="diff-row__gutter">…</span>
                <span className="diff-row__gutter">…</span>
                <span className="diff-row__marker">@</span>
                <span className="diff-row__content">{item.text}</span>
              </div>
            )
          }
          return (
            <div key={index} className={`diff-row diff-row--${item.type}`}>
              <span className="diff-row__gutter">{item.oldLine ?? ''}</span>
              <span className="diff-row__gutter">{item.newLine ?? ''}</span>
              <span className="diff-row__marker">
                {item.type === 'add' ? '+' : item.type === 'remove' ? '-' : ' '}
              </span>
              <span className="diff-row__content">{item.text || ' '}</span>
            </div>
          )
        })}
        {truncated && (
          <div className="diff-row diff-row--meta">
            <span className="diff-row__gutter" />
            <span className="diff-row__gutter" />
            <span className="diff-row__marker"> </span>
            <span className="diff-row__content">[kalan değişiklikler kısaltıldı]</span>
          </div>
        )}
      </div>
    </div>
  )
}
