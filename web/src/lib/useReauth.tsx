import { useCallback, useMemo, useRef, useState, type ReactElement } from 'react'

import { ReauthDialog } from '@/components/ReauthDialog'

import { isReauthRequired, postReauth, type ReauthSubmit } from './reauth'

/**
 * Wraps a sensitive request. If it fails with code "reauth_required", opens the password dialog and,
 * once the server accepts it, retries the request exactly once. Cancelling rejects with the original error.
 *
 *   const { withReauth, reauthDialog } = useReauth()
 *   const revoke = (id) => withReauth(() => revokeKey.mutateAsync(id))
 *   return <>{...}{reauthDialog}</>
 */
export function useReauth(submit: ReauthSubmit = postReauth, options: { requireTotp?: boolean } = {}): {
  withReauth: <T>(run: () => Promise<T>) => Promise<T>
  reauthDialog: ReactElement
} {
  const [open, setOpen] = useState(false)
  const pending = useRef<{ resolve: () => void; reject: (e: unknown) => void } | null>(null)
  const original = useRef<unknown>(null)

  const withReauth = useCallback(async function withReauth<T>(run: () => Promise<T>): Promise<T> {
    try {
      return await run()
    } catch (error) {
      if (!isReauthRequired(error)) throw error
      original.current = error
      await new Promise<void>((resolve, reject) => {
        pending.current = { resolve, reject }
        setOpen(true)
      })
      return run()
    }
  }, [])

  const reauthDialog = useMemo(
    () => (
      <ReauthDialog
        open={open}
        requireTotp={options.requireTotp}
        onSubmit={async (values) => {
          await submit(values)
          setOpen(false)
          pending.current?.resolve()
          pending.current = null
        }}
        onCancel={() => {
          setOpen(false)
          pending.current?.reject(original.current)
          pending.current = null
        }}
      />
    ),
    [open, submit, options.requireTotp],
  )

  return { withReauth, reauthDialog }
}
