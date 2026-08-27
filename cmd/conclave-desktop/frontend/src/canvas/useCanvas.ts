import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Edge, Node } from '@xyflow/react'

import {
  Branch,
  Canvas as LoadCanvas,
  CreateConversation,
  CreateNote,
  DeleteCanvasNode,
  LinkNodes,
  PairNodes,
  PatchCanvasNode,
  PickProjectDirectory,
  ResumeDialogue,
  SetProject,
  SendTurn,
  SetLoop,
  SetLoopRunning,
  SetRole,
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

export type BoardNode = Node<ConversationNodeData | NoteNodeData>

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
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState(false)
  const timers = useRef(new Map<string, number>())

  const load = useCallback(async () => {
    try {
      const canvas = await LoadCanvas()
      const conversations = new Map<number, domain.Conversation>()
      let working = false
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
        .map((node) => toBoardNode(node, conversations))
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
      setError(null)
      setLoaded(true)
    } catch (cause) {
      setError(String(cause))
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
        PatchCanvasNode(patchInput).catch((cause) => setError(String(cause)))
      }, PATCH_DEBOUNCE),
    )
  }, [])

  const addConversation = useCallback(
    async (input: domain.NewConversation) => {
      try {
        await CreateConversation(input)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const addNote = useCallback(
    async (input: domain.NewNote) => {
      try {
        await CreateNote(input)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const remove = useCallback(async (id: string) => {
    const numeric = Number(id)
    // Drop it locally first so the board feels immediate, then confirm.
    setNodes((current) => current.filter((node) => node.id !== id))
    const pending = timers.current.get(id)
    if (pending) {
      window.clearTimeout(pending)
      timers.current.delete(id)
    }
    try {
      await DeleteCanvasNode(numeric)
    } catch (cause) {
      setError(String(cause))
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
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const pair = useCallback(
    async (firstID: string, secondID: string, mode: string, rounds: number, briefing: string) => {
      try {
        await PairNodes(Number(firstID), Number(secondID), mode, rounds, briefing)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const configureLink = useCallback(
    async (id: string, mode: string, rounds: number, untilDone: boolean, briefing: string) => {
      try {
        await UpdateLink(Number(id), mode, rounds, untilDone, briefing)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const saveRole = useCallback(
    async (conversationID: number, role: string) => {
      try {
        await SetRole(conversationID, role)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  // Forking an answer into new cards. The daemon places them and draws the link
  // back, so the board shows where a line of work split.
  const branch = useCallback(
    async (conversationID: number, answer: string, providers: string[]) => {
      try {
        await Branch(conversationID, answer, providers)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
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
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [nodes, load],
  )

  // Clearing the parked state is what lets a stalled pair be pushed on: the
  // next message starts the exchange again instead of being swallowed.
  const resumeDialogue = useCallback(
    async (conversationID: number) => {
      try {
        await ResumeDialogue(conversationID)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const saveLoop = useCallback(
    async (conversationID: number, config: domain.LoopConfig) => {
      try {
        await SetLoop(conversationID, config)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const toggleLoop = useCallback(
    async (conversationID: number, running: boolean) => {
      try {
        await SetLoopRunning(conversationID, running)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const unlink = useCallback(
    async (id: string) => {
      setEdges((current) => current.filter((edge) => edge.id !== id))
      try {
        await UnlinkNodes(Number(id))
      } catch (cause) {
        setError(String(cause))
      }
    },
    [],
  )

  const pickProject = useCallback(
    async (conversationID: number, current: string) => {
      try {
        const chosen = await PickProjectDirectory(current)
        // An empty result means the dialog was cancelled; leave the card alone.
        if (!chosen) return
        await SetProject(conversationID, chosen, 'edit')
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const setAccess = useCallback(
    async (conversationID: number, project: string, access: string) => {
      try {
        await SetProject(conversationID, project, access)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  const send = useCallback(
    async (conversationID: number, prompt: string) => {
      try {
        await SendTurn(conversationID, prompt)
        await load()
      } catch (cause) {
        setError(String(cause))
      }
    },
    [load],
  )

  return useMemo(
    () => ({
      nodes, setNodes, edges, error, loaded, load, patch,
      addConversation, addNote, remove, setNoteBody, send, link, unlink,
      pickProject, setAccess, pair, configureLink, saveLoop, toggleLoop,
      saveRole, resumeDialogue, assignRoles, branch,
    }),
    [nodes, edges, error, loaded, load, patch, addConversation, addNote, remove,
     setNoteBody, send, link, unlink, pickProject, setAccess, pair, configureLink,
     saveLoop, toggleLoop, saveRole, resumeDialogue, assignRoles, branch],
  )
}
