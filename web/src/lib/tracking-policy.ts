import type { TrackingPolicy } from '@/features/sites/types'

type PolicySite = { tracking_policy?: TrackingPolicy }

// UI capabilities reflect the server ceiling, not the project's profile opt-in.
// Missing or malformed bootstrap policy never grants access to historical data.
export function trackingPolicyKnown(site: PolicySite | undefined): boolean {
  return site?.tracking_policy?.instance_mode === 'identified' || site?.tracking_policy?.instance_mode === 'anonymous'
}
export function isAnonymousTracking(site: PolicySite | undefined): boolean {
  return site?.tracking_policy?.instance_mode !== 'identified'
}

export function isAnonymousDimensionAllowed(dimension: string): boolean {
  return ['time', 'hour', 'country', 'device', 'browser', 'os', 'channel'].includes(dimension)
}

// Deliberately finite labels, matching internal/privacy/dimensions.go. Unknown
// labels remain visible as aggregate buckets but cannot become probing filters.
const labels: Record<string, ReadonlySet<string>> = {
  country: new Set('AC AD AE AF AG AI AL AM AN AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BU BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CP CQ CR CS CT CU CV CW CX CY CZ DD DE DG DJ DK DM DO DY DZ EA EC EE EG EH ER ES ET EZ FI FJ FK FM FO FQ FR FX GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU HV IC ID IE IL IM IN IO IQ IR IS IT JE JM JO JP JT KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MI MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NH NI NL NO NP NQ NR NT NU NZ OM PA PC PE PF PG PH PK PL PM PN PR PS PT PU PW PY PZ QA RE RH RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SU SV SX SY SZ TA TC TD TF TG TH TJ TK TL TM TN TO TP TR TT TV TW TZ UA UG UK UM UN US UY UZ VA VC VD VE VG VI VN VU WF WK WS XK YD YE YT YU ZA ZM ZR ZW'.split(' ')),
  device: new Set(['desktop', 'mobile', 'tablet']),
  browser: new Set(['Chrome', 'Chromium', 'Firefox', 'Safari', 'Edge', 'Opera', 'Opera Mini', 'Opera Touch', 'NetFront', 'Mozilla', 'Internet Explorer', 'Samsung Internet', 'Samsung Browser', 'Android Browser', 'UC Browser', 'Yandex Browser', 'Vivaldi', 'Brave', 'Headless Chrome', 'Facebook', 'Instagram', 'Facebook App', 'Instagram App', 'TikTok App']),
  os: new Set(['Windows', 'Windows Phone', 'macOS', 'Mac OS X', 'iOS', 'Android', 'Linux', 'ChromeOS', 'Chrome OS', 'Ubuntu', 'FreeBSD', 'OpenBSD', 'BlackBerry', 'Harmony', 'Other']),
  channel: new Set(['Direct', 'Organic Search', 'Paid Search', 'Organic Social', 'Paid Social', 'Email', 'Referral', 'AI Assistants', 'Other']),
}

export function isAnonymousFilterAllowed(filter: { dimension: string; operator: string; values: string[] }): boolean {
  return (filter.operator === 'is' || filter.operator === 'is_not') &&
    filter.values.length > 0 && filter.values.every((value) => labels[filter.dimension]?.has(value) === true)
}
