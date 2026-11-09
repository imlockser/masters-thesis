import type { AgentEvent } from './types'

const types: ReadonlySet<string> = new Set([
  'run_started',
  'generation_started',
  'text_delta',
  'message_completed',
  'tool_started',
  'tool_completed',
  'run_completed',
  'run_failed',
])

export function listenToRun(
  url: string,
  getAccessToken: () => string | undefined,
  onEvent: (event: AgentEvent) => void,
  onConnection: (status: 'open' | 'reconnecting') => void,
  onFailure: (message: string) => void,
): () => void {
  const controller = new AbortController()
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let lastSequence = -1

  function close() {
    controller.abort()
    clearTimeout(retryTimer)
    void reader?.cancel().catch(() => {})
  }

  function fail(message: string) {
    close()
    onFailure(message)
  }

  async function connect() {
    if (controller.signal.aborted) return
    const token = getAccessToken()
    if (!token) {
      fail('An access token is required to call the backend.')
      return
    }

    try {
      const headers = new Headers({
        Accept: 'text/event-stream',
        Authorization: `Bearer ${token}`,
      })
      if (lastSequence >= 0) headers.set('Last-Event-ID', String(lastSequence))

      const response = await fetch(url, {
        headers,
        signal: controller.signal,
        cache: 'no-store',
      })
      reader = response.body?.getReader()
      if (controller.signal.aborted) return

      if (!response.ok) {
        if (response.status >= 500) throw new Error('Agent stream unavailable.')
        fail(`The agent stream could not be opened (HTTP ${response.status}).`)
        return
      }
      if (
        !reader ||
        response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase() !== 'text/event-stream'
      ) {
        fail('The backend did not return an agent event stream.')
        return
      }
      onConnection('open')

      const decoder = new TextDecoder()
      let buffer = ''
      let skipLF = false
      let type = ''
      let data: string[] = []
      let eventId = ''
      let reconnect = false

      function line(value: string) {
        if (value === '') {
          if (type === 'stream_error') {
            reconnect = true
          } else if (types.has(type) && data.length) {
            try {
              const event = JSON.parse(data.join('\n')) as AgentEvent
              if (
                event?.type !== type ||
                !Number.isSafeInteger(event.sequence) ||
                event.sequence < 0 ||
                (eventId !== '' && eventId !== String(event.sequence))
              ) {
                throw new Error('Invalid agent event.')
              }

              if (event.sequence > lastSequence) {
                lastSequence = event.sequence
                if (type === 'run_completed' || type === 'run_failed') close()
                onEvent(event)
              }
            } catch {
              fail('The agent response could not be read. Reload the chat to recover it.')
            }
          }
          type = ''
          data = []
          eventId = ''
          return
        }

        const colon = value.indexOf(':')
        const field = colon < 0 ? value : value.slice(0, colon)
        const content = colon < 0 ? '' : value.slice(colon + 1).replace(/^ /, '')
        if (field === 'event') type = content
        else if (field === 'data') data.push(content)
        else if (field === 'id') eventId = content
      }

      while (!controller.signal.aborted && !reconnect) {
        const { value, done } = await reader.read()
        if (controller.signal.aborted) return
        let chunk = decoder.decode(value, { stream: !done })
        if (chunk) {
          // CR ends a line immediately; its paired LF can arrive in the next chunk.
          const endsWithCR = chunk.endsWith('\r')
          if (skipLF && chunk.startsWith('\n')) chunk = chunk.slice(1)
          skipLF = endsWithCR
          buffer += chunk.replace(/\r\n?/g, '\n')
        }

        const lines = buffer.split('\n')
        buffer = lines.pop()!
        for (const value of lines) {
          line(value)
          if (controller.signal.aborted) return
          if (reconnect) break
        }
        if (done) break
      }
    } catch {
      if (controller.signal.aborted) return
    } finally {
      await reader?.cancel().catch(() => {})
      reader?.releaseLock()
      reader = undefined
    }

    if (!controller.signal.aborted) {
      onConnection('reconnecting')
      if (!controller.signal.aborted)
        retryTimer = setTimeout(() => void connect(), 3000)
    }
  }

  void connect()
  return close
}
