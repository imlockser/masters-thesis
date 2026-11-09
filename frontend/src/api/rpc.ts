import { useCallback } from 'react'
import { useAccessToken } from '../auth/useAccessToken'
import type { Chat, ChatDetails, StartedRun } from './types'

interface Methods {
  CreateChat: { params: { title: string }; result: Chat }
  ListChats: { params: Record<string, never>; result: Chat[] }
  GetChat: { params: { chat_id: string }; result: ChatDetails }
  RenameChat: { params: { chat_id: string; new_title: string }; result: Chat }
  DeleteChat: { params: { chat_id: string }; result: null }
  DeleteAllChats: { params: Record<string, never>; result: null }
  SendMessage: {
    params: { request_id: string; chat_id: string; content: string }
    result: StartedRun
  }
}

export class RpcError extends Error {
  code: number

  constructor(code: number, message: string) {
    super(message)
    this.code = code
  }
}

export async function rpc<M extends keyof Methods>(
  method: M,
  params: Methods[M]['params'],
  accessToken: string | undefined,
  signal?: AbortSignal,
): Promise<Methods[M]['result']> {
  if (!accessToken)
    throw new Error('An access token is required to call the backend.')

  const id = crypto.randomUUID()
  const response = await fetch('/rpc', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${accessToken}`,
    },
    body: JSON.stringify({
      jsonrpc: '2.0',
      id,
      method,
      params,
    }),
    signal,
  })

  if (!response.ok)
    throw new Error(`Backend request failed (HTTP ${response.status}).`)

  const payload = await response.json()
  if (payload.jsonrpc !== '2.0' || payload.id !== id)
    throw new Error('Invalid backend response.')
  if (payload.error)
    throw new RpcError(payload.error.code, payload.error.message)
  if (!('result' in payload)) throw new Error('Backend returned no result.')

  return payload.result
}

export function useRpc() {
  const getAccessToken = useAccessToken()

  return useCallback(
    <M extends keyof Methods>(
      method: M,
      params: Methods[M]['params'],
      signal?: AbortSignal,
    ) => {
      return rpc(method, params, getAccessToken(), signal)
    },
    [getAccessToken],
  )
}
