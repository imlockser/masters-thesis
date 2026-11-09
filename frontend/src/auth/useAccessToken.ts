import { useCallback, useLayoutEffect, useRef } from 'react'
import { useAuth } from 'react-oidc-context'

export function useAccessToken() {
  const auth = useAuth()
  const currentAuth = useRef(auth)

  // Pending requests read renewed tokens without restarting chat effects.
  useLayoutEffect(() => {
    currentAuth.current = auth
  }, [auth])

  return useCallback(() => {
    const { isLoading, isAuthenticated, user } = currentAuth.current
    return !isLoading && isAuthenticated ? user?.access_token : undefined
  }, [])
}
