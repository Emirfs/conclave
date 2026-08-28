import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Edge, Node } from '@xyflow/react'

import {
  Branch,
  CancelConversation,
  Canvas as LoadCanvas,
  CreateConversation,
  CreateJoin,
  CreateNote,
  CreatePipeline,
  ExportBoard,
  ExportConversation,
  ImportBoard,
  DeleteCanvasNode,
  LinkNodes,
  PairNodes,
  PatchCanvasNode,
  PickProjectDirectory,
  ResumeDialogue,
  Search,
  SetPipeline,
  SetProject,
  SendTurn,
  SetLoop,
  SetLoopRunning,
  SetModel,
  SetRole,
  StartPipeline,
  StopFlowRun,
  UnlinkNodes,
  UpdateLink,
} from '../../wailsjs/go/main/App'
import { domain } from '../../wailsjs/go/models'
import { providerStyle } from '../providers'

export type ConversationNodeData = {
  kind: 'conversation'
  conversation: domain.Conversation
}

export type NoteNodeData = {
  kind: 'note'
  body: string
  color: string
}

export type PipelineNodeData = {
  kind: 'pipeline'
  pipeline: domain.Pipeline
}

export type JoinNodeData = {
  kind: 'join'
  /** The join's name, which is also the node's body text. */
  body: string
  /** How many lines feed it and which of them have spoken. Absent only in the
   *  moment between a join being created and the next board load. */
  join?: domain.JoinNode
}

export type BoardNode = Node<
  ConversationNodeData | NoteNodeData | PipelineNodeData | JoinNodeData
>

/** How long a drag or keystroke settles before it is written to the daemon. */
const PATCH_DEBOUNCE = 350

/** Refetch cadence. Answers are written to the daemon as they are produced, so
 *  the board polls quickly while a provider is mid-answer and backs off when
 *  everything is settled. */
const REFRESH_ACTIVE = 400
const REFRESH_IDLE = 1500

function toBoardNode(
  node: domain.CanvasNode,
  conversations: Map<number, domain.Conversation>,
  pipelines: Map<number, domain.Pipeline>,
  joins: Map<number, domain.JoinNode>,
): BoardNode | null {
  const shared = {
    id: String(node.id),
    position: { x: node.x, y: node.y },
    width: node.width,
    height: node.height,
    zIndex: node.z,
    // Dragging only from the grip. Node bodies hold selectable transcripts and
    // editable text, where a press must mean "select", not "move the card".
    dragHandle: '.node__grip',
  }
  if (node.kind === 'note') {
    return {
      ...shared,
      type: 'note',
      data: { kind: 'note', body: node.body ?? '', color: node.color ?? '' },
    }
  }
  if (node.kind === 'join') {
    return {
      ...shared,
      type: 'join',
      data: { kind: 'join', body: node.body ?? '', join: joins.get(node.id) },
    }
  }
  if (node.kind === 'pipeline') {
    const pipeline = node.pipeline_id ? pipelines.get(node.pipeline_id) : undefined
    // Same rule as a conversation node: a card with no record behind it is
    // corrupt state, not something to draw an empty frame for.
    if (!pipeline) return null
    return { ...shared, type: 'pipeline', data: { kind: 'pipeline', pipeline } }
  }
  const conversation = node.conversation_id ? conversations.get(node.conversation_id) : undefined
  // A conversation node without its conversation is corrupt state; skip it
  // rather than rendering an empty frame the user cannot act on.
  if (!conversation) return null
  return { ...shared, type: 'conversation', data: { kind: 'conversation', conversation } }
}

/** Link modes as the user sees them on the board. */
export const LINK_MODES: Record<string, string> = {
  relay: 'aktar',
  dialogue: 'karşılıklı',
  review: 'incele',
}

function linkLabel(link: domain.CanvasLink): string {
  return link.until_done
    ? `${LINK_MODES[link.mode] ?? link.mode} · bitene kadar`
    : `${LINK_MODES[link.mode] ?? link.mode} · ${link.max_rounds}`
}

export function useCanvas(connected: boolean) {
  const [nodes, setNodes] = useState<BoardNode[]>([])
  const [edges, setEdges] = useState<Edge[]>([])
  const [error, setError] = useState<string | null>(null)
  const [deletingIds, setDeletingIds] = useState<Set<string>>(new Set())
  const deletingRef = useRef(new Set<string>())
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [runs, setRuns] = useState<domain.FlowRun[]>([])
  const timers = useRef(new Map<string, number>())

  const clearError = useCallback(() => setError(null), [])

  const load = useCallback(async () => {
    try {
      const canvas = await LoadCanvas()
      let working = false
      const pipelines = new Map<number, domain.Pipeline>()
      for (const item of canvas.pipelines ?? []) {
        pipelines.set(item.id, item)
        // A queued or running pipeline is work in progress too, so the board
        // polls at the fast cadence while one is going.
        for (const run of item.runs ?? []) {
          if (run.status === 'queued' || run.status === 'running') working = true
        }
      }
      const joins = new Map<number, domain.JoinNode>()
      for (const item of canvas.joins ?? []) joins.set(item.node_id, item)
      // A run still in flight is work in progress: something on the board is
      // about to move even if no provider is mid-answer this instant.
      if ((canvas.runs ?? []).length > 0) working = true
      setRuns(canvas.runs ?? [])
      const conversations = new Map<number, domain.Conversation>()
      for (const item of canvas.conversations ?? []) {
        conversations.set(item.id, item)
        for (const turn of item.turns ?? []) {
          for (const response of turn.responses ?? []) {
            if (response.status === 'queued' || response.status === 'running') working = true
          }
        }
      }
      setBusy(working)
      // A link into a note is the line from a pair of cards to the result they
      // produced. Nothing is relayed along it, so it is drawn as a quiet dashed
      // line rather than a working connection.
      const noteNodes = new Set(
        (canvas.nodes ?? []).filter((node) => node.kind === 'note').map((node) => String(node.id)),
      )
      // The colour a card is drawn in is its lead provider's. Carrying those
      // two colours into the line is what makes the board say who is talking to
      // whom without a label on every edge.
      const accents = new Map<string, string>()
      const producing = new Set<string>()
      for (const node of canvas.nodes ?? []) {
        if (node.conversation_id === undefined) continue
        const conversation = conversations.get(node.conversation_id)
        if (!conversation) continue
        accents.set(String(node.id), providerStyle(conversation.providers?.[0] ?? '').accent)
        for (const turn of conversation.turns ?? []) {
          for (const response of turn.responses ?? []) {
            if (response.status === 'queued' || response.status === 'running') {
              producing.add(String(node.id))
            }
          }
        }
      }
      const mappedEdges = (canvas.links ?? []).map((link) => {
          const toNote = noteNodes.has(String(link.target_id))
          return {
            id: String(link.id),
            source: String(link.source_id),
            target: String(link.target_id),
            // React Flow's own animation runs on every edge it is set on.
            // Only the one edge whose target is actually producing an answer
            // moves, and it does so in CSS.
            animated: false,
            type: 'provider',
            style: undefined,
            data: {
              mode: link.mode,
              maxRounds: link.max_rounds,
              untilDone: link.until_done,
              briefing: link.briefing ?? '',
              toNote,
              label: toNote ? undefined : linkLabel(link),
              sourceAccent: accents.get(String(link.source_id)),
              targetAccent: accents.get(String(link.target_id)),
              active: !toNote && producing.has(String(link.target_id)),
            },
          }
        })
      setEdges((current) => {
        if (
          current.length === mappedEdges.length &&
          mappedEdges.every((edge, index) => {
            const existing = current[index]
            return existing.id === edge.id && existing.source === edge.source &&
              existing.target === edge.target &&
              existing.data?.label === edge.data.label &&
              existing.data?.briefing === edge.data.briefing &&
              // A link that starts or stops carrying an answer has to be
              // redrawn, or the board never shows the work moving.
              existing.data?.active === edge.data.active
          })
        ) {
          return current
        }
        const local = new Map(current.map((edge) => [edge.id, edge]))
        return mappedEdges.map((edge) => ({ ...edge, selected: local.get(edge.id)?.selected }))
      })
      const mapped = (canvas.nodes ?? [])
        .map((node) => toBoardNode(node, conversations, pipelines, joins))
        .filter((node): node is BoardNode => node !== null)
      setNodes((current) => {
        const local = new Map(current.map((node) => [node.id, node]))
        const next = mapped.map((node) => {
          const existing = local.get(node.id)
          if (!existing) return node
          if (node.data.kind === 'note' && existing.data.kind === 'note') {
            // Note edits are local-first and debounced. Reusing the whole node
            // also keeps an idle board from rendering on every poll.
            return existing
          }
          if (
            node.data.kind === 'conversation' &&
            existing.data.kind === 'conversation' &&
            JSON.stringify(node.data.conversation) === JSON.stringify(existing.data.conversation)
          ) {
            return existing
          }
          // Server data wins for content, local state wins for geometry: a
          // refresh landing mid-drag must not yank the node back.
          return {
            ...node,
            position: existing.position,
            width: existing.width,
            height: existing.height,
            selected: existing.selected,
            dragging: existing.dragging,
            data: node.data,
          }
        })
        return next.length === current.length && next.every((node, index) => node === current[index])
          ? current
          : next
      })
      setLoaded(true)
    } catch (cause) {
      setError(`Pano yüklenemedi: ${cause}`)
    }
  }, [])

  useEffect(() => {
    if (!connected) return
    void load()
    const timer = window.setInterval(() => void load(), busy ? REFRESH_ACTIVE : REFRESH_IDLE)
    return () => window.clearInterval(timer)
  }, [connected, load, busy])

  // Clear pending writes on unmount so a timer cannot fire into a dead tree.
  useEffect(() => {
    const pending = timers.current
    return () => {
      for (const timer of pending.values()) window.clearTimeout(timer)
      pending.clear()
    }
  }, [])

  const patch = useCallback((patchInput: domain.CanvasNodePatch) => {
    const key = String(patchInput.id)
    const existing = timers.current.get(key)
    if (existing) window.clearTimeout(existing)
    timers.current.set(
      key,
      window.setTimeout(() => {
        timers.current.delete(key)
        PatchCanvasNode(patchInput).catch((cause) => setError(`Düğüm güncellenemedi: ${cause}`))
      }, PATCH_DEBOUNCE),
    )
  }, [])

  const addConversation = useCallback(
    async (input: domain.NewConversation) => {
      try {
        await CreateConversation(input)
      } catch (cause) {
        setError(`Konuşma oluşturulamadı: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const addNote = useCallback(
    async (input: domain.NewNote) => {
      try {
        await CreateNote(input)
      } catch (cause) {
        setError(`Not oluşturulamadı: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  // A join is created empty and named on the card, the way a note is.
  const addJoin = useCallback(
    async (input: domain.NewNote) => {
      try {
        await CreateJoin(input)
      } catch (cause) {
        setError(`Birleştirici oluşturulamadı: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  // Stopping a run stops every card still working on it. Catching each card in
  // turn is what this replaces.
  const stopRun = useCallback(
    async (runID: number) => {
      try {
        await StopFlowRun(runID)
      } catch (cause) {
        setError(`Akış durdurulamadı: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const remove = useCallback(async (id: string) => {
    if (deletingRef.current.has(id)) return
    deletingRef.current.add(id)
    setDeletingIds(new Set(deletingRef.current))
    const numeric = Number(id)
    const pending = timers.current.get(id)
    if (pending) {
      window.clearTimeout(pending)
      timers.current.delete(id)
    }
    try {
      await DeleteCanvasNode(numeric)
      setNodes((current) => current.filter((node) => node.id !== id))
    } catch (cause) {
      setError(`Kart silinemedi: ${cause}`)
    } finally {
      deletingRef.current.delete(id)
      setDeletingIds(new Set(deletingRef.current))
    }
  }, [])

  const setNoteBody = useCallback(
    (id: string, body: string) => {
      setNodes((current) =>
        current.map((node) =>
          node.id === id && node.data.kind === 'note'
            ? { ...node, data: { ...node.data, body } }
            : node,
        ),
      )
      patch({ id: Number(id), body } as domain.CanvasNodePatch)
    },
    [patch],
  )

  const link = useCallback(
    async (sourceID: string, targetID: string) => {
      try {
        await LinkNodes(Number(sourceID), Number(targetID))
      } catch (cause) {
        setError(`Kartlar bağlanamadı: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const pair = useCallback(
    async (firstID: string, secondID: string, mode: string, rounds: number, briefing: string) => {
      try {
        await PairNodes(Number(firstID), Number(secondID), mode, rounds, briefing)
      } catch (cause) {
        setError(`Kartlar eşleştirilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const configureLink = useCallback(
    async (id: string, mode: string, rounds: number, untilDone: boolean, briefing: string) => {
      try {
        await UpdateLink(Number(id), mode, rounds, untilDone, briefing)
      } catch (cause) {
        setError(`Bağlantı güncellenemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  // Changing the model drops that provider's session, so the answer to the next
  // message comes from the model the card now says it runs on.
  const saveModel = useCallback(
    async (conversationID: number, providerName: string, model: string) => {
      try {
        await SetModel(conversationID, providerName, model)
      } catch (cause) {
        setError(`Model kaydedilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const saveRole = useCallback(
    async (conversationID: number, role: string) => {
      try {
        await SetRole(conversationID, role)
      } catch (cause) {
        setError(`Rol kaydedilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  // Forking an answer into new cards. The daemon places them and draws the link
  // back, so the board shows where a line of work split.
  const branch = useCallback(
    async (conversationID: number, answer: string, providers: string[]) => {
      try {
        await Branch(conversationID, answer, providers)
      } catch (cause) {
        setError(`Dallanma oluşturulamadı: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  // Roles come in complementary pairs, so both cards of a link are assigned
  // together. Node ids are what a link carries; the conversation behind each
  // one is what carries the role.
  const assignRoles = useCallback(
    async (sourceNodeID: string, targetNodeID: string, sourceRole: string, targetRole: string) => {
      const conversationOf = (nodeID: string) => {
        const node = nodes.find((item) => item.id === nodeID)
        return node?.data.kind === 'conversation' ? node.data.conversation.id : null
      }
      const source = conversationOf(sourceNodeID)
      const target = conversationOf(targetNodeID)
      if (source === null || target === null) return
      try {
        await SetRole(source, sourceRole)
        await SetRole(target, targetRole)
      } catch (cause) {
        setError(`Roller atanamadı: ${cause}`)
        return
      }
      await load()
    },
    [nodes, load],
  )

  // A pipeline card is created empty; its stages and project are filled in on
  // the card itself, the way a conversation card picks its project.
  const addPipeline = useCallback(
    async (input: domain.NewPipeline) => {
      try {
        await CreatePipeline(input)
      } catch (cause) {
        setError(`Pipeline oluşturulamadı: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const savePipeline = useCallback(
    async (pipelineID: number, config: domain.PipelineConfig) => {
      try {
        await SetPipeline(pipelineID, config)
      } catch (cause) {
        setError(`Pipeline kaydedilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  // Queueing is all the board does: the daemon claims the run and works
  // through the stages, exactly as it does for one queued from a terminal.
  const runPipeline = useCallback(
    async (pipelineID: number) => {
      try {
        await StartPipeline(pipelineID)
      } catch (cause) {
        setError(`Pipeline başlatılamadı: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const pickPipelineProject = useCallback(
    async (
      pipelineID: number,
      current: string,
      draft?: { title: string; stages: domain.PipelineStage[] },
    ) => {
      let chosen = ''
      try {
        chosen = await PickProjectDirectory(current)
      } catch (cause) {
        setError(`Dizin seçilemedi: ${cause}`)
        return
      }
      if (!chosen) return
      // The project is one field of a whole-pipeline write, so the rest of
      // the definition has to be carried over from what is on the board or in the local draft.
      const node = nodes.find(
        (item) => item.data.kind === 'pipeline' && item.data.pipeline.id === pipelineID,
      )
      const pipeline = node?.data.kind === 'pipeline' ? node.data.pipeline : undefined
      const title = draft?.title ?? pipeline?.title ?? 'Pipeline'
      const stages = draft?.stages ?? pipeline?.stages ?? []
      try {
        await SetPipeline(pipelineID, {
          title,
          project_path: chosen,
          stages,
        } as domain.PipelineConfig)
      } catch (cause) {
        setError(`Pipeline projesi seçilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load, nodes],
  )

  // Exporting a card writes a Markdown file the user picks; a cancelled dialog
  // returns an empty path and is not an error.
  const exportConversation = useCallback(
    async (conversationID: number, title: string) => {
      try {
        return await ExportConversation(conversationID, title)
      } catch (cause) {
        setError(`Konuşma dışa aktarılamadı: ${cause}`)
        return ''
      }
    },
    [],
  )

  const exportBoard = useCallback(async () => {
    try {
      return await ExportBoard()
    } catch (cause) {
      setError(`Pano dışa aktarılamadı: ${cause}`)
      return ''
    }
  }, [])

  // An import is additive: the file's cards arrive next to what is already on
  // the board, so the reload afterwards is what makes them appear.
  const importBoard = useCallback(async () => {
    try {
      const result = await ImportBoard()
      await load()
      return result
    } catch (cause) {
      setError(`Pano içe aktarılamadı: ${cause}`)
      return undefined
    }
  }, [load])

  // Searching is a read the daemon answers; the board only asks and renders.
  // It deliberately does not touch canvas state: a search must not move, select
  // or reload anything until the user picks a result.
  const search = useCallback(async (query: string, limit: number) => {
    try {
      return (await Search(query, limit)) ?? []
    } catch (cause) {
      setError(`Arama yapılamadı: ${cause}`)
      return []
    }
  }, [])

  // Stopping is written to the daemon, which owns the provider process; the
  // board only asks. The reload right after is what turns the card's spinner
  // into a stopped reply without waiting for the next poll.
  const cancelConversation = useCallback(
    async (conversationID: number) => {
      try {
        await CancelConversation(conversationID)
      } catch (cause) {
        setError(`Konuşma durdurulamadı: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  // Clearing the parked state is what lets a stalled pair be pushed on: the
  // next message starts the exchange again instead of being swallowed.
  const resumeDialogue = useCallback(
    async (conversationID: number) => {
      try {
        await ResumeDialogue(conversationID)
      } catch (cause) {
        setError(`Konuşma devam ettirilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const saveLoop = useCallback(
    async (conversationID: number, config: domain.LoopConfig) => {
      try {
        await SetLoop(conversationID, config)
      } catch (cause) {
        setError(`Döngü kaydedilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const toggleLoop = useCallback(
    async (conversationID: number, running: boolean) => {
      try {
        await SetLoopRunning(conversationID, running)
      } catch (cause) {
        setError(`Döngü durumu değiştirilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const unlink = useCallback(
    async (id: string) => {
      setEdges((current) => current.filter((edge) => edge.id !== id))
      try {
        await UnlinkNodes(Number(id))
      } catch (cause) {
        setError(`Bağlantı kaldırılamadı: ${cause}`)
      }
    },
    [],
  )

  const pickProject = useCallback(
    async (conversationID: number, current: string) => {
      let chosen = ''
      try {
        chosen = await PickProjectDirectory(current)
      } catch (cause) {
        setError(`Dizin seçilemedi: ${cause}`)
        return
      }
      // An empty result means the dialog was cancelled; leave the card alone.
      if (!chosen) return
      try {
        await SetProject(conversationID, chosen, 'edit')
      } catch (cause) {
        setError(`Proje seçilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const setAccess = useCallback(
    async (conversationID: number, project: string, access: string) => {
      try {
        await SetProject(conversationID, project, access)
      } catch (cause) {
        setError(`Proje erişimi değiştirilemedi: ${cause}`)
        return
      }
      await load()
    },
    [load],
  )

  const send = useCallback(
    async (conversationID: number, prompt: string) => {
      try {
        await SendTurn(conversationID, prompt)
      } catch (cause) {
        setError(`Mesaj gönderilemedi: ${cause}`)
        throw cause
      }
      await load()
    },
    [load],
  )

  return useMemo(
    () => ({
      nodes, setNodes, edges, error, clearError, deletingIds, loaded, load, patch,
      runs, addJoin, stopRun,
      addConversation, addNote, remove, setNoteBody, send, link, unlink,
      pickProject, setAccess, pair, configureLink, saveLoop, toggleLoop,
      addPipeline, savePipeline, runPipeline, pickPipelineProject,
      exportConversation, exportBoard, importBoard,
      saveRole, saveModel, resumeDialogue, cancelConversation, search, assignRoles, branch,
    }),
    [nodes, edges, error, clearError, deletingIds, loaded, load, patch, runs, addJoin, stopRun,
     addConversation, addNote, remove,
     setNoteBody, send, link, unlink, pickProject, setAccess, pair, configureLink,
     saveLoop, toggleLoop, saveRole, saveModel, resumeDialogue, cancelConversation, search,
     addPipeline, savePipeline, runPipeline, pickPipelineProject,
     exportConversation, exportBoard, importBoard, assignRoles, branch],
  )
}
