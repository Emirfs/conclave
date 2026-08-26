import { memo, useCallback } from 'react'
import { NodeResizer, type NodeProps } from '@xyflow/react'

import type { NoteNodeData } from './useCanvas'

type Props = NodeProps & {
  data: NoteNodeData & { onBodyChange: (id: string, body: string) => void }
}

export const NoteNode = memo(function NoteNode({ id, data, selected }: Props) {
  const { onBodyChange } = data
  const change = useCallback(
    (event: React.ChangeEvent<HTMLTextAreaElement>) => onBodyChange(id, event.target.value),
    [id, onBodyChange],
  )

  return (
    <div
      className={`node node--note${selected ? ' node--selected' : ''}`}
      style={{ ['--note-accent' as string]: data.color || 'var(--warning)' }}
    >
      <NodeResizer minWidth={160} minHeight={120} isVisible={selected} lineClassName="node__resize-line" handleClassName="node__resize-handle" />
      <textarea
        className="node__note-text nodrag"
        value={data.body}
        onChange={change}
        placeholder="Not…"
        spellCheck={false}
      />
    </div>
  )
})
