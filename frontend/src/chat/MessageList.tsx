import { LoadingOutlined, SunOutlined, ToolOutlined } from '@ant-design/icons'
import { Card, Collapse, Space, Tag, Typography } from 'antd'
import { useEffect, useRef } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { messageText } from '../api/types'
import type { ChatDetails, Message } from '../api/types'
import type { RunState } from './run'

function toolName(name: string): string {
  if (name === 'forecast_solar_generation') return 'Solar forecast'
  if (name === 'find_best_solar_window') return 'Best solar window'
  if (name === 'get_electricity_prices') return 'Day-ahead electricity prices'
  if (name === 'get_generation_mix') return 'Historical generation mix'
  if (name === 'remember') return 'User memory'
  return name.replaceAll('_', ' ')
}

interface MarkdownNode {
  type: string
  value?: string
  children?: MarkdownNode[]
}

function preserveLineBreaks() {
  function visit(node: MarkdownNode) {
    if (!node.children) return

    node.children = node.children.flatMap((child): MarkdownNode[] => {
      visit(child)
      if (child.type === 'html' && /^<br\s*\/?>$/i.test(child.value ?? ''))
        return [{ type: 'break' }]

      if (child.type !== 'text' || !child.value?.includes('\n')) return [child]

      return child.value.split('\n').flatMap((value, index) =>
        index === 0
          ? [{ type: 'text', value }]
          : [{ type: 'break' }, { type: 'text', value }],
      )
    })
  }

  return visit
}

export function MessageList({
  chat,
  run,
}: {
  chat: ChatDetails
  run: RunState | null
}) {
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
      aria-label="Chat"
      onScroll={() => {
        const element = container.current
        if (element)
          follow.current =
            element.scrollHeight - element.scrollTop - element.clientHeight < 96
      }}
    >
      <div className="message-column">
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
                    {message.role === 'user' ? 'You' : 'Agent'}
                  </Typography.Text>
                  <Card size="small">
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
                          <Markdown
                            remarkPlugins={[remarkGfm, preserveLineBreaks]}
                            skipHtml
                            components={{
                              table: ({ children }) => (
                                <div className="markdown-table" tabIndex={0} role="region" aria-label="Table">
                                  <table>{children}</table>
                                </div>
                              ),
                            }}
                          >
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
                const name = call.function.name
                const solar = name === 'forecast_solar_generation' || name === 'find_best_solar_window'

                return (
                  <Collapse
                    key={call.id}
                    size="small"
                    className="tool-details"
                    expandIcon={({ isActive }) => (
                      <span className={isActive ? 'chevron' : 'chevron chevron-right'} aria-hidden="true" />
                    )}
                    items={[
                      {
                        key: call.id,
                        label: (
                          <Space>
                            <span className={solar ? 'tool-icon-solar' : undefined} aria-hidden="true">
                              {active ? <LoadingOutlined spin /> : solar ? <SunOutlined /> : <ToolOutlined />}
                            </span>
                            <span className="tool-name">{toolName(name)}</span>
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
