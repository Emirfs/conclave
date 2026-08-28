import { memo, useEffect, useRef, useState } from 'react'
import { Handle, NodeResizer, Position, type NodeProps } from '@xyflow/react'

import { CardControls } from './CardControls'
import { CloseButton } from './CloseButton'
import { domain } from '../../wailsjs/go/models'
import type { GateNodeData } from './useCanvas'

type Props = NodeProps & {
  data: GateNodeData & {
    onSave: (gateID: number, config: domain.GateConfig) => Promise<void>
    onClose: (id: string) => void
    onResize: (id: string, direction: -1 | 1) => void
    deleting?: boolean
  }
}

const MODE_LABELS: Record<string, string> = {
  contains: 'şunu içeriyorsa',
  missing: 'şunu içermiyorsa',
  matches: 'şu ifadeye uyuyorsa',
  not_empty: 'boş değilse',
}

const PLACEHOLDERS: Record<string, string> = {
  contains: 'HATA',
  missing: 'TAMAM',
  matches: 'exit [1-9][0-9]*',
  not_empty: '',
}

/** A gate card: it reads the message that reached it and sends it out of one of
 *  two ports. It says nothing of its own — a decision point is not a speaker —
 *  so what leaves it is exactly what arrived. */
export const GateNode = memo(function GateNode({ id, data, selected }: Props) {
  const { gate, onSave, onClose, onResize, deleting } = data
  const cardRef = useRef<HTMLDivElement>(null)
  const [title, setTitle] = useState(gate.title)
  const [mode, setMode] = useState(gate.mode)
  const [pattern, setPattern] = useState(gate.pattern)
  const [sensitive, setSensitive] = useState(gate.case_sensitive)
  // Adopt server state when the board reloads, so a change made elsewhere shows.
  useEffect(() => setTitle(gate.title), [gate.title])
  useEffect(() => setMode(gate.mode), [gate.mode])
  useEffect(() => setPattern(gate.pattern), [gate.pattern])
  useEffect(() => setSensitive(gate.case_sensitive), [gate.case_sensitive])

  const save = (next?: Partial<domain.GateConfig>) =>
    void onSave(gate.id, {
      title,
      mode,
      pattern,
      case_sensitive: sensitive,
      ...next,
    } as domain.GateConfig)

  return (
    <div
      ref={cardRef}
      className={`node node--gate${selected ? ' node--selected' : ''}${deleting ? ' node--deleting' : ''}`}
    >
      <NodeResizer
        minWidth={260}
        minHeight={200}
        isVisible={selected}
        lineClassName="node__resize-line"
        handleClassName="node__resize-handle node__resize-handle--large"
      />
      <header className="node__header node__grip">
        <input
          className="node__gate-title nodrag"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          onKeyDown={(event) => event.stopPropagation()}
          onBlur={() => save()}
          placeholder="Kapı adı"
          spellCheck={false}
        />
        <CardControls
          target={cardRef}
          onShrink={() => onResize(id, -1)}
          onGrow={() => onResize(id, 1)}
        />
        <CloseButton onClose={() => onClose(id)} label="Kapıyı sil" />
      </header>

      <div className="gate__body">
        <span className="gate__lead">Gelen mesaj</span>
        <select
          className="gate__mode nodrag"
          value={mode}
          onChange={(event) => {
            setMode(event.target.value)
            save({ mode: event.target.value })
          }}
          onKeyDown={(event) => event.stopPropagation()}
        >
          {Object.entries(MODE_LABELS).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
        {mode !== 'not_empty' && (
          <input
            className="gate__pattern nodrag"
            value={pattern}
            onChange={(event) => setPattern(event.target.value)}
            onKeyDown={(event) => event.stopPropagation()}
            onBlur={() => save()}
            placeholder={PLACEHOLDERS[mode] ?? ''}
            spellCheck={false}
          />
        )}
        {mode !== 'not_empty' && (
          <label className="gate__case nodrag">
            <input
              type="checkbox"
              checked={sensitive}
              onChange={(event) => {
                setSensitive(event.target.checked)
                save({ case_sensitive: event.target.checked })
              }}
            />
            büyük/küçük harf ayrımı
          </label>
        )}
      </div>

      <footer className="gate__foot">
        <span className={`gate__port gate__port--pass${gate.last_result === 'pass' ? ' gate__port--hit' : ''}`}>
          geçer
        </span>
        <span className={`gate__port gate__port--else${gate.last_result === 'else' ? ' gate__port--hit' : ''}`}>
          kalan
        </span>
      </footer>

      <Handle type="target" position={Position.Left} id="in" className="node__port" />
      {/* Two ways out. The upper one carries the passing case, the lower one
          everything else; the labels beside them say which is which. */}
      <Handle
        type="source"
        position={Position.Right}
        id="pass"
        className="node__port node__port--pass"
        style={{ top: '38%' }}
      />
      <Handle
        type="source"
        position={Position.Right}
        id="else"
        className="node__port node__port--else"
        style={{ top: '72%' }}
      />
    </div>
  )
})
