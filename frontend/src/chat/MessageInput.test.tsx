import type { KeyboardEvent as ReactKeyboardEvent, Ref } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import type { TextAreaProps, TextAreaRef } from 'antd/es/input/TextArea'
import { afterEach, expect, it, vi } from 'vitest'
import { MessageInput } from './MessageInput'

const captured = vi.hoisted(() => ({
  effects: [] as (() => void | (() => void))[],
  textarea: undefined as (TextAreaProps & { ref?: Ref<TextAreaRef> }) | undefined,
  focus: vi.fn(),
  blur: vi.fn(),
}))

vi.mock('react', async (original) => ({
  ...await original<typeof import('react')>(),
  useEffect: (effect: () => void | (() => void)) => { captured.effects.push(effect) },
  useRef: () => ({ current: { focus: captured.focus, blur: captured.blur } }),
}))
vi.mock('@ant-design/icons', () => ({ SendOutlined: () => null }))
vi.mock('antd', () => ({
  Button: () => <button aria-label="Send" />,
  Input: {
    TextArea: (props: NonNullable<typeof captured.textarea>) => {
      captured.textarea = props
      const ref = props.ref as { current: { focus: typeof captured.focus; blur: typeof captured.blur } | null }
      ref.current = { focus: captured.focus, blur: captured.blur }
      return <textarea />
    },
  },
}))

afterEach(() => {
  captured.effects.length = 0
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})

function renderInput(value = '', disabled = false) {
  captured.effects.length = 0
  const onChange = vi.fn()
  const onSend = vi.fn()
  vi.stubGlobal('window', { addEventListener: vi.fn(), removeEventListener: vi.fn() })
  vi.stubGlobal('document', { querySelectorAll: vi.fn(() => []) })
  const markup = renderToStaticMarkup(
    <MessageInput value={value} disabled={disabled} sending={false} onChange={onChange} onSend={onSend} />,
  )
  const cleanups = captured.effects.map((effect) => effect())
  const cleanup = () => cleanups.forEach((remove) => { if (typeof remove === 'function') remove() })
  const initiallyFocused = captured.focus.mock.calls.length > 0
  captured.focus.mockClear()
  const listener = vi.mocked(window.addEventListener).mock.calls[0]?.[1] as
    ((event: KeyboardEvent) => void) | undefined
  return { onChange, onSend, listener, cleanup, markup, initiallyFocused }
}

function keyEvent(key: string, changes: Partial<KeyboardEvent> = {}): KeyboardEvent {
  return {
    key, target: null, defaultPrevented: false, isComposing: false,
    ctrlKey: false, metaKey: false, altKey: false, shiftKey: false,
    preventDefault: vi.fn(), ...changes,
  } as unknown as KeyboardEvent
}

it.each(['a', 'Ж', '3', '?'])('focuses the composer and preserves the first character: %s', (key) => {
  const input = renderInput()
  input.listener?.(keyEvent(key))
  expect(captured.focus).toHaveBeenCalledWith({ cursor: 'end' })
  expect(input.onChange).toHaveBeenCalledWith(key)
  expect(input.onSend).not.toHaveBeenCalled()
  expect(input.markup).toContain('composer-box')
  expect(input.markup).not.toContain('Shift')
  if (typeof input.cleanup === 'function') input.cleanup()
  expect(window.removeEventListener).toHaveBeenCalledWith('keydown', input.listener)
})

it.each([' ', 'Enter', 'Escape', 'ArrowLeft', 'Tab'])('does not activate an empty composer on %s', (key) => {
  const input = renderInput()
  input.listener?.(keyEvent(key))
  expect(captured.focus).not.toHaveBeenCalled()
  expect(input.onChange).not.toHaveBeenCalled()
  expect(input.onSend).not.toHaveBeenCalled()
})

it('allows space, Enter and Shift+Enter when there is text', () => {
  const input = renderInput('Forecast')
  input.listener?.(keyEvent(' '))
  expect(input.onChange).toHaveBeenLastCalledWith('Forecast ')
  input.listener?.(keyEvent('Enter', { shiftKey: true }))
  expect(input.onChange).toHaveBeenLastCalledWith('Forecast\n')
  input.listener?.(keyEvent('Enter'))
  expect(captured.blur).toHaveBeenCalledOnce()
  expect(input.onSend).toHaveBeenCalledOnce()
})

it.each(['ctrlKey', 'metaKey', 'altKey', 'isComposing', 'defaultPrevented'] as const)('preserves %s keyboard events', (property) => {
  const input = renderInput()
  input.listener?.(keyEvent('a', { [property]: true }))
  expect(input.onChange).not.toHaveBeenCalled()
  expect(captured.focus).not.toHaveBeenCalled()
})

it('leaves other inputs and visible dialogs or menus alone', () => {
  const input = renderInput()
  input.listener?.(keyEvent('a', { target: { closest: () => ({}) } as unknown as EventTarget }))
  expect(input.onChange).not.toHaveBeenCalled()

  vi.mocked(document.querySelectorAll).mockReturnValue([
    { closest: () => null, checkVisibility: () => true },
  ] as unknown as NodeListOf<Element>)
  input.listener?.(keyEvent('a'))
  expect(input.onChange).not.toHaveBeenCalled()
  expect(captured.focus).not.toHaveBeenCalled()
})

it('blocks whitespace and Enter in an empty focused textarea', () => {
  const input = renderInput()
  for (const key of [' ', 'Enter']) {
    const event = { key, shiftKey: true, nativeEvent: { isComposing: false }, preventDefault: vi.fn() }
    captured.textarea?.onKeyDown?.(event as unknown as ReactKeyboardEvent<HTMLTextAreaElement>)
    expect(event.preventDefault).toHaveBeenCalledOnce()
  }
  expect(input.onSend).not.toHaveBeenCalled()
})

it('does not register automatic typing while disabled', () => {
  const input = renderInput('', true)
  expect(input.listener).toBeUndefined()
  expect(window.addEventListener).not.toHaveBeenCalled()
  expect(input.initiallyFocused).toBe(false)
})

it('does not auto-focus when the composer becomes ready after a response', () => {
  renderInput('', true)
  expect(renderInput().initiallyFocused).toBe(false)
})

it('does not let hidden overlays block typing or let visible overlays lose focus', () => {
  const input = renderInput()
  vi.mocked(document.querySelectorAll).mockReturnValue([
    { closest: () => null, checkVisibility: () => false },
  ] as unknown as NodeListOf<Element>)
  input.listener?.(keyEvent('Ж'))
  expect(input.onChange).toHaveBeenCalledWith('Ж')

  captured.focus.mockClear()
  vi.mocked(document.querySelectorAll).mockReturnValue([
    { closest: () => null, checkVisibility: () => true },
  ] as unknown as NodeListOf<Element>)
  input.listener?.(keyEvent('a'))
  expect(input.onChange).toHaveBeenCalledOnce()
  expect(captured.focus).not.toHaveBeenCalled()
})

it('allows immediate typing when the sidebar navigation menu is visible', () => {
  const input = renderInput()
  vi.mocked(document.querySelectorAll).mockReturnValue([
    { closest: () => ({}), checkVisibility: () => true },
  ] as unknown as NodeListOf<Element>)
  input.listener?.(keyEvent('Ж'))
  expect(captured.focus).toHaveBeenCalledWith({ cursor: 'end' })
  expect(input.onChange).toHaveBeenCalledWith('Ж')
})

it('blurs the textarea immediately when submitting with Enter', () => {
  const input = renderInput('Forecast')
  captured.textarea?.onKeyDown?.({
    key: 'Enter', shiftKey: false, nativeEvent: { isComposing: false }, preventDefault: vi.fn(),
  } as unknown as ReactKeyboardEvent<HTMLTextAreaElement>)
  expect(captured.blur).toHaveBeenCalledOnce()
  expect(input.onSend).toHaveBeenCalledOnce()
  expect(captured.blur.mock.invocationCallOrder[0]).toBeLessThan(input.onSend.mock.invocationCallOrder[0])
})

it('blurs the focused send button immediately on form submission', () => {
  const onSend = vi.fn()
  const element = MessageInput({ value: 'Forecast', disabled: false, sending: false, onChange: vi.fn(), onSend })
  element.props.onSubmit({
    preventDefault: vi.fn(), currentTarget: { querySelector: () => ({ blur: captured.blur }) },
  })
  expect(captured.blur).toHaveBeenCalledOnce()
  expect(onSend).toHaveBeenCalledOnce()
  expect(captured.blur.mock.invocationCallOrder[0]).toBeLessThan(onSend.mock.invocationCallOrder[0])
})

it('focuses on container padding clicks without intercepting the textarea or send button', () => {
  const element = MessageInput({ value: 'Draft', disabled: false, sending: false, onChange: vi.fn(), onSend: vi.fn() })
  const box = element.props.children
  const event = { target: { closest: vi.fn<() => object | null>(() => null) }, preventDefault: vi.fn() }
  box.props.onPointerDown(event)
  expect(event.preventDefault).toHaveBeenCalledOnce()
  expect(captured.focus).toHaveBeenCalledWith({ cursor: 'end' })

  event.target.closest.mockReturnValue({})
  box.props.onPointerDown(event)
  expect(captured.focus).toHaveBeenCalledOnce()
})

it('does not focus a disabled composer on container clicks', () => {
  const element = MessageInput({ value: '', disabled: true, sending: false, onChange: vi.fn(), onSend: vi.fn() })
  element.props.children.props.onPointerDown({ target: { closest: () => null }, preventDefault: vi.fn() })
  expect(captured.focus).not.toHaveBeenCalled()
})
