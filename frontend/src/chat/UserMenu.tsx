import {
  DeleteOutlined,
  LogoutOutlined,
  MoreOutlined,
  UserOutlined,
} from '@ant-design/icons'
import { App, Avatar, Button, Dropdown, Typography } from 'antd'
import { useState } from 'react'
import { useAuth } from 'react-oidc-context'

interface Props {
  deleteDisabled: boolean
  deleting: boolean
  onDeleteAll: () => void
}

export function UserMenu({ deleteDisabled, deleting, onDeleteAll }: Props) {
  const auth = useAuth()
  const profile = auth.user?.profile
  const { message } = App.useApp()
  const [menuOpen, setMenuOpen] = useState(false)

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

  return (
    <Dropdown
      trigger={['click']}
      placement="rightBottom"
      autoAdjustOverflow
      autoFocus
      open={menuOpen}
      onOpenChange={setMenuOpen}
      disabled={deleting}
      menu={{
        items: [
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
        onClick: ({ key }) => {
          if (key === 'delete') onDeleteAll()
          else if (key === 'logout') {
            void auth.signoutRedirect().catch(() => {
              void message.error('Sign out failed. Please try again.')
            })
          }
        },
      }}
    >
      <Button
        type="text"
        block
        className="user-menu-trigger"
        aria-label={`User menu: ${name || 'Account'}${email ? `, ${email}` : ''}`}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        loading={deleting}
        disabled={deleting}
      >
        <Avatar
          size={32}
          src={picture}
          alt=""
          icon={!name ? <UserOutlined /> : undefined}
        >
          {initials}
        </Avatar>
        <span className="user-menu-details">
          <Typography.Text strong ellipsis>
            {name || 'Account'}
          </Typography.Text>
          {email && (
            <Typography.Text type="secondary" ellipsis>
              {email}
            </Typography.Text>
          )}
        </span>
        <MoreOutlined />
      </Button>
    </Dropdown>
  )
}
