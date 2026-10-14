# Supermetry for iOS / Apple platforms

Swift Package for sending events to [Supermetry](../../README.md) (self-hosted web analytics)
from iOS 14+, macOS 12+, tvOS 14+ and watchOS 7+ apps. No dependencies; uses `URLSession`.

- Screen views (`pageview` events for `appUrl/<screen>`), custom events with props and revenue,
  identify / reset.
- Events are queued in memory and persisted to Application Support, retried with the same
  `id`/`ts` on network errors, 429 and 5xx (exponential backoff), dropped on 400.
- Flushes when the app moves to the background; `optOut()` / `optIn()` and the profile ID are
  persisted in `UserDefaults`.
- Browser-shaped User-Agent, e.g.
  `Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Supermetry-iOS/0.1.0`, so the server
  parses OS/device and does not treat the app as a bot.

License: Apache-2.0.

## Install

Swift Package Manager (Xcode: *File → Add Package Dependencies…*), or in `Package.swift`:

```swift
dependencies: [
    .package(url: "https://github.com/<your-org>/supermetry", from: "0.1.0"),
],
targets: [
    .target(name: "App", dependencies: [.product(name: "Supermetry", package: "supermetry")]),
]
```

(The package lives in `sdk/ios`; point SPM at a repository or local path that has
`Package.swift` at its root, e.g. `.package(path: "../supermetry/sdk/ios")`.)

## Quick start

```swift
import Supermetry

@main
struct MyApp: App {
    init() {
        Supermetry.configure(
            site: "your-public-site-id",
            host: "https://analytics.example.com",
            appUrl: "https://app.example.com" // must be an allowed host of the site
        )
    }

    var body: some Scene {
        WindowGroup {
            ContentView().trackScreen("Home")   // SwiftUI helper
        }
    }
}

let analytics = Supermetry.shared
analytics.screen("Checkout")                               // pageview of https://app.example.com/Checkout
analytics.track("Signup", props: ["plan": "pro", "seats": 3])
analytics.track("Purchase", revenue: SupermetryRevenue(49.90, currency: "USD"))
analytics.identify("user-42", traits: ["plan": "pro"])     // later events carry profile_id
analytics.reset()                                          // forget the profile (also identify(nil))
analytics.optOut()                                         // persisted; optIn() to resume
analytics.flush()                                          // or: await analytics.flush()
```

UIKit: call `Supermetry.shared.screen("Name")` from `viewDidAppear`.

### Options

```swift
var config = SupermetryConfig(site: "…", host: "https://analytics.example.com", appUrl: "https://app.example.com")
config.disabled = isDebugBuild
config.onError = { error in print(error) }   // dropped events / retryable failures
config.maxQueueSize = 500
config.endpoint = "https://analytics.example.com/api/public/v1/events"
Supermetry.configure(config)
```

Other fields: `userAgent`, `screenWidth`, `retryBaseDelay`, `maxRetryDelay`,
`flushOnBackground`, `storageDirectory`, `userDefaults`, `urlSession`.

## Notes

- Custom events are attributed to the last screen URL (or `appUrl` before any screen view).
- Props: at most 30 keys (sorted, like the server), values are strings, numbers or booleans;
  long strings are truncated to 2000 bytes. Traits are only sent with `identify`, and invalid
  traits (key > 64 or value > 256 characters, more than 20) are dropped locally rather than
  letting the server reject the event. Revenue is ignored on screen views.
- Events older than 24 h are discarded from the queue (the server would drop them).
- `screen_width` is `UIScreen.main.bounds.width` in points (CSS pixels), read when the client
  is configured on the main thread.

## Development

```bash
docker run --rm -v "$PWD":/pkg -w /pkg swift:6.0 swift test   # Linux: core + tests
swift test                                                      # macOS (Xcode toolchain)
```

On Linux the UIKit/AppKit/SwiftUI parts are compiled out (`#if canImport(...)`).
