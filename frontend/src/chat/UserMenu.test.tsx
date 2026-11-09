import { App, ConfigProvider } from 'antd'
import type { UserProfile } from 'oidc-client-ts'
import { renderToStaticMarkup } from 'react-dom/server'
import { useAuth, type AuthContextProps } from 'react-oidc-context'
import { afterEach, expect, it, vi } from 'vitest'
import { UserMenu } from './UserMenu'

vi.mock('react-oidc-context', () => ({ useAuth: vi.fn() }))
afterEach(() => vi.clearAllMocks())

function renderProfile(profile?: Partial<UserProfile>) {
  vi.mocked(useAuth).mockReturnValue({
    user: profile ? { profile } : undefined,
  } as AuthContextProps)
  const onDeleteAll = vi.fn().mockResolvedValue(undefined)
  const markup = renderToStaticMarkup(
    <ConfigProvider>
      <App>
        <UserMenu deleteDisabled={false} deleting={false} onDeleteAll={onDeleteAll} />
      </App>
    </ConfigProvider>,
  )
  expect(onDeleteAll).not.toHaveBeenCalled()
  return markup
}

it('displays OIDC name, email and initials, preferring name over username', () => {
  const markup = renderProfile({
    name: 'John Doe', preferred_username: 'other-name', email: 'john@example.com',
  })
  expect(markup).toContain('John Doe')
  expect(markup).toContain('john@example.com')
  expect(markup).toContain('>JD<')
  expect(markup).not.toContain('other-name')
})

it('uses preferred_username when name is blank', () => {
  const markup = renderProfile({ name: ' ', preferred_username: 'energy_trader' })
  expect(markup).toContain('energy_trader')
  expect(markup).toContain('>E<')
})

it('prefers given name and family name and displays email below', () => {
  const markup = renderProfile({
    given_name: ' John ', family_name: ' Doe ', name: 'Display name',
    email: 'john@example.com',
  })
  expect(markup).toContain('John Doe')
  expect(markup).toContain('john@example.com')
  expect(markup).toContain('>JD<')
  expect(markup).not.toContain('Display name')
})

it.each([undefined, {}, { name: ' ', email: ' ' }])('handles missing claims without inventing a user: %j', (profile) => {
  const markup = renderProfile(profile)
  expect(markup).toContain('Account')
  expect(markup).toContain('User menu: Account')
  expect(markup).not.toContain('undefined')
  expect(markup).not.toContain('@')
})

it.each(['https://example.com/avatar.png', 'http://localhost:8080/avatar.png'])('uses an HTTP(S) profile picture: %s', (picture) => {
  expect(renderProfile({ picture })).toContain(`src="${picture}"`)
})

it.each(['not a URL', 'javascript:alert(1)', 'data:image/svg+xml,test'])('falls back from an invalid picture URL: %s', (picture) => {
  const markup = renderProfile({ name: 'John Doe', picture })
  expect(markup).not.toContain('<img')
  expect(markup).toContain('>JD<')
})
