import { CheckIcon } from 'lucide-react'

import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { cn } from '@/lib/utils'
import { uiFlows } from '@/messages/uiFlows'

/** The searchable list inside the time-zone picker; loaded on first open so cmdk stays out of the main bundle. */
export default function TimezoneList({ id, zones, current, onChoose }: { id: string; zones: string[]; current: string; onChoose: (zone: string) => void }) {
  return (
    <Command>
      <CommandInput placeholder={uiFlows.site.timezoneSearch} aria-label={uiFlows.site.timezoneSearch} />
      <CommandList id={id}>
        <CommandEmpty>{uiFlows.site.timezoneEmpty}</CommandEmpty>
        {zones.map((zone) => (
          <CommandItem key={zone} value={zone} keywords={[zone.replace(/_/g, ' ')]} onSelect={() => onChoose(zone)}>
            <CheckIcon aria-hidden="true" className={cn(zone === current ? 'opacity-100' : 'opacity-0')} />
            {zone.replace(/_/g, ' ')}
          </CommandItem>
        ))}
      </CommandList>
    </Command>
  )
}
