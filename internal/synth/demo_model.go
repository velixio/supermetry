package synth

// DemoHosts are the only hosts emitted by the demo traffic model.
var DemoHosts = []string{"www.demo.example", "docs.demo.example"}

const (
	DemoWindowDays      = 90
	DemoCampaign        = "autumn-launch"
	DemoCampaignDaysAgo = 20
	DemoDipDaysAgo      = 40
)

type demoGeo struct {
	country, language string
	weight            int64
	places            [][2]string
}

type demoDevice struct {
	kind                           int16
	browser, browserVer, os, osVer string
	screenW, weight                int
}

type demoChannel struct {
	referrer, source, medium, campaign, content, term string
	weight                                            int64
}

var demoWeekly = [7]float64{0.82, 1.08, 1.12, 1.10, 1.06, 0.98, 0.84}

var demoHourly = []int64{8, 5, 4, 3, 3, 5, 12, 28, 52, 72, 84, 90, 86, 82, 85, 89, 94, 91, 78, 64, 48, 34, 22, 13}

var demoChannels = []demoChannel{
	{weight: 260},
	{referrer: "https://www.google.com/", weight: 120},
	{referrer: "https://www.bing.com/", weight: 28},
	{referrer: "https://duckduckgo.com/", weight: 26},
	{referrer: "https://search.brave.com/", weight: 18},
	{referrer: "https://search.yahoo.com/", weight: 16},
	{referrer: "https://www.ecosia.org/", weight: 14},
	{referrer: "https://x.com/", weight: 30},
	{referrer: "https://www.linkedin.com/", weight: 38},
	{referrer: "https://www.reddit.com/", weight: 30},
	{referrer: "https://www.facebook.com/", weight: 16},
	{referrer: "https://mastodon.social/", weight: 14},
	{referrer: "https://www.threads.net/", weight: 14},
	{referrer: "https://www.youtube.com/", weight: 18},
	{referrer: "https://news.ycombinator.com/", weight: 30},
	{referrer: "https://www.producthunt.com/", weight: 20},
	{referrer: "https://techcrunch.com/", weight: 14},
	{referrer: "https://dev.to/", weight: 18},
	{referrer: "https://hashnode.com/", weight: 14},
	{referrer: "https://medium.com/", weight: 16},
	{referrer: "https://github.com/", weight: 28},
	{referrer: "https://stackoverflow.com/", weight: 18},
	{referrer: "https://lobste.rs/", weight: 12},
	{referrer: "https://www.indiehackers.com/", weight: 14},
	{referrer: "https://www.quora.com/", weight: 12},
	{referrer: "https://chatgpt.com/", weight: 24},
	{referrer: "https://www.perplexity.ai/", weight: 18},
	{referrer: "https://claude.ai/", weight: 14},
	{referrer: "https://gemini.google.com/", weight: 14},
	{referrer: "https://copilot.microsoft.com/", weight: 12},
	{source: "newsletter", medium: "email", campaign: "product-updates", content: "october-edition", weight: 50},
	{source: "lifecycle", medium: "email", campaign: "onboarding-series", content: "day-three", weight: 24},
	{source: "google", medium: "cpc", campaign: DemoCampaign, content: "hero-a", term: "privacy analytics", weight: 48},
	{source: "linkedin", medium: "paid-social", campaign: "founder-stories", content: "video", term: "saas metrics", weight: 25},
	{source: "partner", medium: "webinar", campaign: "partner-webinar", content: "follow-up", weight: 20},
	{source: "newsletter", medium: "email", campaign: "docs-week", content: "api-guide", weight: 20},
	{source: "google", medium: "cpc", campaign: "benchmark-report", content: "report-cover", term: "analytics benchmark", weight: 20},
	{source: "community", medium: "social", campaign: "community-roundup", content: "customer-story", weight: 18},
}

var demoGeos = []demoGeo{
	{country: "US", language: "en-US", weight: 240, places: [][2]string{{"CA", "San Francisco"}, {"NY", "New York"}, {"TX", "Austin"}, {"WA", "Seattle"}, {"IL", "Chicago"}}},
	{country: "GB", language: "en-GB", weight: 90, places: [][2]string{{"England", "London"}, {"Scotland", "Edinburgh"}, {"England", "Manchester"}}},
	{country: "DE", language: "de-DE", weight: 85, places: [][2]string{{"BE", "Berlin"}, {"BY", "Munich"}, {"HE", "Frankfurt"}}},
	{country: "IN", language: "en-IN", weight: 125, places: [][2]string{{"KA", "Bengaluru"}, {"MH", "Mumbai"}, {"DL", "Delhi"}, {"TN", "Chennai"}, {"TS", "Hyderabad"}}},
	{country: "FR", language: "fr-FR", weight: 65, places: [][2]string{{"IDF", "Paris"}, {"ARA", "Lyon"}, {"PAC", "Marseille"}}},
	{country: "CA", language: "en-CA", weight: 60, places: [][2]string{{"ON", "Toronto"}, {"BC", "Vancouver"}, {"QC", "Montreal"}}},
	{country: "BR", language: "pt-BR", weight: 55, places: [][2]string{{"SP", "Sao Paulo"}, {"RJ", "Rio de Janeiro"}, {"MG", "Belo Horizonte"}}},
	{country: "NL", language: "nl-NL", weight: 45, places: [][2]string{{"NH", "Amsterdam"}, {"ZH", "Rotterdam"}, {"UT", "Utrecht"}}},
	{country: "AU", language: "en-AU", weight: 45, places: [][2]string{{"NSW", "Sydney"}, {"VIC", "Melbourne"}, {"QLD", "Brisbane"}}},
	{country: "JP", language: "ja-JP", weight: 40, places: [][2]string{{"13", "Tokyo"}, {"27", "Osaka"}, {"23", "Nagoya"}}},
	{country: "SG", language: "en-SG", weight: 24, places: [][2]string{{"Central", "Singapore"}, {"West", "Jurong East"}}},
	{country: "NP", language: "ne-NP", weight: 18, places: [][2]string{{"Bagmati", "Kathmandu"}, {"Gandaki", "Pokhara"}}},
	{country: "ES", language: "es-ES", weight: 36, places: [][2]string{{"MD", "Madrid"}, {"CT", "Barcelona"}, {"VC", "Valencia"}}},
	{country: "IT", language: "it-IT", weight: 34, places: [][2]string{{"Lazio", "Rome"}, {"Lombardy", "Milan"}, {"Piedmont", "Turin"}}},
	{country: "SE", language: "sv-SE", weight: 28, places: [][2]string{{"AB", "Stockholm"}, {"O", "Gothenburg"}, {"M", "Malmo"}}},
	{country: "NO", language: "nb-NO", weight: 22, places: [][2]string{{"Oslo", "Oslo"}, {"Vestland", "Bergen"}, {"Trondelag", "Trondheim"}}},
	{country: "DK", language: "da-DK", weight: 22, places: [][2]string{{"Hovedstaden", "Copenhagen"}, {"Midtjylland", "Aarhus"}, {"Syddanmark", "Odense"}}},
	{country: "FI", language: "fi-FI", weight: 20, places: [][2]string{{"Uusimaa", "Helsinki"}, {"Pirkanmaa", "Tampere"}, {"Southwest", "Turku"}}},
	{country: "PL", language: "pl-PL", weight: 30, places: [][2]string{{"Mazovia", "Warsaw"}, {"Lesser Poland", "Krakow"}, {"Lower Silesia", "Wroclaw"}}},
	{country: "CZ", language: "cs-CZ", weight: 20, places: [][2]string{{"Prague", "Prague"}, {"South Moravia", "Brno"}, {"Moravia-Silesia", "Ostrava"}}},
	{country: "AT", language: "de-AT", weight: 20, places: [][2]string{{"Vienna", "Vienna"}, {"Styria", "Graz"}, {"Salzburg", "Salzburg"}}},
	{country: "CH", language: "de-CH", weight: 24, places: [][2]string{{"Zurich", "Zurich"}, {"Geneva", "Geneva"}, {"Basel-Stadt", "Basel"}}},
	{country: "IE", language: "en-IE", weight: 25, places: [][2]string{{"Leinster", "Dublin"}, {"Munster", "Cork"}, {"Connacht", "Galway"}}},
	{country: "PT", language: "pt-PT", weight: 22, places: [][2]string{{"Lisbon", "Lisbon"}, {"Porto", "Porto"}, {"Braga", "Braga"}}},
	{country: "MX", language: "es-MX", weight: 32, places: [][2]string{{"CDMX", "Mexico City"}, {"Jalisco", "Guadalajara"}, {"Nuevo Leon", "Monterrey"}}},
	{country: "AR", language: "es-AR", weight: 22, places: [][2]string{{"Buenos Aires", "Buenos Aires"}, {"Cordoba", "Cordoba"}, {"Santa Fe", "Rosario"}}},
	{country: "CL", language: "es-CL", weight: 18, places: [][2]string{{"Santiago", "Santiago"}, {"Valparaiso", "Valparaiso"}, {"Biobio", "Concepcion"}}},
	{country: "CO", language: "es-CO", weight: 20, places: [][2]string{{"Bogota", "Bogota"}, {"Antioquia", "Medellin"}, {"Valle", "Cali"}}},
	{country: "ZA", language: "en-ZA", weight: 24, places: [][2]string{{"Gauteng", "Johannesburg"}, {"Western Cape", "Cape Town"}, {"KwaZulu-Natal", "Durban"}}},
	{country: "NG", language: "en-NG", weight: 20, places: [][2]string{{"Lagos", "Lagos"}, {"FCT", "Abuja"}, {"Rivers", "Port Harcourt"}}},
	{country: "KE", language: "en-KE", weight: 18, places: [][2]string{{"Nairobi", "Nairobi"}, {"Mombasa", "Mombasa"}, {"Nakuru", "Nakuru"}}},
	{country: "AE", language: "ar-AE", weight: 24, places: [][2]string{{"Dubai", "Dubai"}, {"Abu Dhabi", "Abu Dhabi"}, {"Sharjah", "Sharjah"}}},
	{country: "IL", language: "he-IL", weight: 18, places: [][2]string{{"Tel Aviv", "Tel Aviv"}, {"Jerusalem", "Jerusalem"}, {"Haifa", "Haifa"}}},
	{country: "TR", language: "tr-TR", weight: 24, places: [][2]string{{"Istanbul", "Istanbul"}, {"Ankara", "Ankara"}, {"Izmir", "Izmir"}}},
	{country: "KR", language: "ko-KR", weight: 28, places: [][2]string{{"Seoul", "Seoul"}, {"Busan", "Busan"}, {"Incheon", "Incheon"}}},
	{country: "ID", language: "id-ID", weight: 28, places: [][2]string{{"Jakarta", "Jakarta"}, {"West Java", "Bandung"}, {"East Java", "Surabaya"}}},
	{country: "MY", language: "ms-MY", weight: 22, places: [][2]string{{"Kuala Lumpur", "Kuala Lumpur"}, {"Penang", "George Town"}, {"Johor", "Johor Bahru"}}},
	{country: "TH", language: "th-TH", weight: 20, places: [][2]string{{"Bangkok", "Bangkok"}, {"Chiang Mai", "Chiang Mai"}, {"Phuket", "Phuket"}}},
	{country: "VN", language: "vi-VN", weight: 22, places: [][2]string{{"Hanoi", "Hanoi"}, {"HCMC", "Ho Chi Minh City"}, {"Da Nang", "Da Nang"}}},
	{country: "NZ", language: "en-NZ", weight: 18, places: [][2]string{{"Auckland", "Auckland"}, {"Wellington", "Wellington"}, {"Canterbury", "Christchurch"}}},
	{country: "PH", language: "en-PH", weight: 20, places: [][2]string{{"Metro Manila", "Manila"}, {"Central Visayas", "Cebu City"}, {"Davao", "Davao City"}}},
}

var demoDevices = []demoDevice{
	{kind: 0, browser: "Chrome", browserVer: "131", os: "Windows", osVer: "11", screenW: 1920, weight: 180},
	{kind: 0, browser: "Edge", browserVer: "131", os: "Windows", osVer: "11", screenW: 1536, weight: 70},
	{kind: 0, browser: "Safari", browserVer: "18", os: "macOS", osVer: "15", screenW: 1512, weight: 135},
	{kind: 0, browser: "Firefox", browserVer: "132", os: "Linux", osVer: "6", screenW: 1440, weight: 55},
	{kind: 0, browser: "Opera", browserVer: "116", os: "Windows", osVer: "11", screenW: 1366, weight: 24},
	{kind: 0, browser: "Brave", browserVer: "131", os: "macOS", osVer: "14", screenW: 1728, weight: 25},
	{kind: 0, browser: "Vivaldi", browserVer: "7", os: "Linux", osVer: "6", screenW: 1920, weight: 18},
	{kind: 0, browser: "Chrome", browserVer: "130", os: "ChromeOS", osVer: "126", screenW: 1366, weight: 28},
	{kind: 1, browser: "Chrome", browserVer: "131", os: "Android", osVer: "15", screenW: 412, weight: 180},
	{kind: 1, browser: "Samsung Internet", browserVer: "27", os: "Android", osVer: "14", screenW: 360, weight: 55},
	{kind: 1, browser: "Firefox", browserVer: "132", os: "Android", osVer: "14", screenW: 393, weight: 30},
	{kind: 1, browser: "Safari", browserVer: "18", os: "iOS", osVer: "18", screenW: 390, weight: 160},
	{kind: 1, browser: "Edge", browserVer: "131", os: "Android", osVer: "15", screenW: 412, weight: 20},
	{kind: 2, browser: "Safari", browserVer: "18", os: "iPadOS", osVer: "18", screenW: 820, weight: 35},
	{kind: 2, browser: "Chrome", browserVer: "131", os: "Android", osVer: "14", screenW: 1280, weight: 25},
}

var demoBlogPages = []string{
	"/blog/privacy-first-analytics",
	"/blog/measure-conversions",
	"/blog/building-faster-sites",
	"/blog/product-analytics-guide",
	"/blog/understanding-web-vitals",
	"/blog/analytics-without-cookies",
	"/blog/session-replay-guide",
	"/blog/saas-retention-metrics",
	"/blog/heatmap-best-practices",
	"/blog/marketing-attribution",
	"/blog/privacy-regulations",
	"/blog/product-led-growth",
	"/blog/reduce-bounce-rate",
	"/blog/measure-user-journeys",
	"/blog/launch-checklist",
}

var demoFeaturePages = []string{"/features/analytics", "/features/replay", "/features/funnels", "/features/heatmaps", "/features/profiles", "/features/realtime"}

var demoDocPages = []string{
	"/docs/getting-started", "/docs/api", "/docs/tracker", "/docs/go-sdk", "/docs/javascript-sdk", "/docs/custom-events",
	"/docs/revenue", "/docs/session-replay", "/docs/heatmaps", "/docs/privacy", "/docs/self-hosting", "/docs/integrations",
}

var demoPlans = []string{"starter", "growth", "business"}

var demoCompanySizes = []string{"1-10", "11-50", "51-200", "201-1000"}

var demoSignupMethods = []string{"email", "google", "github"}

const (
	demoSignupEvent = iota
	demoPurchaseEvent
	demoOutboundEvent
	demoDownloadEvent
	demoNotFoundEvent
	demoFormEvent
	demoVitalsEvent
)

var demoEvents = []string{
	"Signup",
	"Purchase",
	"Outbound Link: Click",
	"File Download",
	"404",
	"Form: Submit",
	"Web Vitals",
}
