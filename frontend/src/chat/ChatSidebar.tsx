import {
  CommentOutlined,
  DeleteOutlined,
  EditOutlined,
  MoreOutlined,
  PlusOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons'
import {
  Avatar,
  Button,
  Dropdown,
  type DropdownProps,
  Flex,
  Menu,
  Spin,
  Typography,
} from 'antd'
import type { Chat } from '../api/types'
import { UserMenu } from './UserMenu'

interface Props {
  dark: boolean
  chats: Chat[]
  selectedId: string | null
  activeChatId: string | null
  loading: boolean
  mutating: boolean
  deletingAll: boolean
  onSelect: (id: string) => void
  onCreate: () => void
  onToggleTheme: () => void
  onRename: (chat: Chat) => void
  onDelete: (chat: Chat) => void
  onDeleteAll: () => void
}

type ChatActionsProps = Pick<DropdownProps, 'autoFocus' | 'children' | 'classNames' | 'disabled' | 'onOpenChange' | 'open' | 'placement'> & {
  chat: Chat
  deleteDisabled: boolean
  minWidth?: number
  onRename: Props['onRename']
  onDelete: Props['onDelete']
}

export function ChatActions({ chat, deleteDisabled, minWidth = 168, onRename, onDelete, ...dropdown }: ChatActionsProps) {
  return (
    <Dropdown
      {...dropdown}
      trigger={['click']}
      styles={{ root: { minWidth } }}
      menu={{
        items: [
          { key: 'rename', label: 'Rename', icon: <EditOutlined /> },
          { key: 'delete', label: 'Delete', icon: <DeleteOutlined />, danger: true, disabled: deleteDisabled },
        ],
        onClick: ({ key, domEvent }) => {
          domEvent.stopPropagation()
          if (key === 'rename') onRename(chat)
          else if (key === 'delete') onDelete(chat)
        },
      }}
    />
  )
}

export function ChatSidebar(props: Props) {
  return (
    <div className="sidebar-content">
      <div className="brand">
        <Avatar
          shape="square"
          icon={<ThunderboltOutlined />}
        />
        <Typography.Text strong>Energy Agent</Typography.Text>
      </div>
      <div className="sidebar-actions">
        <Button
          className="new-chat"
          type="primary"
          block
          icon={<PlusOutlined />}
          onClick={props.onCreate}
          disabled={props.mutating || props.loading}
        >
          New chat
        </Button>
      </div>

      <nav className="chat-navigation" aria-label="Chats">
        <Typography.Text type="secondary" className="chat-section-label">
          Chats
        </Typography.Text>
        {props.loading ? (
          <div className="centered">
            <Spin />
          </div>
        ) : props.chats.length === 0 ? (
          <Typography.Text type="secondary" className="chat-navigation-empty">
            No chats yet
          </Typography.Text>
        ) : (
          <Menu
            mode="inline"
            inlineIndent={8}
            selectedKeys={props.selectedId ? [props.selectedId] : []}
            onClick={({ key }) => props.onSelect(key)}
            items={props.chats.map((chat) => ({
              key: chat.id,
              icon: <CommentOutlined />,
              label: (
                <Flex align="center" gap="small" style={{ minWidth: 0 }}>
                  <Typography.Text ellipsis style={{ flex: 1 }}>
                    {chat.title}
                  </Typography.Text>
                  <ChatActions
                    chat={chat}
                    deleteDisabled={props.activeChatId === chat.id}
                    onRename={props.onRename}
                    onDelete={props.onDelete}
                  >
                    <Button
                      className="chat-options"
                      type="text"
                      size="small"
                      icon={<MoreOutlined />}
                      aria-label={`Options for ${chat.title}`}
                      aria-haspopup="menu"
                      disabled={props.mutating}
                      onClick={(event) => event.stopPropagation()}
                    />
                  </ChatActions>
                </Flex>
              ),
            }))}
          />
        )}
      </nav>

      <div className="sidebar-footer">
        <UserMenu
          dark={props.dark}
          onToggleTheme={props.onToggleTheme}
          deleting={props.deletingAll}
          deleteDisabled={
            props.loading || props.chats.length === 0 ||
            !!props.activeChatId || props.mutating
          }
          onDeleteAll={props.onDeleteAll}
        />
      </div>
    </div>
  )
}
