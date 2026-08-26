import { useCallback, useMemo } from 'react'
import {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  applyNodeChanges,
  useReactFlow,
  type Connection,
  type EdgeChange,
  type NodeChange,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'

import { domain } from '../../wailsjs/go/models'
import { ConversationNode } from './ConversationNode'
import { NoteNode } from './NoteNode'
import type { BoardNode, useCanvas } from './useCanvas'

const nodeTypes = { conversation: ConversationNode, note: NoteNode }

export type BoardHandle = ReturnType<typeof useCanvas>

export function Board({ canvas }: { canvas: BoardHandle }) {
  return (
    <ReactFlowProvider>
      <BoardSurface canvas={canvas} />
    </ReactFlowProvider>
  )
}

function BoardSurface({ canvas }: { canvas: BoardHandle }) {
  const { nodes, setNodes, edges, patch, remove, setNoteBody, send, link, unlink } = canvas

  // Closing removes the node and, for a conversation, its history with it.
  const close = useCallback((id: string) => void remove(id), [remove])
  const flow = useReactFlow()

  // Notes need a callback in their data so the textarea can report edits, but
  // the hook owns the state. Injecting it here keeps NoteNode presentational.
  const decorated = useMemo(
    () =>
      nodes.map((node) =>
        node.data.kind === 'note'
          ? { ...node, data: { ...node.data, onBodyChange: setNoteBody, onClose: close } }
          : { ...node, data: { ...node.data, onSend: send, onClose: close } },
      ),
    [nodes, setNoteBody, send, close],
  )

  const onNodesChange = useCallback(
    (changes: NodeChange<BoardNode>[]) => {
      setNodes((current) => applyNodeChanges(changes, current))
      for (const change of changes) {
        // Only persist when a gesture finishes: dragging fires continuously and
        // would otherwise flood the daemon with writes.
        if (change.type === 'position' && change.dragging === false && change.position) {
          patch({
            id: Number(change.id),
            x: change.position.x,
            y: change.position.y,
          } as domain.CanvasNodePatch)
        }
        if (change.type === 'dimensions' && change.resizing === false && change.dimensions) {
          patch({
            id: Number(change.id),
            width: change.dimensions.width,
            height: change.dimensions.height,
          } as domain.CanvasNodePatch)
        }
        if (change.type === 'remove') {
          void remove(change.id)
        }
      }
    },
    [patch, remove, setNodes],
  )

  const onConnect = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target) return
      void link(connection.source, connection.target)
    },
    [link],
  )

  const onEdgesChange = useCallback(
    (changes: EdgeChange[]) => {
      for (const change of changes) {
        if (change.type === 'remove') void unlink(change.id)
      }
    },
    [unlink],
  )

  const onDoubleClick = useCallback(
    (event: React.MouseEvent) => {
      // The event always arrives from a React Flow descendant, never from this
      // wrapper, so identity against currentTarget would never match. Creating
      // a note is only correct on the empty pane, not on a node.
      if (!(event.target instanceof Element)) return
      if (!event.target.classList.contains('react-flow__pane')) return
      const point = flow.screenToFlowPosition({ x: event.clientX, y: event.clientY })
      void canvas.addNote({ body: '', color: '', x: point.x - 120, y: point.y - 90 })
    },
    [canvas, flow],
  )

  return (
    <div className="board" onDoubleClick={onDoubleClick}>
      <ReactFlow
        nodes={decorated}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        proOptions={{ hideAttribution: true }}
        minZoom={0.2}
        maxZoom={2}
        defaultViewport={{ x: 0, y: 0, zoom: 0.9 }}
        panOnScroll
        selectionOnDrag
        deleteKeyCode={['Delete', 'Backspace']}
        elevateNodesOnSelect
      >
        <Background variant={BackgroundVariant.Dots} gap={26} size={1.4} color="#232838" />
        <Controls showInteractive={false} position="bottom-right" />
        <MiniMap
          pannable
          zoomable
          position="bottom-left"
          maskColor="rgba(8, 9, 13, 0.72)"
          nodeColor={(node) => (node.type === 'note' ? '#f2c55c' : '#7aa8ff')}
        />
      </ReactFlow>
    </div>
  )
}
