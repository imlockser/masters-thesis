import { LoadingOutlined, ToolOutlined } from '@ant-design/icons'
import { Card, Collapse, Empty, Space, Tag, Typography, theme } from 'antd'
import { useEffect, useRef } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { messageText } from '../api/types'
import type { ChatDetails, Message } from '../api/types'
import type { RunState } from './run'

function toolName(name: string): string {
  if (name === 'forecast_solar_generation') return 'Solar forecast'
  if (name === 'remember') return 'User memory'
  return name.replaceAll('_', ' ')
}

export function MessageList({
  chat,
  run,
}: {
  chat: ChatDetails
  run: RunState | null
}) {
  const { token } = theme.useToken()
  const container = useRef<HTMLDivElement>(null)
  const follow = useRef(true)
  const rows: { key: string; message: Message }[] = [
    ...chat.messages.map((item) => ({
      key: `stored:${item.id}`,
      message: { role: item.role, ...item.payload },
    })),
    ...Object.entries(run?.messages ?? {})
      .sort(([a], [b]) => Number(a) - Number(b))
      .map(([index, message]) => ({ key: `${run?.id}:${index}`, message })),
  ]
  const results = new Map(
    rows
      .filter((row) => row.message.role === 'tool')
      .map((row) => [row.message.tool_call_id, row.message]),
  )

  useEffect(() => {
    follow.current = true
  }, [chat.id])

  useEffect(() => {
    if (follow.current && container.current)
      container.current.scrollTop = container.current.scrollHeight
  }, [chat.messages, run])

  return (
    <div
      ref={container}
      className="message-scroll"
      role="log"
      aria-label="Conversation"
      onScroll={() => {
        const element = container.current
        if (element)
          follow.current =
            element.scrollHeight - element.scrollTop - element.clientHeight < 96
      }}
    >
      <div className="message-column">
        {rows.length === 0 && (
          <div className="conversation-empty">
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="Ask about Germany's solar generation"
            />
            <Typography.Paragraph type="secondary">
              Try asking for a forecast for the next six hours.
            </Typography.Paragraph>
          </div>
        )}
        {rows.map(({ key, message }) => {
          if (message.role === 'tool') return null
          const text = messageText(message)
          const generating =
            !text && !message.tool_calls?.length && run?.phase === 'running'

          return (
            <article key={key} className={`message message-${message.role}`}>
              {(text || generating) && (
                <>
                  <Typography.Text type="secondary" className="message-label">
                    {message.role === 'user' ? 'You' : 'Assistant'}
                  </Typography.Text>
                  <Card
                    size="small"
                    style={
                      message.role === 'user'
                        ? { background: token.colorFillQuaternary }
                        : undefined
                    }
                  >
                    {generating ? (
                      <Space>
                        <LoadingOutlined spin />
                        <Typography.Text type="secondary">
                          Generating…
                        </Typography.Text>
                      </Space>
                    ) : (
                      <div
                        className={
                          message.role === 'assistant'
                            ? 'markdown'
                            : 'user-text'
                        }
                      >
                        {message.role === 'assistant' ? (
                          <Markdown remarkPlugins={[remarkGfm]} skipHtml>
                            {text}
                          </Markdown>
                        ) : (
                          text
                        )}
                      </div>
                    )}
                  </Card>
                </>
              )}
              {message.tool_calls?.map((call) => {
                const result = results.get(call.id)
                const active = run?.activeTools.includes(call.id)
                const failed =
                  !!result && messageText(result).startsWith('Tool error:')

                return (
                  <Collapse
                    key={call.id}
                    size="small"
                    className="tool-details"
                    items={[
                      {
                        key: call.id,
                        label: (
                          <Space>
                            {active ? (
                              <LoadingOutlined spin />
                            ) : (
                              <ToolOutlined />
                            )}
                            {toolName(call.function.name)}
                            <Tag
                              color={
                                failed
                                  ? 'error'
                                  : result
                                    ? 'success'
                                    : 'processing'
                              }
                            >
                              {failed
                                ? 'Error'
                                : result
                                  ? 'Done'
                                  : active
                                    ? 'Running'
                                    : 'Waiting'}
                            </Tag>
                          </Space>
                        ),
                        children: (
                          <Typography.Paragraph className="tool-result">
                            {result
                              ? messageText(result)
                              : 'Waiting for the tool result…'}
                          </Typography.Paragraph>
                        ),
                      },
                    ]}
                  />
                )
              })}
            </article>
          )
        })}
      </div>
    </div>
  )
}
