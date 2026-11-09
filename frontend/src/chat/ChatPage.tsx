import { EditOutlined, MenuOutlined, MoonOutlined, SunOutlined } from '@ant-design/icons'
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
import { useRef, useState } from 'react'
import type { Chat } from '../api/types'
import { ChatSidebar } from './ChatSidebar'
import { MessageInput } from './MessageInput'
import { MessageList } from './MessageList'
import { useChat } from './useChat'

export function ChatPage({ dark, onToggleTheme }: {
  dark: boolean
  onToggleTheme: () => void
}) {
  const state = useChat()
  const { message, modal } = App.useApp()
  const screens = Grid.useBreakpoint()
  const [drawer, setDrawer] = useState(false)
  const [mutating, setMutating] = useState(false)
  const [confirmingDeleteAll, setConfirmingDeleteAll] = useState(false)
  const [deletingAll, setDeletingAll] = useState(false)
  const deletePending = useRef(false)
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

  function openRename(chat: Chat) {
    setRenaming(chat)
    setTitle(chat.title)
  }

  function confirmDelete(chat: Chat) {
    modal.confirm({
      title: `Delete “${chat.title}”?`,
      content: 'The chat history will be permanently deleted.',
      okText: 'Delete',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await state.deleteChat(chat.id)
        } catch (cause) {
          void message.error(
            cause instanceof Error
              ? cause.message
              : 'The chat could not be deleted.',
          )
          throw cause
        }
      },
    })
  }

  async function deleteAllChats() {
    if (deletePending.current) return
    deletePending.current = true
    setDeletingAll(true)

    try {
      await state.deleteAllChats()
      setConfirmingDeleteAll(false)
    } catch (cause) {
      void message.error(
        cause instanceof Error ? cause.message : 'Chats could not be deleted.',
      )
    } finally {
      deletePending.current = false
      setDeletingAll(false)
    }
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
      deletingAll={deletingAll}
      onSelect={(id) => {
        state.selectChat(id)
        setDrawer(false)
      }}
      onCreate={() => {
        state.newChat()
        setInput({ chatId: null, value: '' })
        setDrawer(false)
      }}
      onRename={openRename}
      onDelete={(chat) => confirmDelete(chat)}
      onDeleteAll={() => {
        setDrawer(false)
        setConfirmingDeleteAll(true)
      }}
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
          aria-label="Chats"
          closable={false}
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
            <Space size="middle" style={{ minWidth: 0, flex: 1 }}>
              {!screens.lg && (
                <Button
                  icon={<MenuOutlined />}
                  aria-label="Open chats"
                  onClick={() => setDrawer(true)}
                />
              )}
              <div className="conversation-heading">
                {state.chat && (
                  <Button
                    type="text"
                    icon={<EditOutlined style={{ fontSize: 18 }} />}
                    aria-label="Rename chat"
                    title="Rename chat"
                    disabled={mutating}
                    style={{ flexShrink: 0 }}
                    onClick={() => {
                      if (state.chat) openRename(state.chat)
                    }}
                  />
                )}
                <Typography.Title level={4} ellipsis>
                  {state.chat?.title ?? 'Energy Assistant'}
                </Typography.Title>
              </div>
            </Space>
            <Space style={{ flexShrink: 0 }}>
              {status && <Badge status="processing" text={status} />}
              <Button
                type="text"
                icon={dark ? <SunOutlined style={{ fontSize: 18 }} /> : <MoonOutlined style={{ fontSize: 18 }} />}
                aria-label={dark ? 'Switch to light theme' : 'Switch to dark theme'}
                title={dark ? 'Switch to light theme' : 'Switch to dark theme'}
                onClick={onToggleTheme}
              />
            </Space>
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
              <Spin delay={300} description="Loading chat…" style={{ color: 'var(--ant-color-text-secondary)' }} />
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
                      Start a chat to get forecasts and explanations.
                    </Typography.Text>
                  </Space>
                }
              />
            </div>
          )}
        </main>

        <MessageInput
          key={state.selectedId ?? 'draft'}
          value={draft}
          onChange={setDraft}
          disabled={state.busy || state.loadingList || (!!state.selectedId && !state.chat)}
          sending={state.sending}
          onSend={() => {
            if (draft.trim() && !state.busy)
              void state.send(draft).then((sent) => {
                if (sent) setDraft('')
              })
          }}
        />
      </Layout>

      <Modal
        title="Delete all chats?"
        open={confirmingDeleteAll}
        okText="Delete all chats"
        okButtonProps={{ danger: true, disabled: deletingAll }}
        cancelButtonProps={{ disabled: deletingAll }}
        confirmLoading={deletingAll}
        closable={!deletingAll}
        keyboard={!deletingAll}
        mask={{ closable: !deletingAll }}
        onCancel={() => {
          if (!deletingAll) setConfirmingDeleteAll(false)
        }}
        onOk={deleteAllChats}
      >
        All your chats and messages will be permanently deleted.
        This cannot be undone.
      </Modal>

      <Modal
        title="Rename chat"
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
          aria-label="Chat title"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          maxLength={120}
        />
      </Modal>
    </Layout>
  )
}
