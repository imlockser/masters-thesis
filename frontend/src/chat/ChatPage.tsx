import {
  BarChartOutlined,
  ClockCircleOutlined,
  MenuOutlined,
  RiseOutlined,
  SunOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons'
import {
  Alert,
  App,
  Button,
  Drawer,
  type GetRef,
  Grid,
  Input,
  Layout,
  Modal,
  Spin,
  Typography,
} from 'antd'
import { useRef, useState } from 'react'
import type { Chat } from '../api/types'
import { ChatActions, ChatSidebar } from './ChatSidebar'
import { MessageInput } from './MessageInput'
import { MessageList } from './MessageList'
import { useChat } from './useChat'

const prompts = [
  {
    title: 'Solar forecast', detail: 'Next six hours', icon: <SunOutlined />,
    text: 'Forecast solar generation in Germany for the next six hours.',
  },
  {
    title: 'Best solar window', detail: 'Peak two-hour period', icon: <ClockCircleOutlined />,
    text: 'Which two consecutive hours in the next 24 hours have the highest forecast solar generation in Germany?',
  },
  {
    title: 'Electricity prices', detail: 'Today’s DE–LU prices', icon: <RiseOutlined />,
    text: 'Show today’s day-ahead wholesale electricity prices for the DE–LU bidding zone.',
  },
  {
    title: 'Generation mix', detail: 'Latest reported day', icon: <BarChartOutlined />,
    text: 'Show Germany’s electricity generation by source for the most recent complete day with reported data.',
  },
]

export function ChatPage({ dark, onToggleTheme }: {
  dark: boolean
  onToggleTheme: () => void
}) {
  const state = useChat()
  const { message, modal } = App.useApp()
  const screens = Grid.useBreakpoint()
  const [drawer, setDrawer] = useState(false)
  const [openChatMenuId, setOpenChatMenuId] = useState<string | null>(null)
  const [mutating, setMutating] = useState(false)
  const [deletingAll, setDeletingAll] = useState(false)
  const deletePending = useRef(false)
  const messageInput = useRef<GetRef<typeof Input.TextArea>>(null)
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

  function confirmDelete(chat?: Chat) {
    const confirmation = modal.confirm({
      title: chat ? `Delete “${chat.title}”?` : 'Delete all chats?',
      content: chat
        ? 'The chat history will be permanently deleted. This cannot be undone.'
        : 'All your chats and messages will be permanently deleted. This cannot be undone.',
      okText: chat ? 'Delete' : 'Delete all chats',
      okButtonProps: { danger: true },
      closable: true,
      mask: { closable: true },
      onOk: async () => {
        if (deletePending.current) return
        deletePending.current = true
        setDeletingAll(!chat)
        confirmation.update({
          closable: false,
          keyboard: false,
          mask: { closable: false },
          cancelButtonProps: { disabled: true },
        })
        try {
          if (chat) await state.deleteChat(chat.id)
          else await state.deleteAllChats()
        } catch (cause) {
          void message.error(
            cause instanceof Error
              ? cause.message
              : 'Chats could not be deleted.',
          )
          throw cause
        } finally {
          deletePending.current = false
          setDeletingAll(false)
          confirmation.update({
            closable: true,
            keyboard: true,
            mask: { closable: true },
            cancelButtonProps: { disabled: false },
          })
        }
      },
    })
  }

  const active =
    state.run?.phase === 'waiting' || state.run?.phase === 'running'
  const activeChatId = active ? state.selectedId : null
  const loading = state.loadingChat || state.loadingList
  const conversation = state.chat && (state.chat.messages.length > 0 || state.run) ? state.chat : null
  const showWelcome = !loading && !conversation
  const inputDisabled = state.busy || state.loadingList || (!!state.selectedId && !state.chat)
  const sidebar = (
    <ChatSidebar
      dark={dark}
      onToggleTheme={onToggleTheme}
      chats={state.chats}
      selectedId={state.selectedId}
      activeChatId={activeChatId}
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
        confirmDelete()
      }}
    />
  )
  return (
    <Layout className="app-layout">
      {screens.lg ? (
        <Layout.Sider width={272} theme="light" className="sidebar">
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
        {(!screens.lg || conversation) && (
          <header className="conversation-header">
            {!screens.lg && (
              <Button
                type="text"
                icon={<MenuOutlined />}
                aria-label="Open chats"
                onClick={() => setDrawer(true)}
              />
            )}
            {conversation && (
              <h1 className="conversation-heading">
                <ChatActions
                  chat={conversation}
                  deleteDisabled={activeChatId === conversation.id}
                  onRename={openRename}
                  onDelete={confirmDelete}
                  placement="bottomLeft"
                  classNames={{ root: 'framed-menu-popup' }}
                  minWidth={0}
                  autoFocus
                  disabled={mutating}
                  open={openChatMenuId === conversation.id}
                  onOpenChange={(open) => setOpenChatMenuId(open ? conversation.id : null)}
                >
                  <Button
                    className="conversation-menu-trigger"
                    type="text"
                    aria-haspopup="menu"
                    aria-expanded={openChatMenuId === conversation.id}
                    title={conversation.title}
                  >
                    <span className="conversation-title">{conversation.title}</span>
                    <span className="chevron" aria-hidden="true" />
                  </Button>
                </ChatActions>
              </h1>
            )}
          </header>
        )}

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

        <main className={`conversation-main${showWelcome ? ' conversation-start' : ''}`}>
          {loading ? (
            <div className="centered">
              <Spin delay={300} description="Loading chat…" style={{ color: 'var(--ant-color-text-secondary)' }} />
            </div>
          ) : conversation ? (
            <MessageList chat={conversation} run={state.run} />
          ) : (
            <div className="welcome">
              <span className="welcome-mark" aria-hidden="true"><ThunderboltOutlined /></span>
              <Typography.Title level={1} className="welcome-title">
                Explore Germany’s energy data
              </Typography.Title>
              <Typography.Paragraph type="secondary">
                Solar forecasts, generation mix, and day-ahead prices.
              </Typography.Paragraph>
            </div>
          )}

          {showWelcome && (
            <section className="welcome-prompts" aria-labelledby="quick-start-heading">
              <h2 id="quick-start-heading">Quick start</h2>
              <div className="prompt-grid">
                {prompts.map((prompt) => (
                  <Button
                    key={prompt.title}
                    className="prompt-button"
                    disabled={inputDisabled}
                    onClick={() => {
                      setDraft(prompt.text)
                      messageInput.current?.focus({ cursor: 'end' })
                    }}
                  >
                    <span className="prompt-icon" aria-hidden="true">{prompt.icon}</span>
                    <span className="prompt-copy">
                      <strong>{prompt.title}</strong>
                      <span>{prompt.detail}</span>
                    </span>
                  </Button>
                ))}
              </div>
            </section>
          )}
          <MessageInput
            key={state.selectedId ?? 'draft'}
            inputRef={messageInput}
            value={draft}
            onChange={setDraft}
            disabled={inputDisabled}
            sending={state.sending}
            onSend={() => {
              if (draft.trim() && !state.busy)
                void state.send(draft).then((sent) => {
                  if (sent) setDraft('')
                })
            }}
          />
        </main>
      </Layout>

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
