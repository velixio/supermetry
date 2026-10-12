package com.supermetry

import java.math.BigDecimal
import java.math.RoundingMode
import java.net.URI
import java.net.URLEncoder
import java.util.UUID

/** Local validation mirroring the server limits (internal/ingest/payload.go). */
internal object Validation {
    const val MAX_BODY_BYTES = 8192
    const val MAX_NAME_BYTES = 300
    const val MAX_URL_BYTES = 2000
    const val MAX_PROPS = 30
    const val MAX_PROP_KEY_BYTES = 300
    const val MAX_PROP_VALUE_BYTES = 2000
    const val MAX_PROFILE_ID_CHARS = 128
    const val MAX_TRAITS = 20
    const val MAX_TRAIT_KEY_CHARS = 64
    const val MAX_TRAIT_VALUE_CHARS = 256
    const val MAX_SCREEN_WIDTH = 32767

    fun utf8Length(value: String): Int = value.toByteArray(Charsets.UTF_8).size

    /** Code point count, matching Go's utf8.RuneCountInString. */
    fun charCount(value: String): Int = value.codePointCount(0, value.length)

    fun validName(name: String): Boolean = name.isNotEmpty() && utf8Length(name) <= MAX_NAME_BYTES

    fun validUrl(value: String): Boolean {
        if (value.isEmpty() || utf8Length(value) > MAX_URL_BYTES) return false
        val uri = try {
            URI(value)
        } catch (_: Exception) {
            return false
        }
        val scheme = uri.scheme?.lowercase() ?: return false
        if (scheme != "http" && scheme != "https") return false
        if (uri.rawUserInfo != null) return false
        val host = uri.host
        return !host.isNullOrEmpty()
    }

    fun validProfileId(value: String): Boolean = value.isNotEmpty() && charCount(value) <= MAX_PROFILE_ID_CHARS

    /** String/number/boolean values with valid keys, sorted like the server, capped at 30. */
    fun cleanProps(props: Map<String, Any?>): Map<String, Any> {
        val clean = LinkedHashMap<String, Any>()
        for (key in props.keys.sorted()) {
            if (clean.size >= MAX_PROPS) break
            if (key.isEmpty() || utf8Length(key) > MAX_PROP_KEY_BYTES) continue
            when (val value = props[key]) {
                is String -> clean[key] = truncateUtf8(value, MAX_PROP_VALUE_BYTES)
                is Boolean -> clean[key] = value
                is Int, is Long, is Short, is Byte -> clean[key] = (value as Number).toLong()
                is Double -> if (!value.isNaN() && !value.isInfinite()) clean[key] = value
                is Float -> if (!value.isNaN() && !value.isInfinite()) clean[key] = value.toDouble()
                is BigDecimal -> clean[key] = value
                else -> Unit
            }
        }
        return clean
    }

    /** Drops invalid traits instead of letting the server reject the identify event. */
    fun cleanTraits(traits: Map<String, String>): Map<String, String> {
        val clean = LinkedHashMap<String, String>()
        for (key in traits.keys.sorted()) {
            if (clean.size >= MAX_TRAITS) break
            val value = traits[key] ?: continue
            if (key.isEmpty() || charCount(key) > MAX_TRAIT_KEY_CHARS) continue
            if (charCount(value) > MAX_TRAIT_VALUE_CHARS) continue
            clean[key] = value
        }
        return clean
    }

    /** `{amount, currency?}` with at most 6 decimals, or null when invalid. */
    fun cleanRevenue(revenue: Revenue): Map<String, Any>? {
        val rounded = revenue.amount.setScale(6, RoundingMode.HALF_UP)
        if (rounded.signum() <= 0) return null
        val out = LinkedHashMap<String, Any>()
        out["amount"] = rounded.stripTrailingZeros().toPlainString()
        val currency = revenue.currency
        if (!currency.isNullOrEmpty()) {
            val upper = currency.uppercase()
            if (upper.length != 3 || upper.any { it !in 'A'..'Z' }) return null
            out["currency"] = upper
        }
        return out
    }

    fun truncateUtf8(value: String, maxBytes: Int): String {
        if (utf8Length(value) <= maxBytes) return value
        val out = StringBuilder()
        var bytes = 0
        var i = 0
        while (i < value.length) {
            val cp = value.codePointAt(i)
            val size = when {
                cp < 0x80 -> 1
                cp < 0x800 -> 2
                cp < 0x10000 -> 3
                else -> 4
            }
            if (bytes + size > maxBytes) break
            bytes += size
            out.appendCodePoint(cp)
            i += Character.charCount(cp)
        }
        return out.toString()
    }

    /** 32 hex characters (UUID without dashes): a valid, stable dedup id. */
    fun newEventId(): String = UUID.randomUUID().toString().replace("-", "")

    /** `appUrl + "/" + screen path`, percent-encoding each path segment. */
    fun screenUrl(appUrl: String, screen: String): String {
        val base = appUrl.trimEnd('/')
        val path = screen.trim().trimStart('/')
        val encoded = path.split('/').joinToString("/") { segment ->
            URLEncoder.encode(segment, "UTF-8").replace("+", "%20")
        }
        return "$base/$encoded"
    }
}
