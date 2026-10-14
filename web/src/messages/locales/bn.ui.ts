import type { MessageTree } from '@/lib/i18n/catalogue'

// BN translation of messages/ui.ts. Machine-assisted first translation (reviewed: false in the manifest).
// Keys, placeholders and function arguments must match English exactly; the catalogue test enforces it.
const messages: MessageTree = {
  breakdown: {
    pages: 'পেজ', sources: 'উৎস', location: 'অবস্থান', environment: 'পরিবেশ', goalsEvents: 'লক্ষ্য ও ইভেন্ট',
    verifiedPages: 'যাচাইকৃত পেজ', channels: 'চ্যানেল', verified: 'যাচাইকৃত',
    tabs: {
      top: 'শীর্ষ', entry: 'প্রবেশ', exit: 'প্রস্থান', source: 'উৎস', channel: 'চ্যানেল', utmSource: 'UTM উৎস',
      medium: 'মাধ্যম', campaign: 'প্রচারণা', countries: 'দেশ', regions: 'অঞ্চল', cities: 'শহর',
      language: 'ভাষা', hostname: 'হোস্টনেম', browser: 'ব্রাউজার', browserVersion: 'ব্রাউজার সংস্করণ', os: 'OS',
      osVersion: 'OS সংস্করণ', device: 'ডিভাইস', screenSize: 'স্ক্রিনের আকার', goals: 'লক্ষ্য', events: 'ইভেন্ট',
    },
    verifiedNone: 'যাচাইকৃত পেজের অ্যানালিটিক্সে ফিল্টার রাখা যায় না। এই আংশিক ডেটাসেট দেখতে সক্রিয় ফিল্টার সরান।',
    anonymousGoals: 'বেনামী মোডে লক্ষ্য ও ইভেন্টের বিভাজন পাওয়া যায় না, কারণ তাদের লেবেলে শনাক্তকারী ডেটা থাকতে পারে।',
    partialCoverage: 'আংশিক কভারেজ: বর্তমান পাথ নীতির অধীনে শুধু যাচাইকৃত পেজের ডেটা। পুরোনো অযাচাইকৃত ডেটা ও আমদানি বাদ দেওয়া হয়েছে। পেজ ফিল্টার ও বিস্তারিত অনুসন্ধান পাওয়া যায় না।',
    canonicalOnly: 'শুধু নির্ধারিত বিভাগ পাওয়া যায়। বেনামী মোডে মুক্ত-পাঠ লেবেল, সংস্করণ ও অনুসন্ধান সীমিত।',
    unknown: 'অজানা', mobile: 'মোবাইল (<768px)', tablet: 'ট্যাবলেট (768–1023px)', laptop: 'ল্যাপটপ (1024–1439px)',
    desktop: 'ডেস্কটপ (1440–1919px)', large: 'বড় (≥1920px)',
    breakdownOf: (title: string) => `${title}-এর বিভাজন`,
    loading: (title: string) => `${title} লোড হচ্ছে`,
    filterBy: (dimension: string, label: string) => `${dimension} ${label} দিয়ে ফিল্টার করুন`,
    unverifiedCoverage: 'যাচাইকৃত পেজের কভারেজ নিশ্চিত করা যায়নি। এই বিভাজন দেখার আগে আবার লোড করুন।',
    noVerifiedData: 'এই সময়সীমায় কোনো যাচাইকৃত পেজের ডেটা নেই। পুরোনো অযাচাইকৃত রেকর্ড ও আমদানি বাদ দেওয়া হয়েছে।',
  },
  menus: {
    settings: 'সেটিংস', settingsMenu: 'সেটিংস মেনু', organizationSettings: 'প্রতিষ্ঠানের সেটিংস', siteSettings: 'সাইটের সেটিংস',
    account: 'অ্যাকাউন্ট', accountMenu: 'অ্যাকাউন্ট মেনু', yourAccount: 'আপনার অ্যাকাউন্ট', accountSettings: 'অ্যাকাউন্ট সেটিংস', logOut: 'লগ আউট',
  },
  emptyState: { setUpTracking: 'ট্র্যাকিং সেট আপ করুন' },
  pager: {
    label: 'তালিকার পৃষ্ঠা বিভাজন', noResults: 'কোনো ফলাফল নেই',
    showing: (first: string, last: string) => `${first}–${last} দেখানো হচ্ছে`,
    ofTotal: (total: string) => ` / মোট ${total}`, page: (page: string) => ` · পৃষ্ঠা ${page}`, ofPages: (pages: string) => ` / ${pages}`,
    first: 'প্রথম', previous: 'আগের', next: 'পরের',
  },
  segments: {
    nameLength: 'নাম 1 থেকে 80 অক্ষরের মধ্যে হতে হবে।', saveFailed: 'সেগমেন্টটি সংরক্ষণ করা যায়নি।', saved: 'সংরক্ষিত সেগমেন্ট',
    loading: 'লোড হচ্ছে…', none: 'কোনো সংরক্ষিত সেগমেন্ট নেই', rename: 'সেগমেন্টের নাম পরিবর্তন করুন', save: 'সেগমেন্ট সংরক্ষণ করুন',
    renameNote: 'এই সংরক্ষিত সেগমেন্টের জন্য নতুন নাম বেছে নিন।', saveNote: 'এই সাইটের বর্তমান ড্যাশবোর্ড ফিল্টার সংরক্ষণ করুন।',
    name: 'সেগমেন্টের নাম', cancel: 'বাতিল করুন', saving: 'সংরক্ষণ করা হচ্ছে…', deleteTitle: 'সেগমেন্ট মুছবেন?',
    deleteBody: (name: string) => `“${name}” মুছবেন? এটি ফিরিয়ে আনা যাবে না।`, deleting: 'মোছা হচ্ছে…', delete: 'সেগমেন্ট মুছুন',
  },
  sidebar: { demo: 'ডেমো', chooseOrganization: 'প্রতিষ্ঠান বেছে নিন' },
  appShell: { loadingLive: 'সরাসরি দর্শনার্থী লোড হচ্ছে' },
  crumbs: {
    label: 'ব্রেডক্রাম্ব', detail: 'বিস্তারিত', account: 'অ্যাকাউন্ট', admin: 'অ্যাডমিন',
    segments: {
      sites: 'সাইট', health: 'স্বাস্থ্য', settings: 'সেটিংস', dashboard: 'ড্যাশবোর্ড', events: 'ইভেন্ট', sessions: 'সেশন',
      realtime: 'রিয়েলটাইম', replays: 'রিপ্লে', replay: 'রিপ্লে', journeys: 'যাত্রাপথ', retention: 'ধরে রাখা', profiles: 'প্রোফাইল',
      cohorts: 'কোহর্ট', heatmaps: 'হিটম্যাপ', vitals: 'Web Vitals', funnels: 'ফানেল', goals: 'লক্ষ্য', revenue: 'আয়', links: 'লিংক',
      pixel: 'পিক্সেল', general: 'সাধারণ', alerts: 'সতর্কতা', installation: 'ইনস্টলেশন', integrations: 'ইন্টিগ্রেশন',
      imports: 'আমদানি', reports: 'রিপোর্ট', shares: 'শেয়ার', tracking: 'ট্র্যাকিং', members: 'সদস্য', invites: 'আমন্ত্রণ',
      audit: 'অডিট লগ', api: 'API', 'api-keys': 'API কী', notifications: 'বিজ্ঞপ্তি', 'notification-history': 'বিজ্ঞপ্তির ইতিহাস',
      'replay-storage': 'রিপ্লে স্টোরেজ', new: 'নতুন', profile: 'প্রোফাইল', security: 'নিরাপত্তা', organizations: 'প্রতিষ্ঠান', users: 'ব্যবহারকারী', update: 'সিস্টেম আপডেট',
    },
  },
  inspector: {
    inspectValue: (label: string) => `${label} পরিদর্শন করুন`, close: 'পরিদর্শক বন্ধ করুন', resize: 'পরিদর্শকের আকার বদলান',
    loading: 'পরিদর্শক লোড হচ্ছে', metrics: 'এই মানের মেট্রিক', topBy: (dimension: string) => `${dimension}: শীর্ষ 5`, addFilter: 'ফিল্টার হিসেবে যোগ করুন',
    filterApplied: 'এই মানটি ইতিমধ্যে একটি ফিল্টার।', tooManyFilters: 'এই মান পরিদর্শন করতে একটি ফিল্টার সরান: কোয়েরিতে সর্বোচ্চ 10টি ফিল্টার রাখা যায়।',
    outOfRange: 'সীমার বাইরে', outOfRangeDetail: 'বর্তমান তারিখসীমা ও ফিল্টার এই মানটি বাদ দিচ্ছে। সেগুলো পরিবর্তন করুন বা পরিদর্শক বন্ধ করুন।',
    cellHint: 'পরিদর্শনের জন্য একটি সেল বেছে নিন।', trafficScope: 'নির্বাচিত সময়সীমায় সাইটের সময় অঞ্চলের সব মিলে যাওয়া বার ও ঘণ্টাজুড়ে।',
    dimensions: {
      page: 'পেজ', entry: 'প্রবেশ পেজ', exit: 'প্রস্থান পেজ', source: 'উৎস', channel: 'চ্যানেল', utm_source: 'UTM উৎস',
      utm_medium: 'UTM মাধ্যম', utm_campaign: 'UTM প্রচারণা', country: 'দেশ', region: 'অঞ্চল', city: 'শহর', language: 'ভাষা',
      hostname: 'হোস্টনেম', browser: 'ব্রাউজার', browser_version: 'ব্রাউজার সংস্করণ', os: 'OS', os_version: 'OS সংস্করণ',
      device: 'ডিভাইস', screen_size: 'স্ক্রিনের আকার', goal: 'লক্ষ্য', event: 'ইভেন্ট', weekday_hour: 'সাপ্তাহিক ছন্দ',
    },
  },
  language: { menu: 'ভাষা', saveFailed: 'ভাষাটি সংরক্ষণ করা যায়নি। আবার চেষ্টা করুন।' },
  palette: {
    open: 'কমান্ড প্যালেট খুলুন', loadFailed: 'কমান্ড প্যালেট লোড করা যায়নি। সংযোগ পরীক্ষা করে আবার চেষ্টা করুন।', keyHint: 'Ctrl K', keyHintApple: '⌘K',
    title: 'কমান্ড প্যালেট', description: 'কোনো পেজে যান, সাইট বা প্রতিষ্ঠান বদলান, অথবা কোনো কাজ চালান।', inputLabel: 'কমান্ড খুঁজুন',
    placeholder: 'পেজ, সাইট বা কাজ লিখুন…', empty: 'মিলে যাওয়া কোনো কমান্ড নেই',
    groups: { pages: 'পেজ', sites: 'সাইট', organizations: 'প্রতিষ্ঠান', actions: 'কাজ' },
  },
}

export default messages
