import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'

import { queryClient } from './lib/queryClient'
import { router } from './router'
import { installSessionLifecycle } from './lib/session-lifecycle'
import './index.css'

installSessionLifecycle()

const root = document.getElementById('root')
if (!root) {
  throw new Error('Missing application root')
}

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
