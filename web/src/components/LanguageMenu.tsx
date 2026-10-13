import { LanguagesIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useSaveLocalePreference } from '@/features/settings/useSaveLocalePreference'
import { manifest, pickerLocales, type LocaleCode } from '@/lib/i18n'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

/** Header button that switches the interface language the same way the account profile card does. */
export function LanguageMenu({ locale }: { locale: LocaleCode | '' }) {
  const { save, isPending } = useSaveLocalePreference()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" size="icon" variant="ghost" aria-label={ui.language.menu} disabled={isPending}>
          <LanguagesIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup value={locale} onValueChange={(value) => save(value as LocaleCode | '')}>
          <DropdownMenuRadioItem value="" disabled={isPending}>{en.language.browserDefault}</DropdownMenuRadioItem>
          {pickerLocales.map((code) => (
            <DropdownMenuRadioItem key={code} value={code} disabled={isPending}>
              {manifest[code].nativeName}{manifest[code].reviewed ? '' : ` ${en.language.machineTranslated}`}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
