import { SendOutlined } from '@ant-design/icons'
import { Button, Input, type GetRef } from 'antd'
import { useEffect, useRef } from 'react'

interface Props {
  value: string
  disabled: boolean
  sending: boolean
  onChange: (value: string) => void
  onSend: () => void
}

function hasOpenOverlay() {
  return Array.from(document.querySelectorAll('[role="dialog"], [role="menu"], [role="listbox"]'))
    .some((element) => !element.closest('.chat-navigation') &&
      element.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true }))
}

export function MessageInput({
  value,
  disabled,
  sending,
  onChange,
  onSend,
}: Props) {
  const input = useRef<GetRef<typeof Input.TextArea>>(null)
  const inactive = disabled || sending

  useEffect(() => {
    if (inactive) return

    function startTyping(event: KeyboardEvent) {
      if (event.defaultPrevented || event.isComposing || event.ctrlKey || event.metaKey || event.altKey ||
          (event.key.length !== 1 && event.key !== 'Enter')) return

      const target = event.target as HTMLElement | null
      if (target?.closest?.('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], [role="combobox"]')) return
      if ((event.key === ' ' || event.key === 'Enter') &&
          (!value.trim() || target?.closest?.('button, a, [role="button"], [role="tab"]'))) return
      if (hasOpenOverlay()) return

      event.preventDefault()
      if (event.key === 'Enter' && !event.shiftKey) {
        input.current?.blur()
        onSend()
      } else {
        input.current?.focus({ cursor: 'end' })
        onChange(value + (event.key === 'Enter' ? '\n' : event.key))
      }
    }

    window.addEventListener('keydown', startTyping)
    return () => window.removeEventListener('keydown', startTyping)
  }, [inactive, value, onChange, onSend])

  return (
    <form
      className="composer"
      onSubmit={(event) => {
        event.preventDefault()
        if (!inactive && value.trim()) {
          event.currentTarget.querySelector<HTMLElement>(':focus')?.blur()
          onSend()
        }
      }}
    >
      <div
        className="composer-box"
        onPointerDown={(event) => {
          if (inactive || (event.target as HTMLElement).closest('textarea, button')) return
          event.preventDefault()
          input.current?.focus({ cursor: 'end' })
        }}
      >
        <div className="composer-field">
          <Input.TextArea
            ref={input}
            variant="borderless"
            aria-label="Message"
            placeholder="Ask about solar generation in Germany…"
            value={value}
            onChange={(event) => onChange(event.target.value)}
            autoSize={{ minRows: 1, maxRows: 6 }}
            disabled={inactive}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing) return
              if ((event.key === ' ' || event.key === 'Enter') && !value.trim()) {
                event.preventDefault()
                return
              }
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault()
                if (!inactive) {
                  input.current?.blur()
                  onSend()
                }
              }
            }}
          />
        </div>
        <Button
          aria-label="Send"
          title="Send"
          className="composer-send"
          style={{ border: 0, boxShadow: 'none' }}
          type="primary"
          shape="circle"
          htmlType="submit"
          icon={<SendOutlined />}
          loading={sending}
          disabled={inactive || !value.trim()}
        />
      </div>
    </form>
  )
}
