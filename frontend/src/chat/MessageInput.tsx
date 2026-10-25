import { SendOutlined } from '@ant-design/icons'
import { Button, Flex, Input, Typography } from 'antd'

interface Props {
  value: string
  disabled: boolean
  sending: boolean
  onChange: (value: string) => void
  onSend: () => void
}

export function MessageInput({
  value,
  disabled,
  sending,
  onChange,
  onSend,
}: Props) {
  return (
    <form
      className="composer"
      onSubmit={(event) => {
        event.preventDefault()
        onSend()
      }}
    >
      <Input.TextArea
        aria-label="Message"
        placeholder="Ask about solar generation in Germany…"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        autoSize={{ minRows: 2, maxRows: 6 }}
        disabled={disabled}
        onKeyDown={(event) => {
          if (
            event.key === 'Enter' &&
            !event.shiftKey &&
            !event.nativeEvent.isComposing
          ) {
            event.preventDefault()
            if (value.trim() && !disabled) onSend()
          }
        }}
      />
      <Flex
        justify="space-between"
        align="center"
        gap="small"
        className="composer-footer"
      >
        <Typography.Text type="secondary" className="keyboard-hint">
          Enter to send · Shift+Enter for a new line
        </Typography.Text>
        <Button
          aria-label="Send"
          type="primary"
          htmlType="submit"
          icon={<SendOutlined />}
          loading={sending}
          disabled={disabled || !value.trim()}
        >
          Send
        </Button>
      </Flex>
    </form>
  )
}
