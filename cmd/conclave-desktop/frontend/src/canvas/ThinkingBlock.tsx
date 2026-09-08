import { useState } from 'react'

export function extractThinking(raw: string): { thinking: string | null; answer: string } {
  const pattern = /<(thought|thinking)>([\s\S]*?)(?:<\/\1>|$)/gi
  const thoughts: string[] = []
  const answer = raw.replace(pattern, (_, _tag, content: string) => {
    const trimmed = content.trim()
    if (trimmed) thoughts.push(trimmed)
    return ''
  }).trim()

  return {
    thinking: thoughts.length > 0 ? thoughts.join('\n\n') : null,
    answer,
  }
}

export function ThinkingBlock({
  content,
  working,
}: {
  content: string
  working?: boolean
}) {
  const [open, setOpen] = useState(false)

  if (!content.trim()) return null

  return (
    <div className={`thinking-block${open ? ' thinking-block--open' : ''}`}>
      <button
        className="thinking-block__header nodrag"
        onClick={() => setOpen(!open)}
        type="button"
        title={open ? 'Düşünce sürecini gizle' : 'Düşünce sürecini göster'}
      >
        <span className="thinking-block__title">
          <span className="thinking-block__sparkle" aria-hidden="true">✦</span>
          <span>Düşünce Süreci</span>
          {working && (
            <span className="thinking-block__pulse" title="Model düşünüyor...">
              <span className="thinking-block__pulse-ping" />
              <span className="thinking-block__pulse-dot" />
            </span>
          )}
        </span>
        <span className="thinking-block__toggle">{open ? 'gizle ✕' : 'göster ▼'}</span>
      </button>
      {open && (
        <div className="thinking-block__body">
          <p className="thinking-block__text">{content}</p>
        </div>
      )}
    </div>
  )
}
