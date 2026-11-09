import { afterEach, expect, it, vi } from 'vitest'
import { rpc, RpcError } from './rpc'

afterEach(() => vi.unstubAllGlobals())

it('preserves business parameters and the submission ID across transport retries', async () => {
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

  await expect(rpc('SendMessage', params, 'test-access-token')).rejects.toThrow(
    'Network disconnected',
  )
  await expect(rpc('SendMessage', params, 'test-access-token')).resolves.toEqual({
    run_id: 'request-1',
    events_url: '/events',
  })
  expect(bodies[0].params).toEqual(params)
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

  await expect(
    rpc('DeleteChat', { chat_id: 'chat-1' }, 'test-access-token'),
  ).rejects.toBeInstanceOf(RpcError)
})

it('rejects a response for another JSON-RPC request', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({ jsonrpc: '2.0', id: 'other', result: [] }),
    ),
  )
  await expect(rpc('ListChats', {}, 'test-access-token')).rejects.toThrow(
    'Invalid backend response',
  )
})

it('sends the supplied access token only in the Authorization header and preserves cancellation', async () => {
  const requests: RequestInit[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, options: RequestInit) => {
      expect(url).toBe('/rpc')
      requests.push(options)
      const { id } = JSON.parse(options.body as string)
      return Response.json({ jsonrpc: '2.0', id, result: [] })
    }),
  )
  const controller = new AbortController()

  await rpc('ListChats', {}, 'first-access-token', controller.signal)
  await rpc('ListChats', {}, 'renewed-access-token', controller.signal)

  expect(
    requests.map((request) => new Headers(request.headers).get('Authorization')),
  ).toEqual(['Bearer first-access-token', 'Bearer renewed-access-token'])
  for (const request of requests) {
    expect(new Headers(request.headers).get('Content-Type')).toBe(
      'application/json',
    )
    expect(JSON.parse(request.body as string).params).toEqual({})
    expect(request.signal).toBe(controller.signal)
  }
})

it.each([undefined, ''])(
  'does not send a request without an access token (%s)',
  async (token) => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)

    await expect(rpc('ListChats', {}, token)).rejects.toThrow(
      'An access token is required',
    )
    expect(fetch).not.toHaveBeenCalled()
  },
)
