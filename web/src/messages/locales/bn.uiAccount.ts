import type { MessageTree } from '@/lib/i18n/catalogue'

// BN translation of messages/uiAccount.ts. Machine-assisted first translation (reviewed: false in the manifest).
// Keys, placeholders and function arguments must match English exactly; the catalogue test enforces it.
const messages: MessageTree = {
  profile: {
    title: 'প্রোফাইল', intro: 'আপনার পরিচয় ও অ্যাকাউন্টের প্রমাণপত্র পরিচালনা করুন।', displayName: 'প্রদর্শিত নাম',
    displayNameNote: 'সব প্রতিষ্ঠানে দেখানো হবে।', saveName: 'নাম সংরক্ষণ করুন', landingTitle: 'ল্যান্ডিং পেজ',
    landingNote: 'সাইন ইন করার পর Supermetry কোথায় খুলবে তা বেছে নিন। সরাসরি সাইন-ইন লিংক তার নির্ধারিত পেজই খুলবে।',
    afterSignIn: 'সাইন ইন করার পর', sitesOverview: 'সাইটের সারসংক্ষেপ', lastSite: 'সর্বশেষ দেখা সাইট',
    landingHint: 'কোনো পছন্দ সংরক্ষিত না থাকলে একটি সাইট সরাসরি খোলে, আর একাধিক সাইট থাকলে সারসংক্ষেপ খোলে।',
    emailTitle: 'ইমেইল ঠিকানা',
    emailNote: (email: string) => `নতুন ঠিকানায় আমরা এক ঘণ্টা মেয়াদের নিশ্চিতকরণ লিংক পাঠাব। আপনার বর্তমান ঠিকানা ${email}।`,
    newEmail: 'নতুন ইমেইল', emailPassword: 'ইমেইল পরিবর্তনের পাসওয়ার্ড', emailSent: 'নিশ্চিতকরণ লিংকের জন্য নতুন ঠিকানা দেখুন।',
    sendConfirmation: 'নিশ্চিতকরণ পাঠান', passwordTitle: 'পাসওয়ার্ড', passwordNote: 'এটি পরিবর্তন করলে আপনার অন্য সেশনগুলো থেকে সাইন আউট হবে।',
    currentPassword: 'বর্তমান পাসওয়ার্ড', newPassword: 'নতুন পাসওয়ার্ড', changePassword: 'পাসওয়ার্ড পরিবর্তন করুন',
    deleteTitle: 'অ্যাকাউন্ট মুছুন', deleteNote: 'আপনার পরিচয় বেনামী করা হবে এবং সদস্যপদ ও সেশন সরানো হবে। যেসব প্রতিষ্ঠানের একমাত্র মালিক আপনি, সেগুলোর মালিকানা হস্তান্তর বা সেগুলো মুছে ফেলতে হবে।',
    deletePassword: 'অ্যাকাউন্ট মোছার পাসওয়ার্ড', authCode: 'প্রমাণীকরণ কোড', deleteConfirmTitle: 'আপনার অ্যাকাউন্ট মুছবেন?',
    deleteConfirmBody: 'এটি ফিরিয়ে আনা যাবে না। আপনার অ্যাকাউন্টের পরিচয় বেনামী করা হবে।', cancel: 'বাতিল করুন', deleteForever: 'অ্যাকাউন্ট স্থায়ীভাবে মুছুন',
  },
  sessions: {
    unknown: 'অজানা', title: 'সক্রিয় সেশন', intro: 'আপনার অ্যাকাউন্টে সাইন ইন করা ডিভাইসগুলো দেখুন এবং অপরিচিত ডিভাইসের অনুমতি বাতিল করুন।',
    sectionLabel: 'অ্যাকাউন্ট সেশন', note: 'সাইন ইন করার সময় IP ঠিকানা, আনুমানিক দেশ ও ব্রাউজার রেকর্ড করা হয়। সময় আপনার স্থানীয় সময় অঞ্চল ব্যবহার করে। সর্বশেষ সক্রিয় সময় প্রমাণীকৃত কার্যকলাপ বোঝায় এবং সর্বোচ্চ মিনিটে একবার হালনাগাদ হয়; পরিচিত পটভূমির পোলিং এটি হালনাগাদ করে না।',
    signOutAll: 'অন্য সব ডিভাইস থেকে সাইন আউট করুন', signOutAllTitle: 'অন্য সব ডিভাইস থেকে সাইন আউট করবেন?',
    signOutBody: (count: number) => `এটি আপনার অ্যাকাউন্টের অন্য ${count}টি ${count === 1 ? 'লগইন সেশন' : 'লগইন সেশন'} থেকে সাইন আউট করবে। এই সেশন সাইন ইন অবস্থায় থাকবে।`,
    signOutFailed: 'অন্য সেশনগুলো থেকে সাইন আউট করা যায়নি। আবার চেষ্টা করুন।', cancel: 'বাতিল করুন', signingOut: 'সাইন আউট করা হচ্ছে…',
    signOutOthers: 'অন্য সেশন থেকে সাইন আউট করুন', loading: 'সেশন লোড হচ্ছে…', loadFailed: 'আপনার সেশনগুলো লোড করা যায়নি।', tryAgain: 'আবার চেষ্টা করুন',
    revokeFailed: 'এই সেশনটির অনুমতি বাতিল করা যায়নি। আবার চেষ্টা করুন।', none: 'কোনো সক্রিয় সেশন নেই।', listLabel: 'সক্রিয় অ্যাকাউন্ট সেশন',
    unknownBrowser: 'অজানা ব্রাউজার', current: 'বর্তমান সেশন', revokeCurrent: 'বর্তমান সেশনের অনুমতি বাতিল করুন',
    revokeAria: (browser: string, ip: string) => `${browser} সেশনের অনুমতি বাতিল করুন${ip ? ` (${ip})` : ''}`,
    revoking: 'অনুমতি বাতিল করা হচ্ছে…', revoke: 'অনুমতি বাতিল করুন', ip: 'সাইন-ইনের সময় IP ঠিকানা', country: 'আনুমানিক দেশ',
    created: 'তৈরির সময়', lastActive: 'সর্বশেষ সক্রিয়',
  },
}

export default messages
