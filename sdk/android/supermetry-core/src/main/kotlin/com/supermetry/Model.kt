package com.supermetry

import java.math.BigDecimal

/**
 * Configuration for Supermetry.
 *
 * @property site public site ID.
 * @property host Supermetry base URL, e.g. `https://analytics.example.com`.
 * @property appUrl base URL for screen views, e.g. `https://app.example.com`; its host must be an
 *   allowed host of the site.
 */
data class SupermetryConfig @JvmOverloads constructor(
    val site: String,
    val host: String,
    val appUrl: String,
    /** Full events endpoint; defaults to `host + "/api/public/v1/events"`. */
    val endpoint: String? = null,
    /** When true every call is a no-op. */
    val disabled: Boolean = false,
    /** Overrides the browser-shaped default User-Agent. */
    val userAgent: String? = null,
    /** Overrides the detected screen width (dp, i.e. CSS pixels). */
    val screenWidth: Int? = null,
    /** Maximum queued events; the oldest are dropped beyond it. */
    val maxQueueSize: Int = 500,
    /** First retry delay after a retryable failure; doubles per attempt. */
    val retryBaseDelayMs: Long = 2_000,
    /** Upper bound for the retry delay. */
    val maxRetryDelayMs: Long = 300_000,
    /** Flush when the app moves to the background (Android). */
    val flushOnBackground: Boolean = true,
    val connectTimeoutMs: Int = 10_000,
    val readTimeoutMs: Int = 10_000,
    /** Called on the Supermetry worker thread for dropped events and delivery failures. */
    val onError: ((SupermetryError) -> Unit)? = null,
)

/** Errors reported through [SupermetryConfig.onError]. Tracking never throws. */
sealed class SupermetryError(message: String) {
    val message: String = message

    /** The event was invalid and was dropped locally. */
    class InvalidEvent(val reason: String) : SupermetryError("invalid event: $reason")

    /** The server rejected the event (4xx other than 408/429) and it was dropped. */
    class Rejected(val status: Int) : SupermetryError("event rejected with HTTP $status")

    /** Retryable failure (network, 408, 429, 5xx); the event stays queued with the same id/ts. */
    class Retrying(val status: Int?, val cause: Throwable?) :
        SupermetryError("delivery failed (${status?.let { "HTTP $it" } ?: cause?.toString() ?: "no response"}), will retry")

    /** The configuration is invalid; the client is disabled. */
    class InvalidConfiguration(val reason: String) : SupermetryError("invalid configuration: $reason")

    override fun toString(): String = "supermetry: $message"
}

/**
 * Revenue attached to a custom event (ignored on screen views).
 *
 * @property amount positive amount, rounded to 6 decimals.
 * @property currency ISO 4217 code; must match the site currency, or be null to use it.
 */
data class Revenue(val amount: BigDecimal, val currency: String? = null) {
    constructor(amount: Double, currency: String? = null) : this(BigDecimal.valueOf(amount), currency)
    constructor(amount: String, currency: String? = null) : this(BigDecimal(amount), currency)
}

/** A built event waiting for delivery. [body] is the exact JSON sent on every attempt. */
data class QueuedEvent(
    val id: String,
    val ts: Long,
    val body: String,
    val attempts: Int = 0,
)
