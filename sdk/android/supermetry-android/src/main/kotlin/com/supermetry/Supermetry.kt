package com.supermetry

import android.app.Activity
import android.app.Application
import android.content.Context
import android.content.SharedPreferences
import android.content.res.Configuration
import android.os.Build
import android.os.Bundle
import java.io.File

/**
 * Supermetry for Android.
 *
 * ```kotlin
 * class App : Application() {
 *     override fun onCreate() {
 *         super.onCreate()
 *         Supermetry.init(this, SupermetryConfig(
 *             site = "your-public-site-id",
 *             host = "https://analytics.supermetry.com",
 *             appUrl = "https://app.supermetry.com",
 *         ))
 *     }
 * }
 *
 * Supermetry.screen("Checkout")
 * Supermetry.track("Purchase", mapOf("plan" to "pro"), Revenue(49.90, "USD"))
 * ```
 *
 * Calls before [init] are ignored. All methods return immediately and never throw.
 */
object Supermetry {
    const val VERSION = SupermetryClient.VERSION

    @Volatile private var instance: SupermetryClient? = null
    private var lifecycle: FlushOnBackground? = null
    private var application: Application? = null

    /** Configure Supermetry. Call once, from `Application.onCreate`. */
    @JvmStatic
    @Synchronized
    fun init(context: Context, config: SupermetryConfig): SupermetryClient {
        val appContext = context.applicationContext ?: context
        instance?.shutdown()
        lifecycle?.let { application?.unregisterActivityLifecycleCallbacks(it) }

        val safeSite = config.site.replace(Regex("[^A-Za-z0-9_-]"), "_")
        val client = SupermetryClient(
            config = config,
            keyValueStore = SharedPreferencesStore(appContext.getSharedPreferences("supermetry", Context.MODE_PRIVATE)),
            queueStore = FileQueueStore(File(appContext.noBackupFilesDir, "supermetry/queue-$safeSite.txt")),
            defaultUserAgent = AndroidDevice.userAgent(appContext),
            defaultScreenWidth = AndroidDevice.screenWidthDp(appContext),
        )
        instance = client

        val app = appContext as? Application
        if (app != null && config.flushOnBackground && client.isEnabled) {
            val callbacks = FlushOnBackground(client)
            app.registerActivityLifecycleCallbacks(callbacks)
            lifecycle = callbacks
            application = app
        } else {
            lifecycle = null
            application = null
        }
        return client
    }

    /** The configured client, or null before [init]. */
    @JvmStatic
    val client: SupermetryClient? get() = instance

    /** Track a custom event. Props: String, Number or Boolean values (max 30). */
    @JvmStatic
    @JvmOverloads
    fun track(name: String, props: Map<String, Any?> = emptyMap(), revenue: Revenue? = null) {
        instance?.track(name, props, revenue)
    }

    /** Track a screen view: a `pageview` of `appUrl + "/" + name`. */
    @JvmStatic
    @JvmOverloads
    fun screen(name: String, props: Map<String, Any?> = emptyMap()) {
        instance?.screen(name, props)
    }

    /** Identify the user (persisted); later events carry `profile_id`. `null` resets. */
    @JvmStatic
    @JvmOverloads
    fun identify(profileId: String?, traits: Map<String, String> = emptyMap()) {
        instance?.identify(profileId, traits)
    }

    /** Forget the identified profile. */
    @JvmStatic
    fun reset() {
        instance?.reset()
    }

    /** Stop tracking and discard queued events (persisted). */
    @JvmStatic
    fun optOut() {
        instance?.optOut()
    }

    /** Resume tracking after [optOut]. */
    @JvmStatic
    fun optIn() {
        instance?.optIn()
    }

    @JvmStatic
    val isOptedOut: Boolean get() = instance?.isOptedOut ?: false

    @JvmStatic
    val profileId: String? get() = instance?.profileId

    /** Send queued events now. [onComplete] runs on a background thread. */
    @JvmStatic
    @JvmOverloads
    fun flush(onComplete: (() -> Unit)? = null) {
        val client = instance
        if (client == null) onComplete?.invoke() else client.flush(onComplete)
    }
}

/** Flushes when the last started activity stops (the app moved to the background). */
internal class FlushOnBackground(private val client: SupermetryClient) : Application.ActivityLifecycleCallbacks {
    private var started = 0

    override fun onActivityStarted(activity: Activity) {
        started++
    }

    override fun onActivityStopped(activity: Activity) {
        started = (started - 1).coerceAtLeast(0)
        if (started == 0 && !activity.isChangingConfigurations) client.flush()
    }

    override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) {}
    override fun onActivityResumed(activity: Activity) {}
    override fun onActivityPaused(activity: Activity) {}
    override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) {}
    override fun onActivityDestroyed(activity: Activity) {}
}

/** [KeyValueStore] on SharedPreferences. */
internal class SharedPreferencesStore(private val prefs: SharedPreferences) : KeyValueStore {
    override fun getString(key: String): String? = prefs.getString(key, null)

    override fun putString(key: String, value: String?) {
        prefs.edit().apply { if (value == null) remove(key) else putString(key, value) }.apply()
    }

    override fun getBoolean(key: String): Boolean = prefs.getBoolean(key, false)

    override fun putBoolean(key: String, value: Boolean) {
        prefs.edit().putBoolean(key, value).apply()
    }
}

/** Browser-shaped User-Agent and screen width, so the server parses OS/device. */
internal object AndroidDevice {
    fun userAgent(context: Context): String {
        val release = Build.VERSION.RELEASE.orEmpty().ifEmpty { "0" }
        val model = Build.MODEL.orEmpty().replace(Regex("[;()\\r\\n]"), " ").trim().ifEmpty { "Android" }
        val tablet = context.resources.configuration.smallestScreenWidthDp >= 600 &&
            context.resources.configuration.smallestScreenWidthDp != Configuration.SMALLEST_SCREEN_WIDTH_DP_UNDEFINED
        val suffix = if (tablet) " Tablet" else ""
        // e.g. "Mozilla/5.0 (Linux; Android 14; Pixel 8) Supermetry-Android/0.1.0"
        return "Mozilla/5.0 (Linux; Android $release; $model) Supermetry-Android/${SupermetryClient.VERSION}$suffix"
    }

    /** Screen width in dp (what a browser reports as `screen.width`). */
    fun screenWidthDp(context: Context): Int? {
        val metrics = context.resources.displayMetrics ?: return null
        if (metrics.density <= 0f || metrics.widthPixels <= 0) return null
        return (metrics.widthPixels / metrics.density).toInt()
    }
}
