import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { chatTitle, useChat } from './useChat'

const { rpc } = vi.hoisted(() => ({ rpc: vi.fn() }))
vi.mock('../api/rpc', async (original) => ({
  ...await original<typeof import('../api/rpc')>(),
  useRpc: () => rpc,
}))
vi.mock('../auth/useAccessToken', () => ({ useAccessToken: () => () => 'test-token' }))
vi.mock('react', async (original) => {
  const actual = await original<typeof import('react')>()
  return {
    ...actual,
    // SSR skips initial network effects; submission tests start with the list ready.
    useState: (initial: unknown) => actual.useState(initial === true ? false : initial),
  }
})

beforeEach(() => {
  vi.stubGlobal('localStorage', { setItem: vi.fn(), removeItem: vi.fn() })
})
afterEach(() => {
  vi.clearAllMocks()
  rpc.mockReset()
  vi.unstubAllGlobals()
})

function chatState() {
  const capture = vi.fn<(state: ReturnType<typeof useChat>) => void>()
  function Probe() {
    capture(useChat())
    return null
  }
  renderToStaticMarkup(createElement(Probe))
  return capture.mock.calls[0][0]
}

const created = { id: 'chat-1', user_id: 'user', title: 'Forecast tomorrow', created_at: '' }
const started = { run_id: 'run-1', events_url: '/agent/runs/run-1/events' }

it('uses the user input and collapses whitespace for a chat title', () => {
  expect(chatTitle('  Forecast\n solar   generation\tfor tomorrow  '))
    .toBe('Forecast solar generation for tomorrow')
})

it('shortens long requests at a word boundary', () => {
  const content = 'Please forecast solar generation in Germany for the next two days and explain the main factors driving the forecast.'
  expect(chatTitle(content))
    .toBe('Please forecast solar generation in Germany for the next two days and explain…')
})

it('keeps titles within 80 Unicode characters, including long single words', () => {
  expect(chatTitle('а'.repeat(80))).toBe('а'.repeat(80))
  expect(chatTitle('☀'.repeat(100))).toBe(`${'☀'.repeat(79)}…`)
  expect(chatTitle('🌞'.repeat(100))).toBe(`${'🌞'.repeat(79)}…`)
})

it('opens a draft without creating a chat and ignores empty submissions', async () => {
  const state = chatState()
  state.newChat()
  expect(await state.send(' \n ')).toBe(false)
  expect(rpc).not.toHaveBeenCalled()
})

it('creates a named chat only for its first non-empty submission', async () => {
  rpc.mockResolvedValueOnce(created).mockResolvedValueOnce(started)
  const state = chatState()
  expect(await state.send('Forecast tomorrow')).toBe(true)
  expect(rpc.mock.calls.map(([method]) => method)).toEqual(['CreateChat', 'SendMessage'])
  expect(rpc.mock.calls[0][1]).toEqual({ title: 'Forecast tomorrow' })
  expect(rpc.mock.calls[1][1]).toEqual({
    request_id: expect.any(String), chat_id: created.id, content: 'Forecast tomorrow',
  })
})

it('retries the same chat and request after a failed first send', async () => {
  rpc.mockResolvedValueOnce(created).mockRejectedValueOnce(new Error('Network unavailable'))
    .mockResolvedValueOnce(started)
  const state = chatState()
  expect(await state.send('Forecast tomorrow')).toBe(false)
  expect(await state.retry()).toBe(true)
  expect(rpc.mock.calls.map(([method]) => method)).toEqual(['CreateChat', 'SendMessage', 'SendMessage'])
  expect(rpc.mock.calls[2][1]).toEqual(rpc.mock.calls[1][1])
})

it('uses an existing selected chat without creating another one', async () => {
  rpc.mockResolvedValueOnce(started)
  const state = chatState()
  state.selectChat('existing-chat')
  expect(await state.send('Another forecast')).toBe(true)
  expect(rpc).toHaveBeenCalledOnce()
  expect(rpc.mock.calls[0]).toEqual(['SendMessage', {
    request_id: expect.any(String), chat_id: 'existing-chat', content: 'Another forecast',
  }])
})
