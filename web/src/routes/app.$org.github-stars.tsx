import { createFileRoute, Outlet } from '@tanstack/react-router'

// An organization page like Sites and Health: every member of the organization may open it.
// The server decides what each role may change.
export const Route = createFileRoute('/app/$org/github-stars')({ component: Outlet })
