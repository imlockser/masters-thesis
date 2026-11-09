import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { AgentEvent } from './types'
import { listenToRun } from './events'

const url = '/agent/runs/run-1/events'
const getToken = () => 'test-access-token'
const terminal: AgentEvent = { sequence: 2, type: 'run_failed', error: 'ConnectionError' }

function frame(event: AgentEvent) {
  return `id: ${event.sequence}\nevent: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`
}

function stream(...chunks: (string | Uint8Array)[]) {
  const cancel = vi.fn()
  let controller!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({
    start(value) {
      controller = value
      for (const chunk of chunks)
        controller.enqueue(typeof chunk === 'string' ? new TextEncoder().encode(chunk) : chunk)
    },
    cancel,
  })
  const response = new Response(body, {
    headers: { 'Content-Type': 'text/event-stream; charset=utf-8' },
  })
  return { response, controller, cancel }
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

it.each(['\n', '\r\n', '\r'])('authorizes SSE and reads split UTF-8, multiline data, and %j lines', async (newline) => {
  const delta: AgentEvent = { sequence: 1, type: 'text_delta', message_index: 0, text: 'Привіт ☀' }
  const multiline = JSON.stringify(delta, null, 2).replace(/\n/g, '\ndata: ')
  const text = ': keepalive\n\n'
    + frame({ sequence: 0, type: 'generation_started', message_index: 0 })
    + `id: 1\nevent: text_delta\ndata: ${multiline}\n\n`
    + frame(terminal)
  const bytes = new TextEncoder().encode(text.replace(/\n/g, newline))
  const source = stream(...Array.from(bytes, (byte) => Uint8Array.of(byte)))
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(source.response)
  vi.stubGlobal('fetch', fetch)
  const onEvent = vi.fn()
  const onConnection = vi.fn()
  const onFailure = vi.fn()

  listenToRun(url, getToken, onEvent, onConnection, onFailure)
  await vi.waitFor(() => expect(onEvent).toHaveBeenCalledTimes(3))

  const options = fetch.mock.calls[0][1] as RequestInit
  expect(fetch.mock.calls[0][0]).toBe(url)
  expect(new Headers(options.headers).get('Authorization')).toBe('Bearer test-access-token')
  expect(new Headers(options.headers).get('Accept')).toBe('text/event-stream')
  expect(new Headers(options.headers).has('Last-Event-ID')).toBe(false)
  expect(onEvent.mock.calls[1][0]).toEqual(delta)
  expect(onConnection).toHaveBeenCalledWith('open')
  expect(onFailure).not.toHaveBeenCalled()
  expect(options.signal?.aborted).toBe(true)
  expect(source.cancel).toHaveBeenCalledOnce()
  expect(vi.getTimerCount()).toBe(0)
})

it.each(['eof', 'read-error'])('resumes after %s with a renewed token, discarding incomplete and duplicate events', async (failure) => {
  let token = 'first-access-token'
  const first = stream(frame({ sequence: 0, type: 'generation_started', message_index: 0 }), 'id: 1\nevent: text_delta\ndata: {')
  const second = stream(
    frame({ sequence: 0, type: 'generation_started', message_index: 0 }),
    frame({ sequence: 1, type: 'text_delta', message_index: 0, text: 'Recovered' }),
    frame(terminal),
  )
  const fetch = vi.fn<typeof globalThis.fetch>()
    .mockResolvedValueOnce(first.response)
    .mockResolvedValueOnce(second.response)
  vi.stubGlobal('fetch', fetch)
  const onEvent = vi.fn()
  const onConnection = vi.fn()
  const onFailure = vi.fn()

  listenToRun(url, () => token, onEvent, onConnection, onFailure)
  await vi.waitFor(() => expect(onEvent).toHaveBeenCalledOnce())
  if (failure === 'eof') first.controller.close()
  else first.controller.error(new TypeError('Disconnected'))
  await vi.waitFor(() => expect(onConnection).toHaveBeenCalledWith('reconnecting'))
  token = 'renewed-access-token'
  await vi.advanceTimersByTimeAsync(3000)

  expect(fetch).toHaveBeenCalledTimes(2)
  const headers = fetch.mock.calls.map(([, options]) => new Headers(options?.headers))
  expect(headers[0].get('Authorization')).toBe('Bearer first-access-token')
  expect(headers[1].get('Authorization')).toBe('Bearer renewed-access-token')
  expect(headers[1].get('Last-Event-ID')).toBe('0')
  expect(onEvent.mock.calls.map(([event]) => event.sequence)).toEqual([0, 1, 2])
  expect(onFailure).not.toHaveBeenCalled()
  expect(vi.getTimerCount()).toBe(0)
})

it('delivers run_completed and closes without processing trailing events or reconnecting', async () => {
  const completed: AgentEvent = {
    sequence: 0,
    type: 'run_completed',
    result: { chat_id: 'chat-1', messages: [], artifacts: [] },
  }
  const source = stream(frame(completed) + frame(terminal))
  const fetch = vi.fn().mockResolvedValue(source.response)
  vi.stubGlobal('fetch', fetch)
  const onEvent = vi.fn()
  const onFailure = vi.fn()

  listenToRun(url, getToken, onEvent, vi.fn(), onFailure)
  await vi.waitFor(() => expect(onEvent).toHaveBeenCalledWith(completed))
  await vi.advanceTimersByTimeAsync(6000)

  expect(onEvent).toHaveBeenCalledOnce()
  expect(source.cancel).toHaveBeenCalledOnce()
  expect(fetch).toHaveBeenCalledOnce()
  expect(onFailure).not.toHaveBeenCalled()
})

it('reconnects after stream_error without advancing the resume cursor', async () => {
  const first = stream(
    frame({ sequence: 0, type: 'generation_started', message_index: 0 }),
    'event: stream_error\ndata: {"error":"Unavailable"}\n\n',
  )
  const fetch = vi.fn<typeof globalThis.fetch>()
    .mockResolvedValueOnce(first.response)
    .mockResolvedValueOnce(stream(frame(terminal)).response)
  vi.stubGlobal('fetch', fetch)
  const onConnection = vi.fn()
  const onFailure = vi.fn()

  listenToRun(url, getToken, vi.fn(), onConnection, onFailure)
  await vi.waitFor(() => expect(onConnection).toHaveBeenCalledWith('reconnecting'))
  expect(first.cancel).toHaveBeenCalledOnce()
  await vi.advanceTimersByTimeAsync(3000)

  expect(new Headers(fetch.mock.calls[1][1]?.headers).get('Last-Event-ID')).toBe('0')
  expect(onFailure).not.toHaveBeenCalled()
})

it.each(['network', 'server'])('retries a transient %s failure', async (failure) => {
  const fetch = vi.fn<typeof globalThis.fetch>()
  if (failure === 'network') fetch.mockRejectedValueOnce(new TypeError('Disconnected'))
  else fetch.mockResolvedValueOnce(new Response('Unavailable', { status: 503 }))
  fetch.mockResolvedValueOnce(stream(frame(terminal)).response)
  vi.stubGlobal('fetch', fetch)
  const onConnection = vi.fn()
  const onFailure = vi.fn()

  listenToRun(url, getToken, vi.fn(), onConnection, onFailure)
  await vi.waitFor(() => expect(onConnection).toHaveBeenCalledWith('reconnecting'))
  await vi.advanceTimersByTimeAsync(3000)

  expect(fetch).toHaveBeenCalledTimes(2)
  expect(onFailure).not.toHaveBeenCalled()
})

it('aborts an active reader without delivering more events or reconnecting', async () => {
  const source = stream()
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(source.response)
  vi.stubGlobal('fetch', fetch)
  const onEvent = vi.fn()
  const onConnection = vi.fn()
  const onFailure = vi.fn()

  const close = listenToRun(url, getToken, onEvent, onConnection, onFailure)
  await vi.waitFor(() => expect(onConnection).toHaveBeenCalledWith('open'))
  close()
  await vi.advanceTimersByTimeAsync(6000)

  expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true)
  expect(source.cancel).toHaveBeenCalledOnce()
  expect(onEvent).not.toHaveBeenCalled()
  expect(onFailure).not.toHaveBeenCalled()
  expect(fetch).toHaveBeenCalledOnce()
  expect(vi.getTimerCount()).toBe(0)
})

it('cancels scheduled reconnects', async () => {
  const fetch = vi.fn().mockRejectedValue(new TypeError('Disconnected'))
  vi.stubGlobal('fetch', fetch)
  const onConnection = vi.fn()
  const close = listenToRun(url, getToken, vi.fn(), onConnection, vi.fn())
  await vi.waitFor(() => expect(onConnection).toHaveBeenCalledWith('reconnecting'))

  close()
  await vi.advanceTimersByTimeAsync(6000)
  expect(fetch).toHaveBeenCalledOnce()
  expect(vi.getTimerCount()).toBe(0)
})

it('aborts a pending connection before response headers arrive', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation((_url, options) =>
    new Promise((_resolve, reject) => {
      options?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
    }),
  )
  vi.stubGlobal('fetch', fetch)
  const onFailure = vi.fn()
  const close = listenToRun(url, getToken, vi.fn(), vi.fn(), onFailure)

  close()
  await vi.advanceTimersByTimeAsync(6000)
  expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true)
  expect(onFailure).not.toHaveBeenCalled()
  expect(fetch).toHaveBeenCalledOnce()
})

it.each([undefined, ''])('does not connect without an access token (%s)', (token) => {
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  const onFailure = vi.fn()

  listenToRun(url, () => token, vi.fn(), vi.fn(), onFailure)
  expect(fetch).not.toHaveBeenCalled()
  expect(onFailure).toHaveBeenCalledWith('An access token is required to call the backend.')
  expect(vi.getTimerCount()).toBe(0)
})

it('stops reconnecting when the access token is no longer available', async () => {
  let token: string | undefined = 'test-access-token'
  const fetch = vi.fn().mockRejectedValue(new TypeError('Disconnected'))
  vi.stubGlobal('fetch', fetch)
  const onConnection = vi.fn()
  const onFailure = vi.fn()
  listenToRun(url, () => token, vi.fn(), onConnection, onFailure)
  await vi.waitFor(() => expect(onConnection).toHaveBeenCalledWith('reconnecting'))

  token = undefined
  await vi.advanceTimersByTimeAsync(3000)
  expect(fetch).toHaveBeenCalledOnce()
  expect(onFailure).toHaveBeenCalledWith('An access token is required to call the backend.')
  expect(vi.getTimerCount()).toBe(0)
})

it.each([401, 403, 404])('stops on HTTP %s without exposing the response body', async (status) => {
  const fetch = vi.fn().mockResolvedValue(new Response('sensitive response', { status }))
  vi.stubGlobal('fetch', fetch)
  const onFailure = vi.fn()

  listenToRun(url, getToken, vi.fn(), vi.fn(), onFailure)
  await vi.waitFor(() => expect(onFailure).toHaveBeenCalledOnce())
  expect(onFailure).toHaveBeenCalledWith(`The agent stream could not be opened (HTTP ${status}).`)
  expect(fetch).toHaveBeenCalledOnce()
  expect(vi.getTimerCount()).toBe(0)
})

it('rejects responses that are not SSE', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: 'Unexpected' })))
  const onFailure = vi.fn()

  listenToRun(url, getToken, vi.fn(), vi.fn(), onFailure)
  await vi.waitFor(() => expect(onFailure).toHaveBeenCalledOnce())
  expect(onFailure).toHaveBeenCalledWith('The backend did not return an agent event stream.')
  expect(vi.getTimerCount()).toBe(0)
})

it.each([
  'id: 0\nevent: text_delta\ndata: {"type":"text_delta","sequence":-1}\n\n',
  'id: 0\nevent: text_delta\ndata: {"type":"run_failed","sequence":0}\n\n',
  'id: 0\nevent: text_delta\ndata: null\n\n',
  'id: 0\nevent: text_delta\ndata: {broken JSON}\n\n',
  'id: 7\nevent: text_delta\ndata: {"type":"text_delta","sequence":0}\n\n',
])('rejects malformed event data without reconnecting: %s', async (chunk) => {
  const source = stream(chunk)
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(source.response))
  const onEvent = vi.fn()
  const onFailure = vi.fn()

  listenToRun(url, getToken, onEvent, vi.fn(), onFailure)
  await vi.waitFor(() => expect(onFailure).toHaveBeenCalledOnce())
  expect(onEvent).not.toHaveBeenCalled()
  expect(source.cancel).toHaveBeenCalledOnce()
  expect(vi.getTimerCount()).toBe(0)
})
