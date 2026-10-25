import { afterEach, expect, it, vi } from 'vitest'
import { rpc, RpcError, USER_ID } from './rpc'

afterEach(() => vi.unstubAllGlobals())

it('preserves the submission ID across transport retries and adds the configured user', async () => {
  const bodies: { id: string; params: object }[] = []
  const fetch = vi.fn(async (_url: string, options: { body: string }) => {
    const body = JSON.parse(options.body)
    bodies.push(body)
    if (bodies.length === 1) throw new TypeError('Network disconnected')
    return Response.json({
      jsonrpc: '2.0',
      id: body.id,
      result: { run_id: 'request-1', events_url: '/events' },
    })
  })
  vi.stubGlobal('fetch', fetch)
  const params = {
    request_id: 'request-1',
    chat_id: 'chat-1',
    content: 'Forecast',
  }

  await expect(rpc('SendMessage', params)).rejects.toThrow(
    'Network disconnected',
  )
  await expect(rpc('SendMessage', params)).resolves.toEqual({
    run_id: 'request-1',
    events_url: '/events',
  })
  expect(bodies[0].params).toEqual({ ...params, user_id: USER_ID })
  expect(bodies[1].params).toEqual(bodies[0].params)
  expect(bodies[1].id).not.toBe(bodies[0].id)
})

it('returns backend errors instead of treating them as successful results', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options: { body: string }) => {
      const { id } = JSON.parse(options.body)
      return Response.json({
        jsonrpc: '2.0',
        id,
        error: { code: -32003, message: 'Chat is busy' },
      })
    }),
  )

  await expect(rpc('DeleteChat', { chat_id: 'chat-1' })).rejects.toBeInstanceOf(
    RpcError,
  )
})

it('rejects a response for another JSON-RPC request', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({ jsonrpc: '2.0', id: 'other', result: [] }),
    ),
  )
  await expect(rpc('ListChats', {})).rejects.toThrow('Invalid backend response')
})
