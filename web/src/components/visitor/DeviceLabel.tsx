import { deviceIcon } from '@/lib/agent-icons'

import { AgentLabel } from './AgentLabel'

function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

export function DeviceLabel({ value }: { value?: string | null }) {
  return <AgentLabel format={capitalize} icon={deviceIcon(value ?? '')} value={value} />
}
