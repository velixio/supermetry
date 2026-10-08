import { osIcon } from '@/lib/agent-icons'

import { AgentLabel } from './AgentLabel'

export function OsLabel({ value }: { value?: string | null }) {
  return <AgentLabel icon={osIcon(value ?? '')} value={value} />
}
