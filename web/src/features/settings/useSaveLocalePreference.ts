import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { applyLocalePreference, type LocaleCode } from '@/lib/i18n'
import { ui } from '@/messages/ui'
import { privateRequest } from './privateApi'

type Request = (method: 'PATCH', path: string, body: Record<string, unknown>) => Promise<unknown>

const defaultRequest: Request = (method, path, body) => privateRequest(method, path, { body })

/** Saves the interface language to the account, then applies it (which reloads the app). */
export function useSaveLocalePreference(request: Request = defaultRequest) {
  const queryClient = useQueryClient()
  const mutation = useMutation({
    mutationFn: (value: LocaleCode | '') => request('PATCH', '/api/private/me', { preferences: { locale: value } }),
    onSuccess: (_, value) => {
      void queryClient.invalidateQueries({ queryKey: ['me'] })
      applyLocalePreference(value)
    },
    onError: () => { toast.error(ui.language.saveFailed) },
  })
  return { save: (value: LocaleCode | '') => mutation.mutate(value), isPending: mutation.isPending }
}
