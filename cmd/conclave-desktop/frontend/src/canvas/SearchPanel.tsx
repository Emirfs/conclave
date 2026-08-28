import { useEffect, useRef, useState } from 'react'

import { domain } from '../../wailsjs/go/models'
import { providerStyle } from '../providers'

/** How long typing settles before the daemon is asked. A search box that fires
 *  on every keystroke turns a full-board scan into a per-character one. */
const DEBOUNCE = 220

const WHERE_LABELS: Record<string, string> = {
  title: 'başlık',
  role: 'rol',
  prompt: 'mesaj',
  answer: 'cevap',
  note: 'not',
}

export function SearchPanel({
  onSearch,
  onJump,
  onClose,
}: {
  onSearch: (query: string, limit: number) => Promise<domain.SearchHit[]>
  onJump: (nodeID: number) => void
  onClose: () => void
}) {
  const [query, setQuery] = useState('')
  const [hits, setHits] = useState<domain.SearchHit[]>([])
  const [searching, setSearching] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  // Only the newest query may write results: a slow scan must not overwrite a
  // faster one that was typed after it.
  const generation = useRef(0)

  useEffect(() => input.current?.focus(), [])

  useEffect(() => {
    const trimmed = query.trim()
    if (trimmed === '') {
      setHits([])
      setSearching(false)
      return
    }
    setSearching(true)
    const current = ++generation.current
    const timer = window.setTimeout(() => {
      void onSearch(trimmed, 40)
        .then((found) => {
          if (current !== generation.current) return
          setHits(found ?? [])
        })
        .finally(() => {
          if (current === generation.current) setSearching(false)
        })
    }, DEBOUNCE)
    return () => window.clearTimeout(timer)
  }, [query, onSearch])

  return (
    <div className="search nodrag nowheel">
      <div className="search__bar">
        <input
          ref={input}
          className="search__input"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Escape') onClose()
            // Enter on a single result is what anyone expects after typing
            // something specific enough to find one thing.
            if (event.key === 'Enter' && hits.length > 0) onJump(hits[0].node_id)
          }}
          placeholder="Panoda ara: başlık, rol, mesaj, cevap, not"
          spellCheck={false}
        />
        <button className="search__close" onClick={onClose} title="Aramayı kapat">
          ✕
        </button>
      </div>
      {query.trim() !== '' && (
        <div className="search__results">
          {hits.length === 0 ? (
            <p className="search__empty">{searching ? 'aranıyor…' : 'sonuç yok'}</p>
          ) : (
            hits.map((hit, index) => (
              <button
                key={`${hit.node_id}-${hit.turn_id ?? 0}-${hit.where}-${index}`}
                className="search__hit"
                onClick={() => onJump(hit.node_id)}
                style={{
                  ['--hit-accent' as string]: hit.provider
                    ? providerStyle(hit.provider).accent
                    : 'var(--line-subtle)',
                }}
              >
                <span className="search__hit-head">
                  <span className="search__hit-title">{hit.title}</span>
                  <span className="search__hit-where">
                    {WHERE_LABELS[hit.where] ?? hit.where}
                    {hit.provider ? ` · ${providerStyle(hit.provider).label}` : ''}
                  </span>
                </span>
                <span className="search__hit-snippet">{hit.snippet}</span>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  )
}
