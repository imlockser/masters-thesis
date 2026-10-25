import { describe, expect, it } from 'vitest'
import { applyEvent, startRun } from './run'

describe('agent event state', () => {
  it('keeps separate generations, replaces deltas, and ignores replayed events', () => {
    const initial = startRun('run-1', '/events')
    let state = applyEvent(initial, {
      sequence: 0,
      type: 'run_started',
      chat_id: 'chat-1',
    })
    state = applyEvent(state, {
      sequence: 1,
      type: 'text_delta',
      message_index: 12,
      text: 'Checking ',
    })
    const delta = {
      sequence: 2,
      type: 'text_delta' as const,
      message_index: 12,
      text: 'weather.',
    }
    state = applyEvent(state, delta)
    expect(applyEvent(state, delta)).toBe(state)

    state = applyEvent(state, {
      sequence: 3,
      type: 'message_completed',
      message_index: 12,
      message: {
        role: 'assistant',
        content: 'Checking weather.',
        tool_calls: [
          {
            id: 'call-1',
            type: 'function',
            function: { name: 'forecast_solar_generation', arguments: '{}' },
          },
        ],
      },
    })
    state = applyEvent(state, {
      sequence: 4,
      type: 'tool_started',
      tool_call_id: 'call-1',
      name: 'forecast_solar_generation',
    })
    expect(state.activeTools).toEqual(['call-1'])
    state = applyEvent(state, {
      sequence: 5,
      type: 'tool_completed',
      tool_call_id: 'call-1',
      name: 'forecast_solar_generation',
      message_index: 13,
      message: {
        role: 'tool',
        tool_call_id: 'call-1',
        content: 'Total forecast energy: 42 MWh.',
      },
      artifact: {
        tool_call_id: 'call-1',
        name: 'forecast_solar_generation',
        data: { total_energy_mwh: 42 },
      },
    })
    state = applyEvent(state, {
      sequence: 6,
      type: 'text_delta',
      message_index: 14,
      text: '42 MWh.',
    })

    expect(state.activeTools).toEqual([])
    expect(state.messages[12].content).toBe('Checking weather.')
    expect(state.messages[13].content).toBe('Total forecast energy: 42 MWh.')
    expect(state.messages[14].content).toBe('42 MWh.')
    expect(JSON.stringify(state.messages)).not.toContain('total_energy_mwh')
    expect(initial.messages).toEqual({})
  })

  it('ends a failed run without accepting an older replay', () => {
    const state = applyEvent(startRun('run-1', '/events'), {
      sequence: 4,
      type: 'run_failed',
      error: 'ConnectionError',
    })
    expect(state.phase).toBe('failed')
    expect(
      applyEvent(state, {
        sequence: 0,
        type: 'run_started',
        chat_id: 'chat-1',
      }),
    ).toBe(state)
  })
})
