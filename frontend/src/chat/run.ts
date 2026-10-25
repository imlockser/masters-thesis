import type { AgentEvent, Message } from '../api/types'

export interface RunState {
  id: string
  url: string
  sequence: number
  phase: 'waiting' | 'running' | 'completed' | 'failed'
  messages: Record<number, Message>
  activeTools: string[]
  error?: string
}

export function startRun(id: string, url: string): RunState {
  return {
    id,
    url,
    sequence: -1,
    phase: 'waiting',
    messages: {},
    activeTools: [],
  }
}

export function applyEvent(state: RunState, event: AgentEvent): RunState {
  if (event.sequence <= state.sequence) return state

  const next = {
    ...state,
    sequence: event.sequence,
    messages: { ...state.messages },
  }

  switch (event.type) {
    case 'run_started':
      next.phase = 'running'
      break
    case 'generation_started':
      next.messages[event.message_index] = { role: 'assistant', content: '' }
      break
    case 'text_delta': {
      const previous = next.messages[event.message_index]?.content
      next.messages[event.message_index] = {
        role: 'assistant',
        content: (typeof previous === 'string' ? previous : '') + event.text,
      }
      break
    }
    case 'message_completed':
      next.messages[event.message_index] = event.message
      break
    case 'tool_started':
      next.activeTools = [...state.activeTools, event.tool_call_id]
      break
    case 'tool_completed':
      next.messages[event.message_index] = event.message
      next.activeTools = state.activeTools.filter(
        (id) => id !== event.tool_call_id,
      )
      break
    case 'run_completed':
      next.phase = 'completed'
      next.activeTools = []
      break
    case 'run_failed':
      next.phase = 'failed'
      next.activeTools = []
      next.error =
        'The agent could not complete this request. You can send another message.'
      break
  }

  return next
}
