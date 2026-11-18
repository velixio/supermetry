import { registerCatalogue } from '@/lib/i18n'

// Strings of the shared foundation components (skip link, confirm dialog, reauth dialog).
export const uiFoundation = {
  skipToContent: 'Skip to content',
  confirm: {
    cancel: 'Cancel',
    typeToConfirm: 'Type {name} to confirm',
  },
  reauth: {
    title: 'Confirm it is you',
    description: 'Enter your password to continue with this sensitive action.',
    password: 'Password',
    totpCode: 'Authentication code',
    totpHint: 'Six-digit code from your authenticator app.',
    submit: 'Confirm',
    submitting: 'Confirming…',
    cancel: 'Cancel',
    invalid: 'That password or code is not correct. Try again.',
    failed: 'Could not confirm your identity. Try again in a moment.',
  },
}

registerCatalogue('uiFoundation', uiFoundation)
