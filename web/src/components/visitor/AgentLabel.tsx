import { useState } from 'react'

type AgentLabelProps = {
  icon: string
  value?: string | null
  format?: (value: string) => string
}

export function AgentLabel({ icon, value, format }: AgentLabelProps) {
  const [failedIcon, setFailedIcon] = useState<string>()
  const name = value?.trim() || 'Unknown'

  return (
    <span className="flex items-center gap-2 whitespace-nowrap">
      {failedIcon !== icon ? (
        <img
          alt=""
          aria-hidden="true"
          className="size-4 shrink-0 object-contain"
          decoding="async"
          height={16}
          loading="lazy"
          src={icon}
          width={16}
          onError={() => setFailedIcon(icon)}
        />
      ) : null}
      <span>{format && name !== 'Unknown' ? format(name) : name}</span>
    </span>
  )
}
