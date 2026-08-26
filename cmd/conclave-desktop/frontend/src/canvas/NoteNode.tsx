import { memo, useCallback, useRef, useState } from 'react'
import { NodeResizer, type NodeProps } from '@xyflow/react'

import { CardControls } from './CardControls'
import { CloseButton } from './CloseButton'
import { Markdown } from './Markdown'
import type { NoteNodeData } from './useCanvas'

type Props = NodeProps & {
  data: NoteNodeData & {
    onBodyChange: (id: string, body: string) => void
    onClose: (id: string) => void
    onResize: (id: string, direction: -1 | 1) => void
  }
}

export const NoteNode = memo(function NoteNode({ id, data, selected }: Props) {
  const { onBodyChange, onClose, onResize } = data
  const [preview, setPreview] = useState(false)
  const card = useRef<HTMLDivElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const change = useCallback(
    (event: React.ChangeEvent<HTMLTextAreaElement>) => onBodyChange(id, event.target.value),
    [id, onBodyChange],
  )

  // Without this the canvas sees Backspace and Delete as "remove the selected
  // node", so editing a note would delete it mid-sentence.
  const onKeyDown = useCallback((event: React.KeyboardEvent) => event.stopPropagation(), [])

  const openMarkdown = useCallback(
    async (event: React.ChangeEvent<HTMLInputElement>) => {
      const file = event.target.files?.[0]
      if (!file) return
      onBodyChange(id, await file.text())
      setPreview(true)
      event.target.value = ''
    },
    [id, onBodyChange],
  )

  return (
    <div
      ref={card}
      className={`node node--note${selected ? ' node--selected' : ''}`}
      style={{ ['--note-accent' as string]: data.color || 'var(--warning)' }}
    >
      <NodeResizer
        minWidth={240}
        minHeight={120}
        isVisible={selected}
        lineClassName="node__resize-line"
        handleClassName="node__resize-handle"
      />
      <div className="node__grip node__grip--note">
        <span className="node__grip-dots" aria-hidden="true" />
        <button
          className="node__preview-toggle nodrag"
          onClick={() => fileInput.current?.click()}
          title="Markdown dosyası aç"
        >
          md aç
        </button>
        <button
          className="node__preview-toggle nodrag"
          onClick={() => setPreview((current) => !current)}
          title={preview ? 'Markdown düzenle' : 'Markdown önizle'}
        >
          {preview ? 'düzenle' : 'önizle'}
        </button>
        <CardControls
          target={card}
          onShrink={() => onResize(id, -1)}
          onGrow={() => onResize(id, 1)}
        />
        <CloseButton onClose={() => onClose(id)} label="Notu sil" />
        <input
          ref={fileInput}
          className="node__file-input"
          type="file"
          accept=".md,.markdown,text/markdown,text/plain"
          onChange={(event) => void openMarkdown(event)}
        />
      </div>
      {preview ? (
        <div className="node__note-preview nodrag nowheel">
          {data.body ? <Markdown>{data.body}</Markdown> : <span className="node__placeholder">Önizlenecek Markdown yok.</span>}
        </div>
      ) : (
        <textarea
          className="node__note-text nodrag nowheel"
          value={data.body}
          onChange={change}
          onKeyDown={onKeyDown}
          placeholder="Markdown notu…"
          spellCheck={false}
        />
      )}
    </div>
  )
})
