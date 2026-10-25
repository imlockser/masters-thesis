import { useCallback, useEffect, useRef, useState } from 'react'
import { listenToRun } from '../api/events'
import { rpc, RpcError, USER_ID } from '../api/rpc'
import type { Chat, ChatDetails } from '../api/types'
import { applyEvent, startRun } from './run'
import type { RunState } from './run'

interface Submission {
  request_id: string
  chat_id: string
  content: string
}

const selectionKey = `energy-assistant:${USER_ID}:chat`
const failedRun =
  'The agent could not complete this request. You can send another message.'

export function useChat() {
  const [chats, setChats] = useState<Chat[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [chat, setChat] = useState<ChatDetails | null>(null)
  const [run, setRun] = useState<RunState | null>(null)
  const [loadingList, setLoadingList] = useState(true)
  const [loadingChat, setLoadingChat] = useState(false)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [connection, setConnection] = useState('connecting')
  const selected = useRef<string | null>(null)
  const loadVersion = useRef(0)
  const retrySubmission = useRef<Submission | null>(null)

  const selectChat = useCallback((id: string | null) => {
    if (selected.current === id) return

    selected.current = id
    loadVersion.current++
    retrySubmission.current = null
    setSelectedId(id)
    setChat(null)
    setRun(null)
    setError(null)
    setSending(false)
    setLoadingChat(!!id)
    if (id) localStorage.setItem(selectionKey, id)
    else localStorage.removeItem(selectionKey)
  }, [])

  const refreshList = useCallback(async (signal?: AbortSignal) => {
    const items = await rpc('ListChats', {}, signal)
    setChats(items)
    return items
  }, [])

  const loadChat = useCallback(
    async (id: string, signal?: AbortSignal, failure?: string) => {
      const version = ++loadVersion.current
      const current = () =>
        selected.current === id &&
        loadVersion.current === version &&
        !signal?.aborted

      try {
        const details = await rpc('GetChat', { chat_id: id }, signal)
        if (!current()) return

        let active = null
        if (details.active_run_id) {
          const last = details.messages.at(-1)
          if (
            last?.role !== 'user' ||
            typeof last.payload.content !== 'string'
          ) {
            throw new Error('The active request could not be recovered.')
          }

          // Reusing the run ID also recovers a request whose original enqueue was not confirmed.
          const confirmed = await rpc(
            'SendMessage',
            {
              request_id: details.active_run_id,
              chat_id: id,
              content: last.payload.content,
            },
            signal,
          )
          active = startRun(confirmed.run_id, confirmed.events_url)
        }
        if (!current()) return

        setChat(details)
        setRun(active)
        setConnection('connecting')
        setError(failure ?? null)
      } catch (cause) {
        if (current())
          setError(
            cause instanceof Error
              ? cause.message
              : 'The conversation could not be loaded.',
          )
      } finally {
        if (current()) setLoadingChat(false)
      }
    },
    [],
  )

  useEffect(() => {
    const controller = new AbortController()
    // oxlint-disable-next-line react/set-state-in-effect -- State updates follow asynchronous network responses.
    void refreshList(controller.signal)
      .then((items) => {
        if (controller.signal.aborted) return
        const saved = localStorage.getItem(selectionKey)
        selectChat(
          items.some((item) => item.id === saved)
            ? saved
            : (items[0]?.id ?? null),
        )
      })
      .catch((cause) => {
        if (!controller.signal.aborted)
          setError(
            cause instanceof Error
              ? cause.message
              : 'Chats could not be loaded.',
          )
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoadingList(false)
      })

    return () => controller.abort()
  }, [refreshList, selectChat])

  useEffect(() => {
    if (!selectedId) return
    const controller = new AbortController()
    // oxlint-disable-next-line react/set-state-in-effect -- State updates follow asynchronous network responses.
    void loadChat(selectedId, controller.signal)
    return () => controller.abort()
  }, [selectedId, loadChat])

  const runId = run?.id
  const runUrl = run?.url

  useEffect(() => {
    if (!runId || !runUrl || !selectedId) return

    return listenToRun(
      runUrl,
      (event) => {
        if (selected.current !== selectedId) return
        setRun((previous) =>
          previous?.id === runId ? applyEvent(previous, event) : previous,
        )
        if (event.type === 'run_completed' || event.type === 'run_failed') {
          setLoadingChat(true)
          void loadChat(
            selectedId,
            undefined,
            event.type === 'run_failed' ? failedRun : undefined,
          )
        }
      },
      (status) => {
        if (selected.current === selectedId) setConnection(status)
      },
      (message) => {
        if (selected.current === selectedId) setError(message)
      },
    )
  }, [runId, runUrl, selectedId, loadChat])

  async function submit(pending: Submission): Promise<boolean> {
    setSending(true)
    setError(null)
    retrySubmission.current = pending

    try {
      const started = await rpc('SendMessage', pending)
      if (selected.current !== pending.chat_id) return false

      retrySubmission.current = null
      setChat((current) => {
        if (!current || current.id !== pending.chat_id) return current
        if (current.active_run_id === started.run_id) return current

        return {
          ...current,
          active_run_id: started.run_id,
          messages: [
            ...current.messages,
            {
              id: -Date.now(),
              chat_id: current.id,
              role: 'user',
              payload: { content: pending.content },
              created_at: new Date().toISOString(),
            },
          ],
        }
      })
      setConnection('connecting')
      setRun(startRun(started.run_id, started.events_url))
      return true
    } catch (cause) {
      if (selected.current === pending.chat_id) {
        if (cause instanceof RpcError && cause.code !== -32603) {
          retrySubmission.current = null
          if (cause.code === -32003) {
            setLoadingChat(true)
            await loadChat(pending.chat_id)
          }
        }
        setError(
          cause instanceof Error
            ? cause.message
            : 'The message could not be sent.',
        )
      }
      return false
    } finally {
      if (selected.current === pending.chat_id) setSending(false)
    }
  }

  async function send(content: string): Promise<boolean> {
    if (!selectedId || !content.trim()) return false
    const previous = retrySubmission.current
    const pending =
      previous?.chat_id === selectedId && previous.content === content
        ? previous
        : { request_id: crypto.randomUUID(), chat_id: selectedId, content }
    return submit(pending)
  }

  async function retry(): Promise<boolean> {
    if (retrySubmission.current?.chat_id === selectedId)
      return submit(retrySubmission.current)
    if (selectedId) {
      setLoadingChat(true)
      await loadChat(selectedId)
    } else {
      try {
        const items = await refreshList()
        setError(null)
        selectChat(items[0]?.id ?? null)
      } catch (cause) {
        setError(
          cause instanceof Error ? cause.message : 'Chats could not be loaded.',
        )
      }
    }
    return false
  }

  async function createChat() {
    const created = await rpc('CreateChat', { title: 'New chat' })
    setChats((items) => [created, ...items])
    selectChat(created.id)
  }

  async function renameChat(id: string, title: string) {
    const updated = await rpc('RenameChat', { chat_id: id, new_title: title })
    setChats((items) => items.map((item) => (item.id === id ? updated : item)))
    setChat((current) =>
      current?.id === id ? { ...current, title: updated.title } : current,
    )
  }

  async function deleteChat(id: string) {
    await rpc('DeleteChat', { chat_id: id })
    const remaining = chats.filter((item) => item.id !== id)
    setChats(remaining)
    if (selected.current === id) selectChat(remaining[0]?.id ?? null)
  }

  async function deleteAllChats() {
    await rpc('DeleteAllChats', {})
    setChats([])
    selectChat(null)
  }

  const busy =
    sending ||
    loadingChat ||
    run?.phase === 'waiting' ||
    run?.phase === 'running'

  return {
    chats,
    chat,
    run,
    selectedId,
    loadingList,
    loadingChat,
    sending,
    error,
    connection,
    busy,
    selectChat,
    createChat,
    renameChat,
    deleteChat,
    deleteAllChats,
    send,
    retry,
  }
}
