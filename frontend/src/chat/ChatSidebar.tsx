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
  Empty,
  Flex,
  Menu,
  Space,
  Spin,
  Typography,
} from 'antd'
import type { Chat } from '../api/types'

interface Props {
  chats: Chat[]
  selectedId: string | null
  activeChatId: string | null
  loading: boolean
  mutating: boolean
  onSelect: (id: string) => void
  onCreate: () => void
  onRename: (chat: Chat) => void
  onDelete: (chat: Chat) => void
  onDeleteAll: () => void
}

export function ChatSidebar(props: Props) {
  return (
    <div className="sidebar-content">
      <Space className="brand" size="middle">
        <Avatar
          shape="square"
          icon={<ThunderboltOutlined />}
          style={{ background: '#1677ff' }}
        />
        <Typography.Text strong>Energy Assistant</Typography.Text>
      </Space>

      <div className="sidebar-actions">
        <Button
          aria-label="New chat"
          block
          type="primary"
          icon={<PlusOutlined />}
          onClick={props.onCreate}
          loading={props.mutating}
        >
          New chat
        </Button>
      </div>

      <nav className="chat-navigation" aria-label="Chats">
        {props.loading ? (
          <div className="centered">
            <Spin />
          </div>
        ) : props.chats.length === 0 ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description="No conversations yet"
          />
        ) : (
          <Menu
            mode="inline"
            selectedKeys={props.selectedId ? [props.selectedId] : []}
            onClick={({ key }) => props.onSelect(key)}
            items={props.chats.map((chat) => ({
              key: chat.id,
              icon: <CommentOutlined />,
              label: (
                <Flex align="center" gap="small">
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
        <Button
          aria-label="Delete all chats"
          block
          type="text"
          danger
          icon={<DeleteOutlined />}
          disabled={
            props.chats.length === 0 || !!props.activeChatId || props.mutating
          }
          onClick={props.onDeleteAll}
        >
          Delete all chats
        </Button>
        <Typography.Text type="secondary">
          Germany · All times in UTC
        </Typography.Text>
      </div>
    </div>
  )
}
