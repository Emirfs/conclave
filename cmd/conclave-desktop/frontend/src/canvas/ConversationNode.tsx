import { memo, useCallback, useEffect, useRef, useState } from 'react'
import { Handle, NodeResizer, Position, useConnection, type NodeProps } from '@xyflow/react'

import { providerStyle } from '../providers'
import { CardControls } from './CardControls'
import { CloseButton } from './CloseButton'
import { Changes } from './Changes'
import { LoopPanel } from './LoopPanel'
import { Markdown } from './Markdown'
import { ModelPicker } from './ModelPicker'
import { domain } from '../../wailsjs/go/models'
import { ROLES, roleName } from './roles'
import type { ConversationNodeData } from './useCanvas'

type Props = NodeProps & {
  data: ConversationNodeData & {
    onSend: (conversationID: number, prompt: string) => Promise<void>
    onClose: (id: string) => void
    onPickProject: (conversationID: number, current: string) => Promise<void>
    onToggleAccess: (conversationID: number, project: string, access: string) => Promise<void>
    onSaveLoop: (conversationID: number, config: domain.LoopConfig) => Promise<void>
    onToggleLoop: (conversationID: number, running: boolean) => Promise<void>
    onSaveRole: (conversationID: number, role: string) => Promise<void>
    onSaveModel: (conversationID: number, provider: string, model: string) => Promise<void>
    onResumeDialogue: (conversationID: number) => Promise<void>
    onBranch: (conversationID: number, answer: string) => void
    /** Puts text on the board as its own note card, next to this one. */
    onPinNote: (body: string) => void
    onResize: (id: string, direction: -1 | 1) => void
  }
}

type Tab = 'chat' | 'changes' | 'tests'

export const ConversationNode = memo(function ConversationNode({ id, data, selected }: Props) {
  const { conversation, onSend, onClose, onPickProject, onToggleAccess, onSaveLoop, onToggleLoop,
    onSaveRole, onSaveModel, onResumeDialogue, onBranch, onPinNote, onResize } = data
  const project = conversation.project_path ?? ''
  const access = conversation.access ?? 'edit'
  const [tab, setTab] = useState<Tab>('chat')
  // True only while the user is dragging a connection somewhere on the board.
  const linking = useConnection((connection) => connection.inProgress)
  const providers = conversation.providers ?? []
  const turns = conversation.turns ?? []
  const lead = providerStyle(providers[0] ?? conversation.title)
  const group = conversation.kind === 'group'
  // A group card has one model per provider and shows them on its chips, so
  // only a solo card can carry its model in the title.
  const soloModel = group ? '' : (conversation.models?.[providers[0]] ?? '')

  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const dialogueState = conversation.dialogue_state ?? ''
  // The role is edited locally and written on blur, so the poll that refreshes
  // the board cannot overwrite a half-typed line.
  const savedRole = conversation.role ?? ''
  const [role, setRole] = useState(savedRole)
  useEffect(() => setRole(savedRole), [savedRole])
  const transcript = useRef<HTMLDivElement>(null)
  const card = useRef<HTMLDivElement>(null)

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
      ref={card}
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
        {/* A solo card is named after what it actually is: the provider and the
            model it runs on. Which of several cards to read is decided by that
            far more often than by the title the card was created with. */}
        <span className="node__title">
          {group ? conversation.title : lead.label}
          {soloModel !== '' && <span className="node__title-model">{soloModel}</span>}
        </span>
        {/* The role belongs in the title bar: which card does what is the first
            thing you need from a board of several. */}
        {savedRole !== '' && (
          <span className="node__badge" title={savedRole}>
            {roleName(savedRole)}
          </span>
        )}
        <span className="node__kind">{group ? 'grup' : 'tekil'}</span>
        <CardControls
          target={card}
          onShrink={() => onResize(id, -1)}
          onGrow={() => onResize(id, 1)}
        />
        <CloseButton onClose={() => onClose(id)} label="Konuşmayı kapat" />
      </header>

      <div className="node__toolbar nodrag">
        <button
          className="node__project"
          onClick={() => void onPickProject(conversation.id, project)}
          title={project || 'Bu kart için bir proje dizini seç'}
        >
          <FolderIcon />
          <span className="node__project-path">{project ? shortPath(project) : 'Proje seç'}</span>
        </button>
        <button
          className={`node__access node__access--${access}`}
          onClick={() => void onToggleAccess(conversation.id, project, access === 'edit' ? 'read' : 'edit')}
          title={
            access === 'edit'
              ? 'Sağlayıcı bu projede dosya değiştirebilir ve komut çalıştırabilir'
              : 'Sağlayıcı yalnızca okuyabilir'
          }
          disabled={!project}
        >
          {access === 'edit' ? 'düzenleyebilir' : 'salt okunur'}
        </button>
        {/* Which model each provider runs on. Changing it drops that provider's
            session, so the next answer really comes from the model named here. */}
        <ModelPicker
          providers={providers}
          chosen={conversation.models ?? {}}
          onSave={(name, model) => onSaveModel(conversation.id, name, model)}
        />
        <span className="node__tabs">
          <button
            className={`node__tab${tab === 'chat' ? ' node__tab--active' : ''}`}
            onClick={() => setTab('chat')}
          >
            sohbet
          </button>
          <button
            className={`node__tab${tab === 'changes' ? ' node__tab--active' : ''}`}
            onClick={() => setTab('changes')}
            disabled={!project}
            title={project ? 'Projedeki değişiklikler' : 'Önce bir proje seç'}
          >
            değişiklikler
          </button>
          <button
            className={`node__tab${tab === 'tests' ? ' node__tab--active' : ''}`}
            onClick={() => setTab('tests')}
            disabled={!project}
            title={project ? 'Her turdan sonra çalışacak komut' : 'Önce bir proje seç'}
          >
            döngü{conversation.loop_running ? ' ●' : ''}
          </button>
        </span>
      </div>

      {tab === 'tests' ? (
        <div className="node__body nowheel">
          <LoopPanel
            conversationID={conversation.id}
            loop={conversation.loop}
            running={conversation.loop_running ?? false}
            runs={conversation.runs ?? []}
            onSave={onSaveLoop}
            onToggleRunning={onToggleLoop}
          />
        </div>
      ) : tab === 'changes' ? (
        <div className="node__body nowheel">
          <Changes
            conversationID={conversation.id}
            refreshKey={turns.length}
            onPin={(_title, body) => onPinNote(body)}
          />
        </div>
      ) : (
      <div className="node__body node__transcript nowheel" ref={transcript}>
        {turns.length === 0 ? (
          <p className="node__placeholder">
            {group
              ? `Buraya yazdığın mesaj ${providers.length} sağlayıcıya aynı anda gider.`
              : `${lead.label} ile ayrı bir konuşma.`}
          </p>
        ) : (
          turns.map((turn) => (
            <Turn
              key={turn.id}
              turn={turn}
              group={group}
              onBranch={(answer) => onBranch(conversation.id, answer)}
            />
          ))
        )}
      </div>
      )}

      {/* Left accepts a relayed answer, right sends this card's answers on. */}
      <Handle type="target" position={Position.Left} id="in" className="node__port" />
      <Handle type="source" position={Position.Right} id="out" className="node__port" />
      {/* A card-wide drop target, but only while a link is actually being
          drawn. Rendering it always would cover the card and swallow every
          click on its header, buttons and input. */}
      {linking && (
        <Handle type="target" position={Position.Left} id="anywhere" className="node__dropzone" />
      )}

      {tab === 'chat' && dialogueState !== '' && (
        <div className={`node__dialogue node__dialogue--${dialogueState}`}>
          {dialogueState === 'done' ? (
            <span className="node__dialogue-text">Konuşma tamamlandı.</span>
          ) : (
            <>
              <span className="node__dialogue-text">Kart senin kararını bekliyor.</span>
              <button
                className="node__dialogue-resume nodrag"
                onClick={() => void onResumeDialogue(conversation.id)}
                title="Beklemeyi kaldır; yazacağın mesaj konuşmayı yeniden başlatır"
              >
                devam ettir
              </button>
            </>
          )}
        </div>
      )}

      {tab === 'chat' && (
        <div className="node__role nodrag">
          <div className="node__role-line">
            <span className="node__role-label">rol</span>
            <input
              className="node__role-input"
              value={role}
              placeholder="Bağlı kartla çalışırken bu kartın işi ne?"
              onChange={(event) => setRole(event.target.value)}
              onBlur={() => role !== savedRole && void onSaveRole(conversation.id, role)}
              spellCheck={false}
            />
            {role !== '' && (
              <button
                className="node__role-clear"
                onClick={() => {
                  setRole('')
                  void onSaveRole(conversation.id, '')
                }}
                title="Rolü kaldır"
              >
                ×
              </button>
            )}
          </div>
          {/* Templates are a starting point, not a constraint: the text stays
              editable and a card can be given anything at all. */}
          <div className="node__role-templates">
            {ROLES.map((item) => (
              <button
                key={item.name}
                className={`node__role-chip${roleName(role) === item.name ? ' node__role-chip--active' : ''}`}
                onClick={() => {
                  setRole(item.text)
                  void onSaveRole(conversation.id, item.text)
                }}
                title={item.text}
              >
                {item.name}
              </button>
            ))}
          </div>
        </div>
      )}

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

function Turn({
  turn,
  group,
  onBranch,
}: {
  turn: domain.ChatTurn
  group: boolean
  onBranch: (answer: string) => void
}) {
  return (
    <article className="turn">
      <Prompt prompt={turn.prompt} kind={turn.kind ?? 'user'} />
      {(turn.responses ?? []).map((response) => (
        <Response key={response.id} response={response} showName={group} onBranch={onBranch} />
      ))}
    </article>
  )
}

/** How long a prompt may be before it is folded away. Relayed prompts carry a
 *  whole answer from another card, and a transcript where every incoming
 *  message is full length is unreadable. */
const PROMPT_FOLD_AFTER = 320

/** One incoming message. Who sent it decides how it is drawn: a person, another
 *  card, or the system pushing a stalled exchange on. Without that separation a
 *  transcript reads as though the user typed everything in it. */
function Prompt({ prompt, kind }: { prompt: string; kind: string }) {
  const [open, setOpen] = useState(false)
  // A briefing is prepended to the first relayed message and separated with a
  // rule. It is context, not the message, so it folds on its own.
  const [context, message] = splitBriefing(prompt)
  const speaker = kind === 'relay' ? leadingSpeaker(message) : null
  const text = speaker ? message.slice(speaker.length + 2) : message
  const foldable = text.length > PROMPT_FOLD_AFTER
  const shown = foldable && !open ? text.slice(0, PROMPT_FOLD_AFTER).trimEnd() + '…' : text

  return (
    <div className={`prompt prompt--${kind}`}>
      <span className="prompt__from">{label(kind, speaker)}</span>
      {context !== '' && <Folded label="bağlam" body={context} />}
      {kind === 'user' ? (
        <p className="prompt__text prompt__text--plain">{shown}</p>
      ) : (
        <div className="prompt__text">
          <Markdown>{shown}</Markdown>
        </div>
      )}
      {foldable && (
        <button className="prompt__more nodrag" onClick={() => setOpen(!open)}>
          {open ? 'katla' : 'devamı'}
        </button>
      )}
    </div>
  )
}

/** A block that stays out of the way until asked for. */
function Folded({ label, body }: { label: string; body: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="prompt__context">
      <button className="prompt__more nodrag" onClick={() => setOpen(!open)}>
        {open ? `${label} ✕` : label}
      </button>
      {open && (
        <div className="prompt__text prompt__text--context">
          <Markdown>{body}</Markdown>
        </div>
      )}
    </div>
  )
}

function label(kind: string, speaker: string | null): string {
  if (kind === 'nudge') return 'konuşmayı sürdür'
  if (kind === 'relay') return speaker ? `← ${speaker}` : '← bağlı kart'
  return 'sen'
}

/** Splits the one-off briefing from the message it rides along with. The rule
 *  is written by framePayload; anything else is a message on its own. */
function splitBriefing(prompt: string): [string, string] {
  const at = prompt.indexOf('\n\n---\n\n')
  if (at === -1) return ['', prompt]
  return [prompt.slice(0, at), prompt.slice(at + 7)]
}

/** Relayed messages start with the speaking card's name. Pulling it out lets it
 *  be shown as a label instead of as the first words of the text. */
function leadingSpeaker(message: string): string | null {
  const end = message.indexOf(': ')
  if (end === -1 || end > 40) return null
  const name = message.slice(0, end)
  return name.includes('\n') ? null : name
}

/** How many characters of an answer are shown before it is folded. Long answers
 *  turn a card into a scrolling well, and the turn you want is never the one on
 *  screen. */
const FOLD_AFTER = 700

function Response({
  response,
  showName,
  onBranch,
}: {
  response: domain.ChatResponse
  showName: boolean
  onBranch: (answer: string) => void
}) {
  const style = providerStyle(response.provider)
  const working = response.status === 'queued' || response.status === 'running'
  const partial = response.content ?? ''
  const [open, setOpen] = useState(false)
  // Folding a streaming answer would hide the part being written.
  const foldable = !working && partial.length > FOLD_AFTER
  const shown = foldable && !open ? partial.slice(0, FOLD_AFTER).trimEnd() + '…' : partial

  return (
    <div className="reply" style={{ ['--reply-accent' as string]: style.accent }}>
      {showName && <span className="reply__name">{style.label}</span>}
      {response.error ? (
        <p className="reply__error">{response.error}</p>
      ) : (
        <>
          {working && <Activity status={response.status} activity={response.activity} />}
          {partial !== '' && (
            working ? (
              <p className="reply__text reply__text--streaming">{partial}</p>
            ) : (
              <div className="reply__text">
                <Markdown>{shown}</Markdown>
              </div>
            )
          )}
          {(foldable || (!working && partial !== '')) && (
            <div className="reply__actions nodrag">
              {foldable && (
                <button className="reply__action" onClick={() => setOpen(!open)}>
                  {open ? 'katla' : `devamı (${Math.round(partial.length / 100) / 10}k)`}
                </button>
              )}
              {!working && partial !== '' && (
                <button
                  className="reply__action"
                  onClick={() => onBranch(partial)}
                  title="Bu cevaptan yeni bir kart başlat"
                >
                  ↗ dallan
                </button>
              )}
            </div>
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

/** Long paths do not fit a card header; the tail is the informative part. */
const SEPARATOR = /[\\/]/

function shortPath(path: string): string {
  const parts = path.split(SEPARATOR).filter(Boolean)
  if (parts.length <= 2) return path
  return '…/' + parts.slice(-2).join('/')
}

function FolderIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
      <path
        d="M1 3.2c0-.4.3-.7.7-.7h2.4l1 1.2h5.2c.4 0 .7.3.7.7v4.9c0 .4-.3.7-.7.7H1.7a.7.7 0 0 1-.7-.7z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1"
        strokeLinejoin="round"
      />
    </svg>
  )
}
