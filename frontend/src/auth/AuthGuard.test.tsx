import { renderToStaticMarkup } from 'react-dom/server'
import { useAuth, type AuthContextProps } from 'react-oidc-context'
import { afterEach, expect, it, vi } from 'vitest'
import { useRpc } from '../api/rpc'
import { AuthGuard } from './AuthGuard'

vi.mock('react-oidc-context', () => ({ useAuth: vi.fn() }))

function auth(overrides: Partial<AuthContextProps> = {}) {
  vi.mocked(useAuth).mockReturnValue({
    isLoading: false,
    isAuthenticated: true,
    user: { access_token: 'test-access-token' },
    ...overrides,
  } as AuthContextProps)
}

function captureRpc() {
  const capture = vi.fn<(request: ReturnType<typeof useRpc>) => void>()

  function Probe() {
    capture(useRpc())
    return null
  }

  renderToStaticMarkup(<Probe />)
  return capture.mock.calls[0][0]
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})

it.each([
  { isLoading: true },
  { activeNavigator: 'signinRedirect' as const },
  { isAuthenticated: false },
  { user: undefined },
  {
    error: Object.assign(new Error('Sign-in failed'), {
      source: 'signinCallback' as const,
    }),
  },
])('does not render the chat before authentication is ready: %j', (state) => {
  auth(state)
  const markup = renderToStaticMarkup(
    <AuthGuard>
      <span>Chat contents</span>
    </AuthGuard>,
  )
  expect(markup).not.toContain('Chat contents')
})

it('renders the chat when authentication has finished and an access token exists', () => {
  auth()
  const markup = renderToStaticMarkup(
    <AuthGuard>
      <span>Chat contents</span>
    </AuthGuard>,
  )
  expect(markup).toContain('Chat contents')
})

it.each([
  { isLoading: true },
  { isAuthenticated: false },
  { user: undefined },
])('blocks RPC calls when OIDC is not ready: %j', async (state) => {
  auth(state)
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  const request = captureRpc()
  await expect(request('ListChats', {})).rejects.toThrow(
    'An access token is required',
  )
  expect(fetch).not.toHaveBeenCalled()
})

it('uses the access token from the OIDC context for RPC', async () => {
  auth()
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options: RequestInit) => {
      expect(new Headers(options.headers).get('Authorization')).toBe(
        'Bearer test-access-token',
      )
      const { id } = JSON.parse(options.body as string)
      return Response.json({ jsonrpc: '2.0', id, result: [] })
    }),
  )

  await expect(captureRpc()('ListChats', {})).resolves.toEqual([])
})
