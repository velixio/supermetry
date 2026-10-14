package com.supermetry

import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.ThreadFactory
import java.util.concurrent.TimeUnit

/**
 * Platform-independent Supermetry client: builds and validates events, persists them through a
 * [QueueStore], and delivers them one at a time on a single background thread.
 *
 * Network errors, 408, 429 and 5xx keep the event (same `id`/`ts`) and retry with exponential
 * backoff; other 4xx drop it. Every public method returns immediately and never throws.
 *
 * Android apps use the [Supermetry] facade instead of constructing this directly.
 */
class SupermetryClient @JvmOverloads constructor(
    val config: SupermetryConfig,
    private val keyValueStore: KeyValueStore = InMemoryKeyValueStore(),
    private val queueStore: QueueStore = InMemoryQueueStore(),
    private val transport: Transport = HttpUrlConnectionTransport(config.connectTimeoutMs, config.readTimeoutMs),
    /** Default User-Agent when [SupermetryConfig.userAgent] is not set. */
    defaultUserAgent: String = "Mozilla/5.0 (Linux) Supermetry-Android/$VERSION",
    /** Default screen width when [SupermetryConfig.screenWidth] is not set. */
    defaultScreenWidth: Int? = null,
    private val clock: () -> Long = { System.currentTimeMillis() },
) {
    companion object {
        const val VERSION = "0.1.0"
        const val EVENTS_PATH = "/api/public/v1/events"
        internal const val MAX_EVENT_AGE_MS = 24L * 60 * 60 * 1000
    }

    private val endpoint: String? = when {
        !config.endpoint.isNullOrEmpty() -> config.endpoint
        config.host.isNotEmpty() -> config.host.trimEnd('/') + EVENTS_PATH
        else -> null
    }
    private val userAgent: String = config.userAgent ?: defaultUserAgent
    private val screenWidth: Int? = (config.screenWidth ?: defaultScreenWidth)?.takeIf { it in 0..Validation.MAX_SCREEN_WIDTH }
    private val prefix = "supermetry.${config.site}."

    /** True when configured and not disabled. */
    val isEnabled: Boolean

    private val executor: ScheduledExecutorService?

    // State below is only touched on the executor thread.
    private val pending = ArrayList<QueuedEvent>()
    private var retryFuture: ScheduledFuture<*>? = null
    private var currentScreenUrl: String? = null

    @Volatile private var profileIdValue: String? = keyValueStore.getString(prefix + "profileId")
    @Volatile private var optedOutValue: Boolean = keyValueStore.getBoolean(prefix + "optOut")

    init {
        val problem = when {
            config.disabled -> null
            config.site.isEmpty() -> "site is required"
            endpoint == null || !Validation.validUrl(endpoint) -> "host must be an absolute http(s) URL"
            !Validation.validUrl(config.appUrl) -> "appUrl must be an absolute http(s) URL without credentials"
            else -> null
        }
        isEnabled = !config.disabled && problem == null
        if (problem != null) report(SupermetryError.InvalidConfiguration(problem))
        executor = if (isEnabled) {
            Executors.newSingleThreadScheduledExecutor(ThreadFactory { runnable ->
                Thread(runnable, "supermetry").apply { isDaemon = true }
            })
        } else {
            null
        }
        submit {
            pending.addAll(queueStore.load())
            if (optedOutValue) {
                pending.clear()
                queueStore.save(pending)
            }
            drain()
        }
    }

    /** The identified profile ID, persisted across launches. */
    val profileId: String? get() = profileIdValue

    /** Whether tracking is opted out (persisted). */
    val isOptedOut: Boolean get() = optedOutValue

    /** Track a custom event, attributed to the last screen (or `appUrl`). */
    @JvmOverloads
    fun track(name: String, props: Map<String, Any?> = emptyMap(), revenue: Revenue? = null) {
        submit { enqueue(name, currentScreenUrl ?: config.appUrl, props, revenue, null) }
    }

    /** Track a screen view: a `pageview` with `url = appUrl + "/" + name`. */
    @JvmOverloads
    fun screen(name: String, props: Map<String, Any?> = emptyMap()) {
        submit {
            val url = Validation.screenUrl(config.appUrl, name)
            currentScreenUrl = url
            enqueue("pageview", url, props, null, null)
        }
    }

    /**
     * Identify the user; the profile ID is persisted and attached to later events.
     * `identify(null)` is the same as [reset].
     */
    @JvmOverloads
    fun identify(profileId: String?, traits: Map<String, String> = emptyMap()) {
        if (profileId.isNullOrEmpty()) {
            reset()
            return
        }
        if (!isEnabled) return
        if (!Validation.validProfileId(profileId)) {
            report(SupermetryError.InvalidEvent("profile id must be 1-128 characters"))
            return
        }
        submit {
            profileIdValue = profileId
            keyValueStore.putString(prefix + "profileId", profileId)
            enqueue("identify", currentScreenUrl ?: config.appUrl, emptyMap(), null, Validation.cleanTraits(traits))
        }
    }

    /** Forget the identified profile. */
    fun reset() {
        val clear = {
            profileIdValue = null
            keyValueStore.putString(prefix + "profileId", null)
        }
        if (executor == null) clear() else submit(clear)
    }

    /** Stop tracking and discard queued events (persisted). */
    fun optOut() {
        optedOutValue = true
        keyValueStore.putBoolean(prefix + "optOut", true)
        submit {
            pending.clear()
            retryFuture?.cancel(false)
            retryFuture = null
            queueStore.save(pending)
        }
    }

    /** Resume tracking after [optOut]. */
    fun optIn() {
        optedOutValue = false
        keyValueStore.putBoolean(prefix + "optOut", false)
    }

    /**
     * Send queued events now, ignoring retry backoff. [onComplete] runs on the worker thread once
     * the queue is empty or a retryable failure stops delivery.
     */
    @JvmOverloads
    fun flush(onComplete: (() -> Unit)? = null) {
        val executor = executor
        if (executor == null) {
            onComplete?.invoke()
            return
        }
        try {
            executor.execute {
                retryFuture?.cancel(false)
                retryFuture = null
                drain()
                onComplete?.invoke()
            }
        } catch (_: Exception) {
            onComplete?.invoke()
        }
    }

    /** Blocking [flush]; returns false on timeout. Do not call on the main thread. */
    fun flushBlocking(timeoutMs: Long = 10_000): Boolean {
        val latch = CountDownLatch(1)
        flush { latch.countDown() }
        return latch.await(timeoutMs, TimeUnit.MILLISECONDS)
    }

    /** Number of events waiting for delivery. */
    fun queuedCount(): Int {
        val executor = executor ?: return 0
        return try {
            executor.submit<Int> { pending.size }.get(10, TimeUnit.SECONDS)
        } catch (_: Exception) {
            -1
        }
    }

    /** Stop the worker thread. Queued events stay persisted for the next launch. */
    fun shutdown() {
        executor?.shutdown()
    }

    /** Build the JSON body for an event, or null (after reporting) when invalid. */
    internal fun buildBody(
        name: String,
        url: String,
        props: Map<String, Any?>,
        revenue: Revenue?,
        traits: Map<String, String>?,
        id: String = Validation.newEventId(),
        ts: Long = clock(),
    ): String? {
        if (!Validation.validName(name)) {
            report(SupermetryError.InvalidEvent("name must be 1-300 bytes"))
            return null
        }
        if (!Validation.validUrl(url)) {
            report(SupermetryError.InvalidEvent("url must be an absolute http(s) URL of at most 2000 bytes"))
            return null
        }
        val payload = LinkedHashMap<String, Any>()
        payload["v"] = 1
        payload["id"] = id
        payload["ts"] = ts
        payload["site"] = config.site
        payload["name"] = name
        payload["url"] = url
        screenWidth?.let { payload["screen_width"] = it }
        val cleanProps = Validation.cleanProps(props)
        if (cleanProps.isNotEmpty()) payload["props"] = cleanProps
        if (name != "pageview" && revenue != null) {
            val cleanRevenue = Validation.cleanRevenue(revenue)
            if (cleanRevenue == null) {
                report(SupermetryError.InvalidEvent("invalid revenue dropped"))
            } else {
                payload["revenue"] = cleanRevenue
            }
        }
        profileIdValue?.let { payload["profile_id"] = it }
        if (name == "identify" && !traits.isNullOrEmpty()) payload["traits"] = traits
        val body = Json.write(payload)
        if (Validation.utf8Length(body) > Validation.MAX_BODY_BYTES) {
            report(SupermetryError.InvalidEvent("event body exceeds 8 KB"))
            return null
        }
        return body
    }

    private fun enqueue(name: String, url: String, props: Map<String, Any?>, revenue: Revenue?, traits: Map<String, String>?) {
        if (optedOutValue) return
        val id = Validation.newEventId()
        val ts = clock()
        val body = buildBody(name, url, props, revenue, traits, id, ts) ?: return
        pending.add(QueuedEvent(id, ts, body))
        while (pending.size > config.maxQueueSize.coerceAtLeast(1)) pending.removeAt(0)
        queueStore.save(pending)
        if (retryFuture == null) drain()
    }

    /** Deliver queued events in order. Runs on the executor thread. */
    private fun drain() {
        val endpoint = endpoint ?: return
        while (!optedOutValue && pending.isNotEmpty()) {
            val event = pending[0]
            if (clock() - event.ts > MAX_EVENT_AGE_MS) {
                pending.removeAt(0)
                queueStore.save(pending)
                continue
            }
            val result = try {
                transport.send(endpoint, event.body, userAgent)
            } catch (e: Exception) {
                TransportResult(null, null, e)
            }
            val status = result.status
            when {
                result.error == null && status != null && status in 200..299 -> {
                    pending.removeAt(0)
                    queueStore.save(pending)
                }
                result.error == null && status != null && status in 400..499 && status != 408 && status != 429 -> {
                    pending.removeAt(0)
                    queueStore.save(pending)
                    report(SupermetryError.Rejected(status))
                }
                else -> {
                    val attempts = event.attempts + 1
                    pending[0] = event.copy(attempts = attempts)
                    queueStore.save(pending)
                    report(SupermetryError.Retrying(status, result.error))
                    scheduleRetry(attempts, result.retryAfterSeconds)
                    return
                }
            }
        }
    }

    private fun scheduleRetry(attempts: Int, retryAfterSeconds: Long?) {
        val executor = executor ?: return
        val exponent = (attempts - 1).coerceIn(0, 20)
        val backoff = (config.retryBaseDelayMs * (1L shl exponent)).coerceAtMost(config.maxRetryDelayMs)
        val delay = maxOf(backoff, (retryAfterSeconds ?: 0L) * 1000)
        retryFuture?.cancel(false)
        retryFuture = try {
            executor.schedule({
                retryFuture = null
                drain()
            }, delay, TimeUnit.MILLISECONDS)
        } catch (_: Exception) {
            null
        }
    }

    private fun submit(block: () -> Unit) {
        val executor = executor ?: return
        try {
            executor.execute {
                try {
                    block()
                } catch (e: Exception) {
                    report(SupermetryError.InvalidEvent("internal error: $e"))
                }
            }
        } catch (_: Exception) {
            // Executor shut down; drop silently.
        }
    }

    private fun report(error: SupermetryError) {
        try {
            config.onError?.invoke(error)
        } catch (_: Exception) {
        }
    }
}
