import { browserIcon } from '@/lib/agent-icons'

import { AgentLabel } from './AgentLabel'

export function BrowserLabel({ value }: { value?: string | null }) {
  return <AgentLabel icon={browserIcon(value ?? '')} value={value} />
}
