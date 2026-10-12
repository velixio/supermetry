package com.supermetry

import java.net.HttpURLConnection
import java.net.URL

/** Result of one delivery attempt: an HTTP status, or a network error. */
class TransportResult(val status: Int?, val retryAfterSeconds: Long? = null, val error: Throwable? = null)

/** Sends one event body. Implementations block; they run on the Supermetry worker thread. */
fun interface Transport {
    fun send(endpoint: String, body: String, userAgent: String): TransportResult
}

/** [Transport] on `HttpURLConnection` (no third-party HTTP client, so no `okhttp` UA). */
class HttpUrlConnectionTransport(
    private val connectTimeoutMs: Int = 10_000,
    private val readTimeoutMs: Int = 10_000,
) : Transport {
    override fun send(endpoint: String, body: String, userAgent: String): TransportResult {
        var connection: HttpURLConnection? = null
        return try {
            val bytes = body.toByteArray(Charsets.UTF_8)
            connection = (URL(endpoint).openConnection() as HttpURLConnection).apply {
                requestMethod = "POST"
                connectTimeout = connectTimeoutMs
                readTimeout = readTimeoutMs
                doOutput = true
                useCaches = false
                instanceFollowRedirects = false
                setFixedLengthStreamingMode(bytes.size)
                setRequestProperty("Content-Type", "application/json")
                setRequestProperty("User-Agent", userAgent)
            }
            connection.outputStream.use { it.write(bytes) }
            val status = connection.responseCode
            val retryAfter = connection.getHeaderField("Retry-After")?.trim()?.toLongOrNull()
            // Drain the body so the connection can be reused.
            try {
                (if (status >= 400) connection.errorStream else connection.inputStream)?.use { it.readBytes() }
            } catch (_: Exception) {
            }
            TransportResult(status, retryAfter)
        } catch (e: Exception) {
            TransportResult(null, null, e)
        } finally {
            connection?.disconnect()
        }
    }
}
