import { memo, useCallback } from 'react'
import { NodeResizer, type NodeProps } from '@xyflow/react'

import { CloseButton } from './CloseButton'
import type { NoteNodeData } from './useCanvas'

type Props = NodeProps & {
  data: NoteNodeData & {
    onBodyChange: (id: string, body: string) => void
    onClose: (id: string) => void
  }
}

export const NoteNode = memo(function NoteNode({ id, data, selected }: Props) {
  const { onBodyChange, onClose } = data
  const change = useCallback(
    (event: React.ChangeEvent<HTMLTextAreaElement>) => onBodyChange(id, event.target.value),
    [id, onBodyChange],
  )

  // Without this the canvas sees Backspace and Delete as "remove the selected
  // node", so editing a note would delete it mid-sentence.
  const onKeyDown = useCallback((event: React.KeyboardEvent) => event.stopPropagation(), [])

  return (
    <div
      className={`node node--note${selected ? ' node--selected' : ''}`}
      style={{ ['--note-accent' as string]: data.color || 'var(--warning)' }}
    >
      <NodeResizer
        minWidth={160}
        minHeight={120}
        isVisible={selected}
        lineClassName="node__resize-line"
        handleClassName="node__resize-handle"
      />
      <div className="node__grip node__grip--note">
        <span className="node__grip-dots" aria-hidden="true" />
        <CloseButton onClose={() => onClose(id)} label="Notu sil" />
      </div>
      <textarea
        className="node__note-text nodrag nowheel"
        value={data.body}
        onChange={change}
        onKeyDown={onKeyDown}
        placeholder="Not…"
        spellCheck={false}
      />
    </div>
  )
})
