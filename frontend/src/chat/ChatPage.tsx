import { MenuOutlined, PlusOutlined } from '@ant-design/icons'
import {
  Alert,
  App,
  Badge,
  Button,
  Drawer,
  Empty,
  Flex,
  Grid,
  Input,
  Layout,
  Modal,
  Space,
  Spin,
  Typography,
} from 'antd'
import { useState } from 'react'
import type { Chat } from '../api/types'
import { ChatSidebar } from './ChatSidebar'
import { MessageInput } from './MessageInput'
import { MessageList } from './MessageList'
import { useChat } from './useChat'

export function ChatPage() {
  const state = useChat()
  const { message, modal } = App.useApp()
  const screens = Grid.useBreakpoint()
  const [drawer, setDrawer] = useState(false)
  const [mutating, setMutating] = useState(false)
  const [renaming, setRenaming] = useState<Chat | null>(null)
  const [title, setTitle] = useState('')
  const [input, setInput] = useState({ chatId: state.selectedId, value: '' })
  const draft = input.chatId === state.selectedId ? input.value : ''
  const setDraft = (value: string) =>
    setInput({ chatId: state.selectedId, value })

  async function mutate(action: () => Promise<void>) {
    setMutating(true)
    try {
      await action()
    } catch (cause) {
      void message.error(
        cause instanceof Error ? cause.message : 'The operation failed.',
      )
    } finally {
      setMutating(false)
    }
  }

  function confirmDelete(chat?: Chat) {
    modal.confirm({
      title: chat ? `Delete “${chat.title}”?` : 'Delete all conversations?',
      content: 'The conversation history will be permanently deleted.',
      okText: 'Delete',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          if (chat) await state.deleteChat(chat.id)
          else await state.deleteAllChats()
        } catch (cause) {
          void message.error(
            cause instanceof Error
              ? cause.message
              : 'The conversation could not be deleted.',
          )
          throw cause
        }
      },
    })
  }

  const active =
    state.run?.phase === 'waiting' || state.run?.phase === 'running'
  const sidebar = (
    <ChatSidebar
      chats={state.chats}
      selectedId={state.selectedId}
      activeChatId={active ? state.selectedId : null}
      loading={state.loadingList}
      mutating={mutating}
      onSelect={(id) => {
        state.selectChat(id)
        setDrawer(false)
      }}
      onCreate={() => {
        void mutate(state.createChat)
        setDrawer(false)
      }}
      onRename={(chat) => {
        setRenaming(chat)
        setTitle(chat.title)
      }}
      onDelete={(chat) => confirmDelete(chat)}
      onDeleteAll={() => confirmDelete()}
    />
  )
  const status = !active
    ? null
    : state.connection === 'reconnecting'
      ? 'Reconnecting…'
      : state.run?.activeTools.length
        ? 'Using tools…'
        : state.run?.phase === 'waiting'
          ? 'Waiting for agent…'
          : 'Generating…'

  return (
    <Layout className="app-layout">
      {screens.lg ? (
        <Layout.Sider width={264} theme="light" className="sidebar">
          {sidebar}
        </Layout.Sider>
      ) : (
        <Drawer
          title="Conversations"
          placement="left"
          open={drawer}
          onClose={() => setDrawer(false)}
          size={280}
          styles={{ body: { padding: 0 } }}
        >
          {sidebar}
        </Drawer>
      )}

      <Layout className="conversation-layout">
        <header className="conversation-header">
          <Flex align="center" justify="space-between" gap="middle">
            <Space size="middle" style={{ minWidth: 0 }}>
              {!screens.lg && (
                <Button
                  icon={<MenuOutlined />}
                  aria-label="Open conversations"
                  onClick={() => setDrawer(true)}
                />
              )}
              <div className="conversation-heading">
                <Typography.Title level={4} ellipsis>
                  {state.chat?.title ?? 'Energy Assistant'}
                </Typography.Title>
                <Typography.Text type="secondary">
                  Solar generation in Germany
                </Typography.Text>
              </div>
            </Space>
            {status && <Badge status="processing" text={status} />}
          </Flex>
        </header>

        {state.error && (
          <div className="error-banner">
            <Alert
              type="error"
              showIcon
              title="Something went wrong"
              description={state.error}
              action={
                <Button
                  size="small"
                  loading={state.sending || state.loadingChat}
                  onClick={() => {
                    void state.retry().then((sent) => {
                      if (sent) setDraft('')
                    })
                  }}
                >
                  Retry
                </Button>
              }
            />
          </div>
        )}

        <main className="conversation-main">
          {state.loadingChat || state.loadingList ? (
            <div className="centered">
              <Spin description="Loading conversation…" />
            </div>
          ) : state.chat ? (
            <MessageList chat={state.chat} run={state.run} />
          ) : (
            <div className="welcome">
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={
                  <Space orientation="vertical">
                    <Typography.Title level={3}>
                      Explore Germany’s solar generation
                    </Typography.Title>
                    <Typography.Text type="secondary">
                      Start a conversation to get forecasts and explanations.
                    </Typography.Text>
                  </Space>
                }
              >
                <Button
                  aria-label="New chat"
                  type="primary"
                  icon={<PlusOutlined />}
                  onClick={() => {
                    void mutate(state.createChat)
                  }}
                  loading={mutating}
                  disabled={state.loadingList}
                >
                  New chat
                </Button>
              </Empty>
            </div>
          )}
        </main>

        {state.selectedId && (
          <MessageInput
            value={draft}
            onChange={setDraft}
            disabled={state.busy || !state.chat}
            sending={state.sending}
            onSend={() => {
              if (draft.trim() && !state.busy)
                void state.send(draft).then((sent) => {
                  if (sent) setDraft('')
                })
            }}
          />
        )}
      </Layout>

      <Modal
        title="Rename conversation"
        open={!!renaming}
        onCancel={() => setRenaming(null)}
        okText="Save"
        confirmLoading={mutating}
        okButtonProps={{ disabled: !title.trim() }}
        onOk={() => {
          void mutate(async () => {
            if (renaming) await state.renameChat(renaming.id, title)
            setRenaming(null)
          })
        }}
      >
        <Input
          aria-label="Conversation title"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          maxLength={120}
        />
      </Modal>
    </Layout>
  )
}
