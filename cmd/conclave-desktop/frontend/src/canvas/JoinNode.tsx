import { memo, useEffect, useState } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'

import { CloseButton } from './CloseButton'
import type { JoinNodeData } from './useCanvas'

type Props = NodeProps & {
  data: JoinNodeData & {
    onBodyChange: (id: string, body: string) => void
    onClose: (id: string) => void
    deleting?: boolean
  }
}

/** A waiting point. Every line feeding it must speak before it passes anything
 *  on, and then it passes on one message carrying all of them. Without it, two
 *  answers arriving at the same card start that card twice — which is what
 *  makes a board of three or more linked cards impossible to follow. */
export const JoinNode = memo(function JoinNode({ id, data, selected }: Props) {
  const { join, onBodyChange, onClose, deleting } = data
  const [title, setTitle] = useState(data.body)
  // Adopt server state when the board reloads, so a rename made elsewhere shows.
  useEffect(() => setTitle(data.body), [data.body])

  const expected = join?.expected ?? 0
  const waiting = join?.waiting ?? 0
  const sources = join?.sources ?? []

  return (
    <div
      className={`node node--join${selected ? ' node--selected' : ''}${deleting ? ' node--deleting' : ''}`}
    >
      <header className="node__header node__grip">
        <input
          className="node__join-title nodrag"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          onKeyDown={(event) => event.stopPropagation()}
          onBlur={() => onBodyChange(id, title)}
          placeholder="Birleştirici"
          spellCheck={false}
        />
        <CloseButton onClose={() => onClose(id)} label="Birleştiriciyi sil" />
      </header>

      <div className="join__body">
        <span className="join__count">
          {waiting} / {expected}
        </span>
        <span className="join__label">
          {expected === 0
            ? 'Henüz bağlı hat yok'
            : waiting === 0
              ? 'hattın konuşması bekleniyor'
              : 'hat konuştu, kalanlar bekleniyor'}
        </span>
        {sources.length > 0 && (
          <ul className="join__sources">
            {sources.map((source, index) => (
              <li key={index} className="join__source">
                {source}
              </li>
            ))}
          </ul>
        )}
      </div>

      {/* Left takes in each line; right hands the combined message on. */}
      <Handle type="target" position={Position.Left} id="in" className="node__port" />
      <Handle type="source" position={Position.Right} id="out" className="node__port" />
    </div>
  )
})
