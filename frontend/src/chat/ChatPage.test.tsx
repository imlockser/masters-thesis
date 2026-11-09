import type { ModalFuncProps } from 'antd'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, expect, it, vi } from 'vitest'
import type { Chat } from '../api/types'
import { ChatPage } from './ChatPage'

const captured = vi.hoisted(() => ({
  confirm: vi.fn(), update: vi.fn(), error: vi.fn(),
  deleteChat: vi.fn(), deleteAllChats: vi.fn(),
  sidebar: null as { onDelete: (chat: Chat) => void; onDeleteAll: () => void } | null,
}))
vi.mock('antd', async (original) => ({
  ...await original<typeof import('antd')>(),
  Grid: { useBreakpoint: () => ({ lg: true }) },
  App: { useApp: () => ({
    message: { error: captured.error }, modal: { confirm: captured.confirm },
  }) },
}))
vi.mock('./useChat', () => ({ useChat: () => ({
  chats: [], selectedId: null, chat: null, run: null, loadingList: false,
  deleteChat: captured.deleteChat, deleteAllChats: captured.deleteAllChats,
}) }))
vi.mock('./ChatSidebar', () => ({ ChatSidebar: (props: NonNullable<typeof captured.sidebar>) => {
  captured.sidebar = props
  return null
} }))
vi.mock('./MessageInput', () => ({ MessageInput: () => null }))

const chat: Chat = { id: 'chat-1', title: 'Solar forecast', user_id: 'user', created_at: '' }

beforeEach(() => {
  vi.resetAllMocks()
  captured.confirm.mockReturnValue({ update: captured.update })
})

function openConfirmation(all: boolean): ModalFuncProps {
  renderToStaticMarkup(<ChatPage dark onToggleTheme={() => {}} />)
  if (all) captured.sidebar!.onDeleteAll()
  else captured.sidebar!.onDelete(chat)
  return captured.confirm.mock.calls[0][0] as ModalFuncProps
}

it.each([false, true])('uses the shared dismissible confirmation without deleting before approval (all=%s)', (all) => {
  const options = openConfirmation(all)
  expect(options.title).toBe(all ? 'Delete all chats?' : 'Delete “Solar forecast”?')
  expect(options.closable).toBe(true)
  expect(options.mask).toEqual({ closable: true })
  expect(options.okButtonProps).toEqual({ danger: true })
  expect(options.content).toContain('This cannot be undone.')
  expect(captured.deleteChat).not.toHaveBeenCalled()
  expect(captured.deleteAllChats).not.toHaveBeenCalled()
})

it.each([false, true])('blocks dismissal and duplicate deletion while pending (all=%s)', async (all) => {
  let finish!: () => void
  const pending = new Promise<void>((resolve) => { finish = resolve })
  const action = all ? captured.deleteAllChats : captured.deleteChat
  action.mockReturnValue(pending)
  const options = openConfirmation(all)
  const work = options.onOk!()
  expect(captured.update).toHaveBeenLastCalledWith({
    closable: false, keyboard: false, mask: { closable: false },
    cancelButtonProps: { disabled: true },
  })
  await options.onOk!()
  expect(action).toHaveBeenCalledOnce()
  finish()
  await work
  if (!all) expect(action).toHaveBeenCalledWith(chat.id)
})

it.each([false, true])('keeps failed deletion open and restores dismissal (all=%s)', async (all) => {
  const action = all ? captured.deleteAllChats : captured.deleteChat
  action.mockRejectedValue(new Error('Deletion failed'))
  const options = openConfirmation(all)
  await expect(options.onOk!()).rejects.toThrow('Deletion failed')
  expect(captured.error).toHaveBeenCalledWith('Deletion failed')
  expect(captured.update).toHaveBeenLastCalledWith({
    closable: true, keyboard: true, mask: { closable: true },
    cancelButtonProps: { disabled: false },
  })
})
