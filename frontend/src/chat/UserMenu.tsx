import {
  DeleteOutlined,
  LogoutOutlined,
  MoonOutlined,
  SunOutlined,
  UserOutlined,
} from '@ant-design/icons'
import { App, Avatar, Button, Dropdown, Grid, Typography, type GetRef } from 'antd'
import { useRef, useState } from 'react'
import { useAuth } from 'react-oidc-context'

interface Props {
  dark: boolean
  deleteDisabled: boolean
  deleting: boolean
  onToggleTheme: () => void
  onDeleteAll: () => void
}

export function UserMenu({ dark, deleteDisabled, deleting, onToggleTheme, onDeleteAll }: Props) {
  const auth = useAuth()
  const profile = auth.user?.profile
  const { message } = App.useApp()
  const screens = Grid.useBreakpoint()
  const [menuOpen, setMenuOpen] = useState(false)
  const trigger = useRef<GetRef<typeof Button>>(null)

  const fullName = [profile?.given_name?.trim(), profile?.family_name?.trim()]
    .filter(Boolean).join(' ')
  const name = fullName || profile?.name?.trim() || profile?.preferred_username?.trim()
  const email = profile?.email?.trim()
  const initials = name?.split(/\s+/).slice(0, 2)
    .map((word) => Array.from(word)[0]).join('').toLocaleUpperCase()
  let picture: string | undefined
  if (profile?.picture && URL.canParse(profile.picture)) {
    const url = new URL(profile.picture)
    if (url.protocol === 'https:' || url.protocol === 'http:') picture = url.href
  }

  const profileSummary = (
    <span className="user-menu-summary">
      <Avatar
        className="user-menu-avatar"
        size={32}
        src={picture}
        alt=""
        icon={!name ? <UserOutlined /> : undefined}
      >
        {initials}
      </Avatar>
      <span className="user-menu-details">
        <Typography.Text strong ellipsis title={name}>
          {name || 'Account'}
        </Typography.Text>
        {email && (
          <Typography.Text type="secondary" ellipsis title={email}>
            {email}
          </Typography.Text>
        )}
      </span>
    </span>
  )

  return (
    <Dropdown
      classNames={{ root: 'user-menu-popup framed-menu-popup' }}
      trigger={['click']}
      placement={screens.lg ? 'rightBottom' : 'topLeft'}
      autoAdjustOverflow
      autoFocus
      open={menuOpen}
      onOpenChange={setMenuOpen}
      disabled={deleting}
      menu={{
        items: [
          {
            type: 'group',
            key: 'profile',
            label: <span className="user-menu-profile">{profileSummary}</span>,
            children: [
              {
                key: 'theme',
                label: dark ? 'Switch to light theme' : 'Switch to dark theme',
                icon: dark ? <SunOutlined /> : <MoonOutlined />,
              },
              { type: 'divider' },
              {
                key: 'delete',
                label: 'Delete all chats',
                icon: <DeleteOutlined />,
                danger: true,
                disabled: deleteDisabled,
              },
              { type: 'divider' },
              { key: 'logout', label: 'Log out', icon: <LogoutOutlined /> },
            ],
          },
        ],
        onKeyDown: (event) => {
          if (event.key !== 'Escape') return
          event.stopPropagation()
          setMenuOpen(false)
          // Let the menu's pending arrow-key focus update finish first.
          requestAnimationFrame(() => trigger.current?.focus())
        },
        onClick: ({ key }) => {
          if (key === 'theme') onToggleTheme()
          else if (key === 'delete') onDeleteAll()
          else if (key === 'logout') {
            void auth.signoutRedirect().catch(() => {
              void message.error('Sign out failed. Please try again.')
            })
          }
        },
      }}
    >
      <Button
        ref={trigger}
        type="text"
        block
        className="user-menu-trigger"
        aria-label={`User menu: ${name || 'Account'}${email ? `, ${email}` : ''}`}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        loading={deleting}
        disabled={deleting}
      >
        {profileSummary}
        <span className="user-menu-chevrons" aria-hidden="true" />
      </Button>
    </Dropdown>
  )
}
