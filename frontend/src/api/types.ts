export interface Chat {
  id: string
  user_id: string
  title: string
  created_at: string
}

export interface ToolCall {
  id: string
  type: 'function'
  function: { name: string; arguments: string }
}

export interface Message {
  role: 'user' | 'assistant' | 'tool'
  content?: string | { type: string; text?: string }[] | null
  tool_calls?: ToolCall[]
  tool_call_id?: string
}

export interface StoredMessage {
  id: number
  chat_id: string
  role: Message['role']
  payload: Omit<Message, 'role'>
  created_at: string
}

export interface ChatDetails extends Chat {
  messages: StoredMessage[]
  active_run_id?: string
}

export interface StartedRun {
  run_id: string
  events_url: string
}

export interface Artifact {
  tool_call_id: string
  name: string
  data: Record<string, unknown>
}

export type AgentEvent = { sequence: number } & (
  | { type: 'run_started'; chat_id: string }
  | { type: 'generation_started'; message_index: number }
  | { type: 'text_delta'; message_index: number; text: string }
  | { type: 'message_completed'; message_index: number; message: Message }
  | { type: 'tool_started'; tool_call_id: string; name: string }
  | {
      type: 'tool_completed'
      tool_call_id: string
      name: string
      message_index: number
      message: Message
      artifact: Artifact | null
    }
  | {
      type: 'run_completed'
      result: { chat_id: string; messages: Message[]; artifacts: Artifact[] }
    }
  | { type: 'run_failed'; error: string }
)

export function messageText(message: Message): string {
  if (typeof message.content === 'string') return message.content

  return (
    message.content
      ?.filter((part) => part.type === 'text')
      .map((part) => part.text ?? '')
      .join('\n') ?? ''
  )
}
