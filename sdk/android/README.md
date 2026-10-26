# Supermetry for Android

Kotlin library `com.supermetry:supermetry-android:0.1.0` for sending events to
[Supermetry](../../README.md) (self-hosted web analytics) from Android apps. minSdk 21, no
third-party dependencies (`HttpURLConnection`, a single background thread, no coroutines).

- Screen views (`pageview` events for `appUrl/<screen>`), custom events with props and revenue,
  identify / reset.
- Events are persisted to a small file queue (`noBackupFilesDir/supermetry/`) and retried with
  the same `id`/`ts` on network errors, 408, 429 and 5xx (exponential backoff, honours
  `Retry-After`); other 4xx are dropped.
- Flushes when the app goes to the background (last activity stopped). `optOut()` / `optIn()`
  and the profile ID are persisted in SharedPreferences.
- Browser-shaped User-Agent, e.g. `Mozilla/5.0 (Linux; Android 14; Pixel 8) Supermetry-Android/0.1.0`
  (`… Tablet` on tablets), so the server parses OS/device and does not treat the app as a bot
  (an `okhttp` UA would be dropped). `screen_width` is the display width in dp.

License: Apache-2.0.

## Install

```kotlin
// settings.gradle.kts: mavenCentral() / mavenLocal() / your repository
dependencies {
    implementation("com.supermetry:supermetry-android:0.1.0")
}
```

Until it is published, build it from this directory and depend on it locally:

```bash
./gradlew :supermetry-android:publishToMavenLocal   # needs ANDROID_HOME or local.properties sdk.dir
```

The library declares the `INTERNET` permission.

## Quick start

```kotlin
class App : Application() {
    override fun onCreate() {
        super.onCreate()
        Supermetry.init(
            this,
            SupermetryConfig(
                site = "your-public-site-id",
                host = "https://analytics.supermetry.com",
                appUrl = "https://app.supermetry.com", // must be an allowed host of the site
            ),
        )
    }
}

Supermetry.screen("Checkout")                                    // pageview of https://app.supermetry.com/Checkout
Supermetry.track("Signup", mapOf("plan" to "pro", "seats" to 3))
Supermetry.track("Purchase", revenue = Revenue(49.90, "USD"))
Supermetry.identify("user-42", mapOf("plan" to "pro"))          // later events carry profile_id
Supermetry.reset()                                               // or identify(null)
Supermetry.optOut()                                              // persisted; optIn() to resume
Supermetry.flush()
```

Java: `Supermetry.track("Signup")`, `Supermetry.screen("Home")`, … (all `@JvmStatic`).

Call `screen()` from `Activity.onResume` / a fragment's `onResume`, or in Compose:

```kotlin
@Composable
fun TrackScreen(name: String) {
    LaunchedEffect(name) { Supermetry.screen(name) }
}
```

(not shipped, to keep the library free of Compose dependencies).

### Options

`SupermetryConfig(site, host, appUrl, endpoint = null, disabled = false, userAgent = null,
screenWidth = null, maxQueueSize = 500, retryBaseDelayMs = 2000, maxRetryDelayMs = 300000,
flushOnBackground = true, connectTimeoutMs = 10000, readTimeoutMs = 10000, onError = null)`.

`onError` receives `SupermetryError.InvalidEvent`, `Rejected(status)`, `Retrying(status, cause)`
or `InvalidConfiguration` on the worker thread. Tracking never throws.

## Notes

- Custom events are attributed to the last screen URL (or `appUrl` before any screen view).
- Props: at most 30 keys (sorted, like the server), String / Number / Boolean values; long
  strings are truncated to 2000 bytes; other value types are dropped. Traits are only sent with
  `identify`; invalid traits (key > 64 or value > 256 characters, more than 20) are dropped
  locally rather than letting the server reject the event. Revenue is ignored on screen views.
- Events older than 24 h are discarded from the queue (the server would drop them).

## Project layout

- `supermetry-core/`: platform-independent Kotlin/JVM (`SupermetryClient`, validation, JSON,
  file queue, `HttpURLConnection` transport) with JUnit tests against a local HTTP server.
- `supermetry-android/`: the published Android library. It compiles the core sources into the
  same AAR and adds `Supermetry` (facade), SharedPreferences storage, lifecycle flushing,
  User-Agent and screen width.

```bash
gradle :supermetry-core:test                                  # plain JVM, no Android SDK
ANDROID_HOME=… gradle :supermetry-android:assembleRelease :supermetry-android:lintRelease
```

The Android module is only included when `ANDROID_HOME`, `ANDROID_SDK_ROOT` or
`local.properties` `sdk.dir` is set.
