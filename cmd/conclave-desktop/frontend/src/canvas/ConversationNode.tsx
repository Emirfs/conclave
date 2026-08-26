import { memo } from 'react'
import { NodeResizer, type NodeProps } from '@xyflow/react'

import { providerStyle } from '../providers'
import type { ConversationNodeData } from './useCanvas'

type Props = NodeProps & { data: ConversationNodeData }

/** A conversation is a framed panel on the board. Stage 3 fills the transcript
 *  area with live provider output; the frame and its identity live here. */
export const ConversationNode = memo(function ConversationNode({ data, selected }: Props) {
  const { conversation } = data
  const providers = conversation.providers ?? []
  const lead = providerStyle(providers[0] ?? conversation.title)
  const group = conversation.kind === 'group'

  return (
    <div
      className={`node node--conversation${selected ? ' node--selected' : ''}`}
      style={{ ['--node-accent' as string]: group ? 'var(--accent)' : lead.accent }}
    >
      <NodeResizer minWidth={280} minHeight={200} isVisible={selected} lineClassName="node__resize-line" handleClassName="node__resize-handle" />
      <header className="node__header">
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
      </header>
      <div className="node__body node__body--transcript">
        <p className="node__placeholder">
          {group
            ? `Bu node'a yazdığın mesaj ${providers.length} sağlayıcıya aynı anda gider.`
            : `${lead.label} ile ayrı bir konuşma.`}
        </p>
      </div>
    </div>
  )
})
