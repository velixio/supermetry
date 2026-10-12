import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'

import { queryClient } from './lib/queryClient'
import { router } from './router'
import { initI18n, syncAccountLocale } from './lib/i18n'
import { installSessionLifecycle } from './lib/session-lifecycle'
import './index.css'

installSessionLifecycle()

// Follow the account's language once the profile loads.
queryClient.getQueryCache().subscribe((event) => {
  if (event.type !== 'updated' || event.action.type !== 'success' || event.query.queryKey[0] !== 'me') return
  const data = event.query.state.data as { preferences?: { locale?: string } } | undefined
  syncAccountLocale(data?.preferences?.locale)
})

// Load the chosen language's messages before the first render so nothing flashes in English.
void initI18n().finally(() => {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </StrictMode>,
  )
})
