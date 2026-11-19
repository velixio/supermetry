import type { MessageTree } from '@/lib/i18n/catalogue'

// BN translation of messages/contextPanel.ts. Machine-assisted first translation (reviewed: false in the manifest).
const messages: MessageTree = {
  label: 'পৃষ্ঠার প্রসঙ্গ',
  shortcutName: 'পৃষ্ঠার প্রসঙ্গ',
  sections: { quick: 'দ্রুত অ্যাক্সেস', recommended: 'প্রস্তাবিত', related: 'সম্পর্কিত', recent: 'সাম্প্রতিক' },
  savedDashboard: (name: string) => `ড্যাশবোর্ড: ${name}`,
  savedSegment: (name: string) => `সেগমেন্ট: ${name}`,
  recommend: {
    install: 'ট্র্যাকার ইনস্টল করুন',
    createGoal: 'লক্ষ্য তৈরি করুন',
    recordingOff: 'রেকর্ডিং চালু করুন',
    rejected: 'প্রত্যাখ্যাত ইভেন্ট দেখুন',
  },
  labels: {
    dashboard: 'ড্যাশবোর্ড', mcp: 'MCP', exports: 'এক্সপোর্ট',
    'alert-silences': 'নীরবকরণ', monitors: 'ক্রন মনিটর',
  },
}

export default messages
