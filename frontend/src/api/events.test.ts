import { afterEach, expect, it, vi } from 'vitest'
import { listenToRun } from './events'

class FakeSource extends EventTarget {
  static CLOSED = 2
  static instance: FakeSource
  readyState = 1
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn(() => {
    this.readyState = FakeSource.CLOSED
  })

  constructor() {
    super()
    FakeSource.instance = this
  }

  emit(type: string, data: unknown) {
    this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }))
  }
}

afterEach(() => vi.unstubAllGlobals())

it('leaves reconnects to EventSource and closes after a terminal event', () => {
  vi.stubGlobal('EventSource', FakeSource)
  const onEvent = vi.fn()
  const onConnection = vi.fn()
  const onFailure = vi.fn()
  const close = listenToRun('/events', onEvent, onConnection, onFailure)
  const source = FakeSource.instance

  source.onerror?.()
  expect(onConnection).toHaveBeenCalledWith('reconnecting')
  expect(source.close).not.toHaveBeenCalled()
  source.emit('run_failed', {
    sequence: 3,
    type: 'run_failed',
    error: 'ConnectionError',
  })
  expect(onEvent).toHaveBeenCalledOnce()
  expect(source.close).toHaveBeenCalledOnce()
  expect(onFailure).not.toHaveBeenCalled()
  close()
})

it('rejects malformed stream data without displaying it as a message', () => {
  vi.stubGlobal('EventSource', FakeSource)
  const onEvent = vi.fn()
  const onFailure = vi.fn()
  listenToRun('/events', onEvent, vi.fn(), onFailure)
  FakeSource.instance.emit('text_delta', { type: 'text_delta', sequence: -1 })
  expect(onEvent).not.toHaveBeenCalled()
  expect(onFailure).toHaveBeenCalledOnce()
  expect(FakeSource.instance.close).toHaveBeenCalledOnce()
})
