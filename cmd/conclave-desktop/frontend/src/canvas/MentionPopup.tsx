import { useEffect, useRef } from 'react'

export interface MentionCandidate {
  id: string
  name: string
  role?: string
  accent: string
  glyph: string
}

export function MentionPopup({
  candidates,
  selectedIndex,
  onSelect,
}: {
  candidates: MentionCandidate[]
  selectedIndex: number
  onSelect: (candidate: MentionCandidate) => void
}) {
  const listRef = useRef<HTMLUListElement>(null)

  useEffect(() => {
    const list = listRef.current
    if (!list) return
    const active = list.children[selectedIndex] as HTMLElement | undefined
    if (active) {
      active.scrollIntoView({ block: 'nearest' })
    }
  }, [selectedIndex])

  if (candidates.length === 0) return null

  return (
    <div className="mention-popup nodrag nowheel">
      <div className="mention-popup__header">
        <span className="mention-popup__title">Ajan / Kart Etiketle</span>
        <span className="mention-popup__hint">Enter veya Tab ile seç</span>
      </div>
      <ul ref={listRef} className="mention-popup__list">
        {candidates.map((item, index) => (
          <li key={item.id}>
            <button
              type="button"
              className={`mention-popup__item${index === selectedIndex ? ' mention-popup__item--selected' : ''}`}
              onClick={() => onSelect(item)}
            >
              <span
                className="mention-popup__badge"
                style={{ ['--badge-accent' as string]: item.accent }}
              >
                {item.glyph}
              </span>
              <span className="mention-popup__info">
                <span className="mention-popup__name">{item.name}</span>
                {item.role && <span className="mention-popup__role">{item.role}</span>}
              </span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}
