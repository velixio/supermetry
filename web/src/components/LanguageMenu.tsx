import { LanguagesIcon } from 'lucide-react'

import {
  DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger,
} from '@/components/ui/dropdown-menu'
import { useSaveLocalePreference } from '@/features/settings/useSaveLocalePreference'
import { manifest, pickerLocales, type LocaleCode } from '@/lib/i18n'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

/** Account-menu submenu that switches the interface language the same way the account profile card does. */
export function LanguageSubmenu({ locale }: { locale: LocaleCode | '' }) {
  const { save, isPending } = useSaveLocalePreference()
  // A stored value the picker does not offer shows as Browser default, like the profile card's select.
  const current: LocaleCode | '' = (pickerLocales as readonly string[]).includes(locale) ? locale : ''
  return (
    <DropdownMenuSub>
      <DropdownMenuSubTrigger disabled={isPending}>
        <LanguagesIcon aria-hidden="true" />
        {ui.language.menu}
      </DropdownMenuSubTrigger>
      <DropdownMenuSubContent>
        <DropdownMenuRadioGroup value={current} onValueChange={(value) => { if (value !== current) save(value as LocaleCode | '') }}>
          <DropdownMenuRadioItem value="" disabled={isPending}>{en.language.browserDefault}</DropdownMenuRadioItem>
          {pickerLocales.map((code) => (
            <DropdownMenuRadioItem key={code} value={code} disabled={isPending}>
              {manifest[code].nativeName}{manifest[code].reviewed ? '' : ` ${en.language.machineTranslated}`}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuSubContent>
    </DropdownMenuSub>
  )
}
