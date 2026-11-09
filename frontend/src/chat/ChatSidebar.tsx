import {
  CommentOutlined,
  DeleteOutlined,
  EditOutlined,
  MoreOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons'
import {
  Avatar,
  Button,
  Dropdown,
  Empty,
  Flex,
  Menu,
  Spin,
  Typography,
} from 'antd'
import type { Chat } from '../api/types'
import { UserMenu } from './UserMenu'

interface Props {
  chats: Chat[]
  selectedId: string | null
  activeChatId: string | null
  loading: boolean
  mutating: boolean
  deletingAll: boolean
  onSelect: (id: string) => void
  onCreate: () => void
  onRename: (chat: Chat) => void
  onDelete: (chat: Chat) => void
  onDeleteAll: () => void
}

export function ChatSidebar(props: Props) {
  return (
    <div className="sidebar-content">
      <Button
        className="brand"
        type="text"
        aria-label="New chat"
        title="New chat"
        onClick={props.onCreate}
        disabled={props.mutating || props.loading}
      >
        <Avatar
          shape="square"
          icon={<ThunderboltOutlined />}
          style={{ background: '#1677ff' }}
        />
        <Typography.Text strong>Energy Assistant</Typography.Text>
      </Button>

      <nav className="chat-navigation" aria-label="Chats">
        <Typography.Text type="secondary" className="chat-section-label">
          Chats
        </Typography.Text>
        {props.loading ? (
          <div className="centered">
            <Spin />
          </div>
        ) : props.chats.length === 0 ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description="No chats yet"
          />
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
                  <Dropdown
                    trigger={['click']}
                    menu={{
                      items: [
                        {
                          key: 'rename',
                          label: 'Rename',
                          icon: <EditOutlined />,
                        },
                        {
                          key: 'delete',
                          label: 'Delete',
                          danger: true,
                          icon: <DeleteOutlined />,
                          disabled: props.activeChatId === chat.id,
                        },
                      ],
                      onClick: ({ key, domEvent }) => {
                        domEvent.stopPropagation()
                        if (key === 'rename') props.onRename(chat)
                        else props.onDelete(chat)
                      },
                    }}
                  >
                    <Button
                      className="chat-options"
                      type="text"
                      size="small"
                      icon={<MoreOutlined />}
                      aria-label={`Options for ${chat.title}`}
                      disabled={props.mutating}
                      onClick={(event) => event.stopPropagation()}
                    />
                  </Dropdown>
                </Flex>
              ),
            }))}
          />
        )}
      </nav>

      <div className="sidebar-footer">
        <UserMenu
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
