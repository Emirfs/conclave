import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  Panel,
  ReactFlowProvider,
  SelectionMode,
  applyNodeChanges,
  useReactFlow,
  type Connection,
  type EdgeChange,
  type NodeChange,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'

import { domain } from '../../wailsjs/go/models'
import { BranchPanel } from './BranchPanel'
import { ConversationNode } from './ConversationNode'
import { LinkPanel } from './LinkPanel'
import { NoteNode } from './NoteNode'
import { ProviderEdge } from './ProviderEdge'
import type { BoardNode, useCanvas } from './useCanvas'

const nodeTypes = { conversation: ConversationNode, note: NoteNode }
const edgeTypes = { provider: ProviderEdge }

export type BoardHandle = ReturnType<typeof useCanvas>

export function Board({ canvas, providers }: { canvas: BoardHandle; providers: string[] }) {
  return (
    <ReactFlowProvider>
      <BoardSurface canvas={canvas} providers={providers} />
    </ReactFlowProvider>
  )
}

function BoardSurface({ canvas, providers }: { canvas: BoardHandle; providers: string[] }) {
  const { nodes, setNodes, edges, patch, remove, setNoteBody, send, link, unlink,
    pickProject, setAccess, pair, configureLink, saveLoop, toggleLoop,
    saveRole, saveModel, resumeDialogue, assignRoles, branch, addNote } = canvas
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null)
  // The answer a branch would start from, held while the user picks providers.
  const [branching, setBranching] = useState<{ conversationID: number; answer: string } | null>(null)
  // The minimap earns its corner on a large board and wastes it on a small one,
  // so the choice is the user's and it is remembered.
  const [map, setMap] = useState(() => {
    try {
      return localStorage.getItem('conclave.minimap') !== 'off'
    } catch {
      return true
    }
  })
  useEffect(() => {
    try {
      localStorage.setItem('conclave.minimap', map ? 'on' : 'off')
    } catch {
      // A browser that refuses storage still gets a working toggle.
    }
  }, [map])

  // Closing removes the node and, for a conversation, its history with it.
  const close = useCallback((id: string) => void remove(id), [remove])
  const flow = useReactFlow()

  const resize = useCallback(
    (id: string, direction: -1 | 1) => {
      setNodes((current) =>
        current.map((node) => {
          if (node.id !== id) return node
          const conversation = node.data.kind === 'conversation'
          const minWidth = conversation ? 300 : 240
          const minHeight = conversation ? 240 : 120
          // A step small enough to need six clicks is a step that gets used
          // once and then abandoned for dragging a corner.
          const width = Math.max(minWidth, Math.min(1200, (node.width ?? (conversation ? 420 : 240)) + direction * 140))
          const height = Math.max(minHeight, Math.min(900, (node.height ?? (conversation ? 520 : 180)) + direction * 110))
          patch({ id: Number(id), width, height } as domain.CanvasNodePatch)
          return { ...node, width, height }
        }),
      )
    },
    [patch, setNodes],
  )

  // Fitting a note to its own text, which is the size it almost always wants.
  const setHeight = useCallback(
    (id: string, height: number) => {
      setNodes((current) =>
        current.map((node) => {
          if (node.id !== id) return node
          patch({ id: Number(id), height } as domain.CanvasNodePatch)
          return { ...node, height }
        }),
      )
    },
    [patch, setNodes],
  )

  // Pinning puts text on the board beside the card it came from, so a diff or
  // an answer can be read next to the conversation instead of inside it.
  const pinNote = useCallback(
    async (nodeID: string, body: string) => {
      const source = nodes.find((node) => node.id === nodeID)
      const x = (source?.position.x ?? 0) + (source?.width ?? 420) + 40
      const y = source?.position.y ?? 0
      await addNote({ body, color: '', x, y } as domain.NewNote)
    },
    [nodes, addNote],
  )

  // Notes need a callback in their data so the textarea can report edits, but
  // the hook owns the state. Injecting it here keeps NoteNode presentational.
  const decorated = useMemo(
    () =>
      nodes.map((node) =>
        node.data.kind === 'note'
          ? {
              ...node,
              data: {
                ...node.data,
                onBodyChange: setNoteBody,
                onClose: close,
                onResize: resize,
                onSetHeight: setHeight,
              },
            }
          : {
              ...node,
              data: {
                ...node.data,
                onSend: send,
                onClose: close,
                onPickProject: pickProject,
                onToggleAccess: setAccess,
                onSaveLoop: saveLoop,
                onToggleLoop: toggleLoop,
                onSaveRole: saveRole,
                onSaveModel: saveModel,
                onResumeDialogue: resumeDialogue,
                onBranch: (conversationID: number, answer: string) =>
                  setBranching({ conversationID, answer }),
                onPinNote: (body: string) => void pinNote(node.id, body),
                onResize: resize,
              },
            },
      ),
    [nodes, setNoteBody, send, close, pickProject, setAccess, saveLoop, toggleLoop,
     saveRole, saveModel, resumeDialogue, pinNote, resize, setHeight],
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

  const selected = useMemo(() => nodes.filter((node) => node.selected), [nodes])

  // Selecting exactly two cards offers pairing, which links them both ways.
  const selectedCards = useMemo(
    () => selected.filter((node) => node.data.kind === 'conversation'),
    [selected],
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
        if (change.type === 'select') {
          setSelectedEdge(change.selected ? change.id : null)
        }
      }
    },
    [unlink],
  )

  // Closing the panel has to clear the edge's own selected flag too, or the
  // edge stays selected and clicking it again produces no change to react to —
  // leaving no way to reopen it.
  const closeLinkPanel = useCallback(() => {
    setSelectedEdge(null)
    flow.setEdges((current) =>
      current.map((edge) => (edge.selected ? { ...edge, selected: false } : edge)),
    )
  }, [flow])

  // Escape closes whatever is open, which is the first thing anyone tries.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setBranching(null)
      closeLinkPanel()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [closeLinkPanel])

  // Ctrl+A selects every card and note, the way a canvas is expected to behave.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 'a') return
      const target = event.target as HTMLElement | null
      // Not while the user is typing in a card.
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA')) return
      event.preventDefault()
      setNodes((current) => current.map((node) => ({ ...node, selected: true })))
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [setNodes])

  const clearSelection = useCallback(
    () => setNodes((current) => current.map((node) => ({ ...node, selected: false }))),
    [setNodes],
  )

  // Tidy the selection into a row so a bulk move lands somewhere readable.
  const arrange = useCallback(() => {
    const ordered = [...selected].sort((left, right) => left.position.x - right.position.x)
    if (ordered.length < 2) return
    const originX = ordered[0].position.x
    const originY = ordered[0].position.y
    let cursor = originX
    setNodes((current) =>
      current.map((node) => {
        const index = ordered.findIndex((item) => item.id === node.id)
        if (index === -1) return node
        const position = { x: cursor, y: originY }
        cursor += (node.width ?? 420) + 32
        patch({ id: Number(node.id), x: position.x, y: position.y } as domain.CanvasNodePatch)
        return { ...node, position }
      }),
    )
  }, [patch, selected, setNodes])

  const closeSelected = useCallback(() => {
    for (const node of selected) void remove(node.id)
  }, [remove, selected])

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
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onPaneClick={closeLinkPanel}
        proOptions={{ hideAttribution: true }}
        minZoom={0.2}
        maxZoom={2}
        // A generous snap radius: the ports are small targets, and a link
        // should land when the pointer is merely near one.
        connectionRadius={70}
        connectOnClick
        defaultViewport={{ x: 0, y: 0, zoom: 0.9 }}
        panOnScroll
        selectionOnDrag
        selectionMode={SelectionMode.Partial}
        // Left drag draws a selection box; panning moves to the middle and
        // right buttons. With the default panOnDrag the box never appears.
        panOnDrag={[1, 2]}
        multiSelectionKeyCode={['Control', 'Meta']}
        selectionKeyCode="Shift"
        // Backspace is left out on purpose: it is a typing key, and deleting a
        // whole selection by mistake is not recoverable.
        deleteKeyCode={['Delete']}
        elevateNodesOnSelect
      >
        <Panel position="top-left" className="boardpanel">
          {selected.length > 1 && (
            <div className="selectionbar">
              <span className="selectionbar__count">{selected.length} seçili</span>
              {selectedCards.length === 2 && (
                <>
                  <button
                    className="boardpanel__action"
                    onClick={() => void pair(selectedCards[0].id, selectedCards[1].id, 'dialogue', 3, '')}
                    title="İki kart birbirine cevap verir"
                  >
                    karşılıklı bağla
                  </button>
                  <button
                    className="boardpanel__action"
                    onClick={() => void link(selectedCards[0].id, selectedCards[1].id)}
                    title="Soldaki kartın cevabı sağdakine aktarılır"
                  >
                    tek yönlü bağla
                  </button>
                </>
              )}
              <button className="boardpanel__action" onClick={arrange}>
                yan yana diz
              </button>
              <button className="boardpanel__action selectionbar__close" onClick={closeSelected}>
                kapat
              </button>
              <button className="boardpanel__action" onClick={clearSelection}>
                seçimi bırak
              </button>
            </div>
          )}
          {selectedEdge && (
            <LinkPanel
              edge={edges.find((edge) => edge.id === selectedEdge) ?? { id: selectedEdge } as never}
              onConfigure={configureLink}
              onAssignRoles={assignRoles}
              onUnlink={(id) => {
                closeLinkPanel()
                void unlink(id)
              }}
              onClose={closeLinkPanel}
            />
          )}
          {branching && (
            <BranchPanel
              providers={providers}
              answer={branching.answer}
              onBranch={(chosen) => {
                void branch(branching.conversationID, branching.answer, chosen)
                setBranching(null)
              }}
              onCancel={() => setBranching(null)}
            />
          )}
        </Panel>
        <Background variant={BackgroundVariant.Dots} gap={26} size={1.4} color="#232838" />
        <Controls showInteractive={false} position="bottom-right" />
        {/* MiniMap positions itself; wrapping it in a Panel only fights that.
            The toggle is its own panel, lifted clear of the map when shown. */}
        {map && (
          <MiniMap
            pannable
            zoomable
            className="mappanel__map"
            position="bottom-left"
            maskColor="rgba(8, 9, 13, 0.72)"
            nodeColor={(node) => (node.type === 'note' ? '#f2c55c' : '#7aa8ff')}
          />
        )}
        <Panel position="bottom-left" className={`mappanel${map ? ' mappanel--raised' : ''}`}>
          <button
            className="mappanel__toggle"
            onClick={() => setMap(!map)}
            title={map ? 'Haritayı gizle' : 'Haritayı göster'}
          >
            {map ? 'harita ✕' : 'harita'}
          </button>
        </Panel>
      </ReactFlow>
    </div>
  )
}
