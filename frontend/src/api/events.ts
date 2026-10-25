import type { AgentEvent } from './types'

export function listenToRun(
  url: string,
  onEvent: (event: AgentEvent) => void,
  onConnection: (status: 'open' | 'reconnecting') => void,
  onFailure: (message: string) => void,
): () => void {
  const source = new EventSource(url)
  const types = [
    'run_started',
    'generation_started',
    'text_delta',
    'message_completed',
    'tool_started',
    'tool_completed',
    'run_completed',
    'run_failed',
  ] as const

  for (const type of types) {
    source.addEventListener(type, (event: MessageEvent<string>) => {
      try {
        const data = JSON.parse(event.data) as AgentEvent
        if (
          data.type !== type ||
          !Number.isInteger(data.sequence) ||
          data.sequence < 0
        ) {
          throw new Error('Invalid agent event.')
        }

        if (type === 'run_completed' || type === 'run_failed') source.close()
        onEvent(data)
      } catch {
        source.close()
        onFailure(
          'The agent response could not be read. Reload the conversation to recover it.',
        )
      }
    })
  }

  source.onopen = () => onConnection('open')
  source.addEventListener('stream_error', () => onConnection('reconnecting'))
  source.onerror = () => {
    if (source.readyState === EventSource.CLOSED) {
      onFailure(
        'The agent stream closed. Reload the conversation to recover it.',
      )
    } else {
      onConnection('reconnecting')
    }
  }

  return () => source.close()
}
