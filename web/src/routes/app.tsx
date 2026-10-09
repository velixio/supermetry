import { Outlet, createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/app')({
  component: AppEntry,
})

function AppEntry() {
  return <Outlet />
}
