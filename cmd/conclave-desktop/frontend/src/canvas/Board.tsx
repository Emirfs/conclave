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
import { ConfirmPanel, type PendingConfirm } from './ConfirmPanel'
import { ConversationNode } from './ConversationNode'
import { GateNode } from './GateNode'
import { JoinNode } from './JoinNode'
import { LinkPanel } from './LinkPanel'
import { NoteNode } from './NoteNode'
import { PipelineNode } from './PipelineNode'
import { ProviderEdge } from './ProviderEdge'
import { RunPanel } from './RunPanel'
import { SearchPanel } from './SearchPanel'
import { TriggerNode } from './TriggerNode'
import type { BoardNode, useCanvas } from './useCanvas'

const nodeTypes = {
  conversation: ConversationNode,
  note: NoteNode,
  pipeline: PipelineNode,
  join: JoinNode,
  trigger: TriggerNode,
  gate: GateNode,
}
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
    saveRole, saveModel, resumeDialogue, cancelConversation, search,
    savePipeline, runPipeline, pickPipelineProject, exportConversation,
    assignRoles, branch, addNote, error, clearError, deletingIds,
    activeRuns, runs, stopRun, saveTrigger, fireTrigger, saveGate,
    loadRuns, runDetail, reportRun } = canvas
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null)
  const [searching, setSearching] = useState(false)
  // The run history is read on demand: it outlives the cards it is about, and
  // fetching it on every board poll would be a request nobody asked for.
  const [runsOpen, setRunsOpen] = useState(false)
  // While the panel is open the history is refreshed, so a run that starts or
  // ends behind it does not leave a stale list on screen.
  useEffect(() => {
    if (!runsOpen) return
    void loadRuns()
    const timer = window.setInterval(() => void loadRuns(), 2000)
    return () => window.clearInterval(timer)
  }, [runsOpen, loadRuns])
  // A deletion waiting for an answer. Held here rather than asked with
  // window.confirm: see ConfirmPanel for why.
  const [pending, setPending] = useState<PendingConfirm | null>(null)
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

  // Every way of deleting goes through here, so one gesture asks one question
  // however many cards it takes with it. Anything that would lose work is
  // worth asking about: a conversation and its history, a pipeline, or a note
  // with something written in it. An empty note is not.
  const confirmRemoval = useCallback(
    (ids: string[]) => {
      const doomed = ids
        .map((id) => nodes.find((item) => item.id === id))
        .filter((node): node is BoardNode => node !== undefined)
      if (doomed.length === 0) return
      const run = () => void Promise.allSettled(doomed.map((node) => remove(node.id)))
      const losesWork = doomed.some((node) => {
        if (node.data.kind === 'conversation' || node.data.kind === 'pipeline') return true
        if (node.data.kind === 'note') return node.data.body.trim() !== ''
        return false
      })
      if (!losesWork) {
        run()
        return
      }
      if (doomed.length > 1) {
        setPending({
          message: `${doomed.length} kart silinecek, geçmişleriyle birlikte.`,
          action: 'Hepsini sil',
          run,
        })
        return
      }
      const only = doomed[0].data.kind
      setPending({
        message:
          only === 'conversation'
            ? 'Bu konuşma ve tüm geçmişi silinecek.'
            : only === 'pipeline'
              ? 'Bu pipeline silinecek.'
              : 'Bu not silinecek.',
        action:
          only === 'conversation'
            ? 'Konuşmayı sil'
            : only === 'pipeline'
              ? "Pipeline'ı sil"
              : 'Notu sil',
        run,
      })
    },
    [nodes, remove],
  )

  const close = useCallback((id: string) => confirmRemoval([id]), [confirmRemoval])
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
      nodes.map((node) => {
        const deleting = deletingIds.has(node.id)
        if (node.data.kind === 'pipeline') {
          return {
            ...node,
            data: {
              ...node.data,
              onSave: savePipeline,
              onRun: runPipeline,
              onPickProject: pickPipelineProject,
              onClose: close,
              onResize: resize,
              deleting,
            },
          }
        }
        if (node.data.kind === 'gate') {
          return {
            ...node,
            data: { ...node.data, onSave: saveGate, onClose: close, onResize: resize, deleting },
          }
        }
        if (node.data.kind === 'trigger') {
          return {
            ...node,
            data: {
              ...node.data,
              onSave: saveTrigger,
              onFire: fireTrigger,
              onClose: close,
              onResize: resize,
              deleting,
            },
          }
        }
        if (node.data.kind === 'join') {
          return {
            ...node,
            data: { ...node.data, onBodyChange: setNoteBody, onClose: close, deleting },
          }
        }
        return node.data.kind === 'note'
          ? {
              ...node,
              data: {
                ...node.data,
                onBodyChange: setNoteBody,
                onClose: close,
                onResize: resize,
                onSetHeight: setHeight,
                deleting,
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
                onCancel: cancelConversation,
                onExport: exportConversation,
                onBranch: (conversationID: number, answer: string) =>
                  setBranching({ conversationID, answer }),
                onPinNote: (body: string) => void pinNote(node.id, body),
                onResize: resize,
                deleting,
              },
            }
      }),
    [nodes, deletingIds, setNoteBody, send, close, pickProject, setAccess, saveLoop, toggleLoop,
     saveTrigger, fireTrigger, saveGate,
     saveRole, saveModel, resumeDialogue, cancelConversation, savePipeline, runPipeline,
     pickPipelineProject, exportConversation, pinNote, resize, setHeight],
  )

  const onNodesChange = useCallback(
    (changes: NodeChange<BoardNode>[]) => {
      // A Delete keypress arrives as a remove change. It is held back rather
      // than applied, because applying it would take the card off the board
      // before the question about it has been answered.
      const removals: string[] = []
      const rest: NodeChange<BoardNode>[] = []
      for (const change of changes) {
        if (change.type === 'remove') removals.push(change.id)
        else rest.push(change)
      }
      setNodes((current) => applyNodeChanges(rest, current))
      if (removals.length > 0) confirmRemoval(removals)
      for (const change of rest) {
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
      }
    },
    [patch, confirmRemoval, setNodes],
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
      // A gate has two ways out, so which port the line left by is part of
      // what the link means.
      void link(connection.source, connection.target, connection.sourceHandle ?? '')
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

  // Bringing a result into view is the whole point of searching: the card is
  // centred and selected, so it is obvious which one was found.
  const jumpTo = useCallback(
    (nodeID: number) => {
      const node = nodes.find((item) => item.id === String(nodeID))
      if (!node) return
      const width = node.width ?? 420
      const height = node.height ?? 320
      void flow.setCenter(node.position.x + width / 2, node.position.y + height / 2, {
        zoom: 1,
        duration: 400,
      })
      setNodes((current) =>
        current.map((item) => ({ ...item, selected: item.id === String(nodeID) })),
      )
    },
    [flow, nodes, setNodes],
  )

  // Ctrl+F opens the search box, the way every board and document does.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 'f') return
      event.preventDefault()
      setSearching(true)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  // Escape closes whatever is open, which is the first thing anyone tries.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setBranching(null)
      setSearching(false)
      setPending(null)
      closeLinkPanel()
      clearError()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [closeLinkPanel, clearError])

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

  const closeSelected = useCallback(
    () => confirmRemoval(selected.map((node) => node.id)),
    [confirmRemoval, selected],
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
        // Backspace is left out on purpose: it is a typing key. Delete stays,
        // but now arrives as a question rather than as a deletion.
        deleteKeyCode={['Delete']}
        elevateNodesOnSelect
      >
        {activeRuns.length > 0 && !runsOpen && (
          <Panel position="bottom-center" className="runpanel">
            {activeRuns.map((run) => (
              <div className="runpanel__run" key={run.id}>
                <span className="runpanel__dot" aria-hidden="true" />
                <button
                  className="runpanel__text"
                  onClick={() => setRunsOpen(true)}
                  title="Akış geçmişini aç"
                >
                  {run.origin_label || `akış #${run.id}`} · {run.steps} adım
                </button>
                <button
                  className="runpanel__stop"
                  onClick={() => void stopRun(run.id)}
                  title="Bu akıştaki her kartı durdur"
                >
                  durdur
                </button>
              </div>
            ))}
          </Panel>
        )}
        {pending && (
          <Panel position="top-center" className="confirmpanel">
            <ConfirmPanel pending={pending} onCancel={() => setPending(null)} />
          </Panel>
        )}
        {error && (
          <Panel position="top-center" className="board-error" role="alert" aria-live="assertive">
            <span className="board-error__icon" aria-hidden="true">
              ⚠
            </span>
            <span className="board-error__text">{error}</span>
            <button
              className="board-error__close"
              onClick={clearError}
              aria-label="Hatayı kapat"
              title="Hatayı kapat"
            >
              ✕
            </button>
          </Panel>
        )}
        <Panel position="top-left" className="boardpanel">
          {searching && (
            <SearchPanel
              onSearch={search}
              onJump={(nodeID) => {
                jumpTo(nodeID)
                setSearching(false)
              }}
              onClose={() => setSearching(false)}
            />
          )}
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
        {/* Ctrl+F is the shortcut, but a board is a pointing surface: the
            search has to be visible to someone who never learns one. */}
        {!searching && (
          <Panel position="top-right" className="boardpanel">
            <button
              className="boardpanel__action"
              onClick={() => setSearching(true)}
              title="Panoda ara (Ctrl+F)"
            >
              ara
            </button>
            <button
              className="boardpanel__action"
              onClick={() => setRunsOpen((open) => !open)}
              title="Panonun geçtiği akışlar"
            >
              {runsOpen ? 'akışlar ✕' : 'akışlar'}
            </button>
          </Panel>
        )}
        {runsOpen && (
          <Panel position="bottom-center" className="runspanel">
            <RunPanel
              runs={runs}
              onStop={stopRun}
              onDetail={runDetail}
              onReport={reportRun}
              onJump={(conversationID) => {
                const node = nodes.find(
                  (item) =>
                    item.data.kind === 'conversation' &&
                    item.data.conversation.id === conversationID,
                )
                if (node) jumpTo(Number(node.id))
              }}
              onClose={() => setRunsOpen(false)}
            />
          </Panel>
        )}
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
