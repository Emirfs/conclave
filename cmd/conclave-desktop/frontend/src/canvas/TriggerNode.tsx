import { memo, useEffect, useRef, useState } from 'react'
import { Handle, NodeResizer, Position, type NodeProps } from '@xyflow/react'

import { CardControls } from './CardControls'
import { CloseButton } from './CloseButton'
import { domain } from '../../wailsjs/go/models'
import type { TriggerNodeData } from './useCanvas'

type Props = NodeProps & {
  data: TriggerNodeData & {
    onSave: (triggerID: number, config: domain.TriggerConfig) => Promise<void>
    onFire: (triggerID: number) => Promise<void>
    onClose: (id: string) => void
    onResize: (id: string, direction: -1 | 1) => void
    deleting?: boolean
  }
}

/** When a scheduled trigger is next due, in the reader's own clock. */
function whenDue(due: string): string {
  if (!due) return ''
  const at = new Date(due)
  if (Number.isNaN(at.getTime())) return ''
  const minutes = Math.round((at.getTime() - Date.now()) / 60000)
  const clock = at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (minutes < 1) return 'birazdan'
  if (minutes < 60) return `${minutes} dk sonra · ${clock}`
  if (minutes < 60 * 24) return `${Math.round(minutes / 60)} sa sonra · ${clock}`
  return `${clock}`
}

/** A trigger card: the starting point of a routine. What it runs is whatever
 *  the board links to it, so it holds a message and a schedule and nothing
 *  else — the flow itself stays on the canvas where it can be seen. */
export const TriggerNode = memo(function TriggerNode({ id, data, selected }: Props) {
  const { trigger, onSave, onFire, onClose, onResize, deleting } = data
  const cardRef = useRef<HTMLDivElement>(null)
  const [title, setTitle] = useState(trigger.title)
  const [prompt, setPrompt] = useState(trigger.prompt)
  const [mode, setMode] = useState(trigger.mode)
  const [minutes, setMinutes] = useState(Math.max(1, Math.round(trigger.interval_seconds / 60)))
  const [at, setAt] = useState(trigger.at_time || '09:00')
  // Adopt server state when the board reloads, so a change made elsewhere shows.
  useEffect(() => setTitle(trigger.title), [trigger.title])
  useEffect(() => setPrompt(trigger.prompt), [trigger.prompt])
  useEffect(() => setMode(trigger.mode), [trigger.mode])
  useEffect(
    () => setMinutes(Math.max(1, Math.round(trigger.interval_seconds / 60))),
    [trigger.interval_seconds],
  )
  useEffect(() => setAt(trigger.at_time || '09:00'), [trigger.at_time])

  const armable = prompt.trim() !== ''

  const save = (enabled: boolean) =>
    void onSave(trigger.id, {
      title,
      prompt,
      mode,
      interval_seconds: Math.max(60, minutes * 60),
      at_time: at,
      enabled,
    } as domain.TriggerConfig)

  return (
    <div
      ref={cardRef}
      className={`node node--trigger${selected ? ' node--selected' : ''}${
        trigger.enabled ? ' node--trigger-armed' : ''
      }${deleting ? ' node--deleting' : ''}`}
    >
      <NodeResizer
        minWidth={280}
        minHeight={240}
        isVisible={selected}
        lineClassName="node__resize-line"
        handleClassName="node__resize-handle node__resize-handle--large"
      />
      <header className="node__header node__grip">
        <input
          className="node__trigger-title nodrag"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          onKeyDown={(event) => event.stopPropagation()}
          onBlur={() => save(trigger.enabled)}
          placeholder="Tetikleyici adı"
          spellCheck={false}
        />
        <CardControls
          target={cardRef}
          onShrink={() => onResize(id, -1)}
          onGrow={() => onResize(id, 1)}
        />
        <CloseButton onClose={() => onClose(id)} label="Tetikleyiciyi sil" />
      </header>

      <div className="node__toolbar">
        <select
          className="trigger__mode nodrag"
          value={mode}
          onChange={(event) => {
            setMode(event.target.value)
            // Switching to manual disarms: there is no schedule left to keep.
            if (event.target.value === 'manual') save(false)
          }}
          onKeyDown={(event) => event.stopPropagation()}
        >
          <option value="manual">elle</option>
          <option value="interval">her</option>
          <option value="daily">her gün</option>
        </select>
        {mode === 'interval' && (
          <label className="trigger__field">
            <input
              className="trigger__number nodrag"
              type="number"
              min={1}
              value={minutes}
              onChange={(event) => setMinutes(Number(event.target.value))}
              onKeyDown={(event) => event.stopPropagation()}
              onBlur={() => save(trigger.enabled)}
            />
            <span className="trigger__unit">dakikada bir</span>
          </label>
        )}
        {mode === 'daily' && (
          <input
            className="trigger__time nodrag"
            type="time"
            value={at}
            onChange={(event) => setAt(event.target.value)}
            onKeyDown={(event) => event.stopPropagation()}
            onBlur={() => save(trigger.enabled)}
          />
        )}
      </div>

      <textarea
        className="trigger__prompt nodrag nowheel"
        value={prompt}
        onChange={(event) => setPrompt(event.target.value)}
        onKeyDown={(event) => event.stopPropagation()}
        onBlur={() => save(trigger.enabled)}
        placeholder="Bağlı kartlara gidecek mesaj…"
        spellCheck={false}
      />

      <footer className="trigger__foot">
        <button
          className="trigger__arm nodrag"
          onClick={() => save(!trigger.enabled)}
          disabled={!armable || mode === 'manual'}
          title={
            mode === 'manual'
              ? 'Elle çalışan bir tetikleyicinin kuracak zamanı yok'
              : armable
                ? 'Zamanı geldiğinde kendiliğinden çalışsın'
                : 'Önce gönderilecek mesajı yaz'
          }
        >
          {trigger.enabled ? 'kurulu ✓' : 'kur'}
        </button>
        <button
          className="trigger__fire nodrag"
          onClick={() => void onFire(trigger.id)}
          disabled={!armable || trigger.working}
          title="Zamanı beklemeden şimdi çalıştır"
        >
          {trigger.working ? 'çalışıyor…' : '▶ şimdi'}
        </button>
        <span className="trigger__status">
          {trigger.working
            ? 'akış sürüyor'
            : trigger.enabled && trigger.due_at
              ? whenDue(trigger.due_at)
              : trigger.last_fired_at
                ? 'en son çalıştı'
                : 'hiç çalışmadı'}
        </span>
      </footer>

      {/* A trigger starts flows and receives none, so it has no input port. */}
      <Handle type="source" position={Position.Right} id="out" className="node__port" />
    </div>
  )
})
