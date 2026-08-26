import { memo, useCallback, useEffect, useRef, useState } from 'react'
import { Handle, NodeResizer, Position, type NodeProps } from '@xyflow/react'

import { providerStyle } from '../providers'
import { CloseButton } from './CloseButton'
import { domain } from '../../wailsjs/go/models'
import type { ConversationNodeData } from './useCanvas'

type Props = NodeProps & {
  data: ConversationNodeData & {
    onSend: (conversationID: number, prompt: string) => Promise<void>
    onClose: (id: string) => void
  }
}

export const ConversationNode = memo(function ConversationNode({ id, data, selected }: Props) {
  const { conversation, onSend, onClose } = data
  const providers = conversation.providers ?? []
  const turns = conversation.turns ?? []
  const lead = providerStyle(providers[0] ?? conversation.title)
  const group = conversation.kind === 'group'

  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const transcript = useRef<HTMLDivElement>(null)

  // Follow the tail as answers arrive, which is what a chat pane should do.
  useEffect(() => {
    const element = transcript.current
    if (element) element.scrollTop = element.scrollHeight
  }, [turns])

  const send = useCallback(async () => {
    const prompt = draft.trim()
    if (!prompt || sending) return
    setSending(true)
    try {
      await onSend(conversation.id, prompt)
      setDraft('')
    } finally {
      setSending(false)
    }
  }, [conversation.id, draft, onSend, sending])

  const onKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
      // Enter sends; Shift+Enter is a newline. Stop propagation so the canvas
      // never treats a keystroke as a shortcut.
      event.stopPropagation()
      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault()
        void send()
      }
    },
    [send],
  )

  return (
    <div
      className={`node node--conversation${selected ? ' node--selected' : ''}`}
      style={{ ['--node-accent' as string]: group ? 'var(--accent)' : lead.accent }}
    >
      <NodeResizer
        minWidth={300}
        minHeight={240}
        isVisible={selected}
        lineClassName="node__resize-line"
        handleClassName="node__resize-handle"
      />
      <header className="node__header node__grip">
        <span className="node__badges">
          {providers.map((name) => {
            const style = providerStyle(name)
            return (
              <span
                key={name}
                className="node__badge"
                style={{ ['--badge-accent' as string]: style.accent }}
                title={style.label}
              >
                {style.glyph}
              </span>
            )
          })}
        </span>
        <span className="node__title">{conversation.title}</span>
        <span className="node__kind">{group ? 'grup' : 'tekil'}</span>
        <CloseButton onClose={() => onClose(id)} label="Konuşmayı kapat" />
      </header>

      <div className="node__body node__transcript nowheel" ref={transcript}>
        {turns.length === 0 ? (
          <p className="node__placeholder">
            {group
              ? `Buraya yazdığın mesaj ${providers.length} sağlayıcıya aynı anda gider.`
              : `${lead.label} ile ayrı bir konuşma.`}
          </p>
        ) : (
          turns.map((turn) => <Turn key={turn.id} turn={turn} group={group} />)
        )}
      </div>

      {/* Left accepts a relayed answer, right sends this card's answers on. */}
      <Handle type="target" position={Position.Left} className="node__port" />
      <Handle type="source" position={Position.Right} className="node__port" />

      <div className="node__composer">
        <textarea
          className="node__input nodrag nowheel"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={onKeyDown}
          placeholder={sending ? 'Gönderiliyor…' : 'Mesaj yaz, Enter ile gönder'}
          rows={2}
          spellCheck={false}
          disabled={sending}
        />
      </div>
    </div>
  )
})

function Turn({ turn, group }: { turn: domain.ChatTurn; group: boolean }) {
  return (
    <article className="turn">
      <p className="turn__prompt">{turn.prompt}</p>
      {(turn.responses ?? []).map((response) => (
        <Response key={response.id} response={response} showName={group} />
      ))}
    </article>
  )
}

function Response({ response, showName }: { response: domain.ChatResponse; showName: boolean }) {
  const style = providerStyle(response.provider)
  const working = response.status === 'queued' || response.status === 'running'
  const partial = response.content ?? ''

  return (
    <div className="reply" style={{ ['--reply-accent' as string]: style.accent }}>
      {showName && <span className="reply__name">{style.label}</span>}
      {response.error ? (
        <p className="reply__error">{response.error}</p>
      ) : (
        <>
          {working && <Activity status={response.status} activity={response.activity} />}
          {partial !== '' && (
            <p className={working ? 'reply__text reply__text--streaming' : 'reply__text'}>
              {partial}
            </p>
          )}
        </>
      )}
    </div>
  )
}

/** What the provider is doing right now. The daemon stores a machine token; the
 *  wording lives here. */
function Activity({ status, activity }: { status: string; activity?: string }) {
  return (
    <p className="reply__working">
      <span className="reply__pips">
        <i />
        <i />
        <i />
      </span>
      {activityLabel(status, activity)}
    </p>
  )
}

function activityLabel(status: string, activity?: string): string {
  if (status === 'queued') return 'sırada'
  if (activity?.startsWith('tool:')) {
    const tool = activity.slice(5)
    return `araç çalıştırıyor: ${toolLabel(tool)}`
  }
  switch (activity) {
    case 'requesting':
      return 'modele soruyor'
    case 'thinking':
      return 'düşünüyor'
    case 'writing':
      return 'yazıyor'
    default:
      return 'çalışıyor'
  }
}

/** Tool names come straight from the provider, so unknown ones pass through. */
function toolLabel(tool: string): string {
  const known: Record<string, string> = {
    command: 'komut',
    edit: 'dosya düzenleme',
    search: 'web araması',
    write_to_file: 'dosya yazma',
    view_file: 'dosya okuma',
    grep_search: 'arama',
    list_dir: 'dizin listeleme',
    run_command: 'komut',
    read_url_content: 'sayfa okuma',
    Read: 'dosya okuma',
    Edit: 'dosya düzenleme',
    Write: 'dosya yazma',
    Bash: 'komut',
    Grep: 'arama',
    Glob: 'dosya arama',
    WebFetch: 'sayfa okuma',
    WebSearch: 'web araması',
  }
  return known[tool] ?? tool
}
