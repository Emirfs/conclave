import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Node } from '@xyflow/react'

import {
  Canvas as LoadCanvas,
  CreateConversation,
  CreateNote,
  DeleteCanvasNode,
  PatchCanvasNode,
  SendTurn,
} from '../../wailsjs/go/main/App'
import { domain } from '../../wailsjs/go/models'

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

export function useCanvas(connected: boolean) {
  const [nodes, setNodes] = useState<BoardNode[]>([])
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
      const mapped = (canvas.nodes ?? [])
        .map((node) => toBoardNode(node, conversations))
        .filter((node): node is BoardNode => node !== null)
      setNodes((current) => {
        const local = new Map(current.map((node) => [node.id, node]))
        return mapped.map((node) => {
          const existing = local.get(node.id)
          if (!existing) return node
          // Server data wins for content, local state wins for geometry: a
          // refresh landing mid-drag must not yank the node back.
          return {
            ...node,
            position: existing.position,
            width: existing.width,
            height: existing.height,
            selected: existing.selected,
            dragging: existing.dragging,
            data:
              node.data.kind === 'note' && existing.data.kind === 'note'
                ? existing.data
                : node.data,
          }
        })
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
    () => ({ nodes, setNodes, error, loaded, load, patch, addConversation, addNote, remove, setNoteBody, send }),
    [nodes, error, loaded, load, patch, addConversation, addNote, remove, setNoteBody, send],
  )
}
