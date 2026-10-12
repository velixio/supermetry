package com.supermetry

import com.sun.net.httpserver.HttpServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.math.BigDecimal
import java.net.InetSocketAddress
import java.util.Collections
import java.util.concurrent.ConcurrentLinkedQueue

class SupermetryClientTest {
    @get:Rule val tmp = TemporaryFolder()

    data class Recorded(val path: String, val headers: Map<String, String>, val body: String)

    private lateinit var server: HttpServer
    private val requests: MutableList<Recorded> = Collections.synchronizedList(ArrayList())
    private val statuses = ConcurrentLinkedQueue<Int>()
    private val errors: MutableList<SupermetryError> = Collections.synchronizedList(ArrayList())
    private val clients = ArrayList<SupermetryClient>()

    private val host get() = "http://127.0.0.1:${server.address.port}"

    @Before fun startServer() {
        server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/") { exchange ->
            val body = exchange.requestBody.readBytes().toString(Charsets.UTF_8)
            val headers = exchange.requestHeaders.entries.associate { it.key.lowercase() to it.value.joinToString(",") }
            requests.add(Recorded(exchange.requestURI.path, headers, body))
            val status = statuses.poll() ?: 202
            if (status == 429) exchange.responseHeaders.add("Retry-After", "1")
            exchange.sendResponseHeaders(status, -1)
            exchange.close()
        }
        server.start()
    }

    @After fun stop() {
        clients.forEach { it.shutdown() }
        server.stop(0)
    }

    private fun client(
        keyValueStore: KeyValueStore = InMemoryKeyValueStore(),
        queueStore: QueueStore = InMemoryQueueStore(),
        clock: () -> Long = { System.currentTimeMillis() },
        configure: (SupermetryConfig) -> SupermetryConfig = { it },
    ): SupermetryClient {
        val config = configure(
            SupermetryConfig(
                site = "site_123",
                host = "$host/",
                appUrl = "https://app.example.com",
                screenWidth = 412,
                retryBaseDelayMs = 60_000, // tests drive retries through flush()
                onError = { errors.add(it) },
            ),
        )
        return SupermetryClient(
            config, keyValueStore, queueStore,
            defaultUserAgent = "Mozilla/5.0 (Linux; Android 14; Pixel 8) Supermetry-Android/0.1.0",
            clock = clock,
        ).also { clients.add(it) }
    }

    private fun field(body: String, name: String): String? =
        Regex("\"" + Regex.escape(name) + "\":(\"(?:[^\"\\\\]|\\\\.)*\"|[^,}]+)").find(body)?.groupValues?.get(1)

    @Test fun screenSendsContractPayloadAndHeaders() {
        val c = client()
        c.screen("Checkout")
        assertTrue(c.flushBlocking())

        assertEquals(1, requests.size)
        val r = requests[0]
        assertEquals("/api/public/v1/events", r.path)
        assertEquals("application/json", r.headers["content-type"])
        assertEquals("Mozilla/5.0 (Linux; Android 14; Pixel 8) Supermetry-Android/0.1.0", r.headers["user-agent"])
        assertTrue(r.body, r.body.startsWith("{\"v\":1,\"id\":\""))
        assertTrue(Regex("^\"[0-9a-f]{32}\"$").matches(field(r.body, "id")!!))
        val ts = field(r.body, "ts")!!.toLong()
        assertTrue(Math.abs(System.currentTimeMillis() - ts) < 10_000)
        assertEquals("\"site_123\"", field(r.body, "site"))
        assertEquals("\"pageview\"", field(r.body, "name"))
        assertEquals("\"https://app.example.com/Checkout\"", field(r.body, "url"))
        assertEquals("412", field(r.body, "screen_width"))
        assertNull(field(r.body, "profile_id"))
        assertNull(field(r.body, "traits"))
    }

    @Test fun trackPropsAndRevenueAttributedToLastScreen() {
        val c = client()
        c.screen("/Cart/Order Details")
        c.track("Purchase", mapOf("plan" to "pro", "seats" to 3, "annual" to true, "ratio" to 0.5, "skip" to listOf(1)), Revenue(49.9, "usd"))
        assertTrue(c.flushBlocking())

        assertEquals(2, requests.size)
        val body = requests[1].body
        assertEquals("\"https://app.example.com/Cart/Order%20Details\"", field(body, "url"))
        assertTrue(body, body.contains("\"props\":{\"annual\":true,\"plan\":\"pro\",\"ratio\":0.5,\"seats\":3}"))
        assertTrue(body, body.contains("\"revenue\":{\"amount\":\"49.9\",\"currency\":\"USD\"}"))
    }

    @Test fun revenueIsIgnoredOnPageviewsAndValidated() {
        assertNull(Validation.cleanRevenue(Revenue(BigDecimal.ZERO)))
        assertNull(Validation.cleanRevenue(Revenue("5", "EURO")))
        assertEquals("1.123457", Validation.cleanRevenue(Revenue("1.1234567"))!!["amount"])
        assertEquals("10", Validation.cleanRevenue(Revenue(10.0))!!["amount"])
        val c = client()
        assertFalse(c.buildBody("pageview", "https://app.example.com/", emptyMap(), Revenue(1.0), null)!!.contains("revenue"))
    }

    @Test fun identifyPersistsProfileAndSendsTraitsOnlyOnIdentify() {
        val store = InMemoryKeyValueStore()
        val c = client(keyValueStore = store)
        c.identify("user-42", mapOf("plan" to "pro", "" to "x", "bio" to "x".repeat(257), "k".repeat(65) to "v"))
        c.track("Clicked")
        assertTrue(c.flushBlocking())

        val identify = requests[0].body
        assertEquals("\"identify\"", field(identify, "name"))
        assertEquals("\"user-42\"", field(identify, "profile_id"))
        assertTrue(identify, identify.contains("\"traits\":{\"plan\":\"pro\"}"))
        assertEquals("\"user-42\"", field(requests[1].body, "profile_id"))
        assertNull(field(requests[1].body, "traits"))

        assertEquals("user-42", client(keyValueStore = store).profileId)
        c.reset()
        c.flushBlocking()
        assertNull(c.profileId)
        assertNull(client(keyValueStore = store).profileId)

        c.identify(null)
        c.track("Anon")
        c.flushBlocking()
        assertNull(field(requests.last().body, "profile_id"))
    }

    @Test fun retryKeepsIdAndTimestamp() {
        statuses.addAll(listOf(503, 429))
        val c = client()
        c.track("Signup") // delivered immediately: 503, then backs off
        assertTrue(c.flushBlocking()) // flush ignores the backoff: 429
        assertEquals(1, c.queuedCount())
        assertTrue(c.flushBlocking()) // 202
        assertEquals(0, c.queuedCount())

        assertEquals(3, requests.size)
        assertEquals(requests[0].body, requests[2].body)
        assertTrue(errors.any { it is SupermetryError.Retrying && it.status == 503 })
        assertTrue(errors.any { it is SupermetryError.Retrying && it.status == 429 })
    }

    @Test fun badRequestIsDropped() {
        statuses.add(400)
        val c = client()
        c.track("Bad")
        c.track("Good")
        assertTrue(c.flushBlocking())
        assertEquals(0, c.queuedCount())
        assertEquals(2, requests.size)
        assertTrue(errors.any { it is SupermetryError.Rejected && it.status == 400 })
    }

    @Test fun networkFailureKeepsFileQueueAcrossLaunches() {
        val file = File(tmp.root, "supermetry/queue-site_123.txt")
        val down = client(queueStore = FileQueueStore(file)) { it.copy(endpoint = "http://127.0.0.1:1/api/public/v1/events") }
        down.track("Offline", mapOf("note" to "tab\tand\nnewline"))
        assertTrue(down.flushBlocking())
        assertEquals(1, down.queuedCount())
        assertTrue(file.exists())
        assertTrue(errors.any { it is SupermetryError.Retrying && it.status == null && it.cause != null })
        down.shutdown()

        val stored = FileQueueStore(file).load()
        assertEquals(1, stored.size)
        assertTrue(stored[0].attempts >= 1)

        val relaunched = client(queueStore = FileQueueStore(file))
        assertTrue(relaunched.flushBlocking())
        assertEquals(1, requests.size)
        assertEquals(stored[0].body, requests[0].body)
        assertEquals("\"${stored[0].id}\"", field(requests[0].body, "id"))
        assertTrue(requests[0].body.contains("\"note\":\"tab\\tand\\nnewline\""))
        assertFalse(file.exists())
    }

    @Test fun eventsOlderThan24HoursAreDiscarded() {
        val queue = InMemoryQueueStore()
        val old = System.currentTimeMillis() - SupermetryClient.MAX_EVENT_AGE_MS - 1000
        queue.save(listOf(QueuedEvent("old", old, "{}")))
        val c = client(queueStore = queue)
        assertTrue(c.flushBlocking())
        assertEquals(0, requests.size)
        assertEquals(0, c.queuedCount())
    }

    @Test fun optOutDropsEventsAndPersists() {
        val store = InMemoryKeyValueStore()
        val c = client(keyValueStore = store)
        c.optOut()
        c.track("Hidden")
        c.screen("Hidden")
        assertTrue(c.flushBlocking())
        assertEquals(0, requests.size)
        assertTrue(client(keyValueStore = store).isOptedOut)

        c.optIn()
        c.track("Visible")
        assertTrue(c.flushBlocking())
        assertEquals(1, requests.size)
        assertFalse(client(keyValueStore = store).isOptedOut)
    }

    @Test fun invalidEventsAreDroppedLocally() {
        val c = client()
        c.track("")
        c.track("n".repeat(301))
        c.identify("p".repeat(129))
        c.track("Huge", (0 until 10).associate { "k$it" to "x".repeat(1000) })
        assertTrue(c.flushBlocking())
        assertEquals(0, requests.size)
        assertEquals(4, errors.count { it is SupermetryError.InvalidEvent })
    }

    @Test fun propsAreCleanedAndCappedAt30() {
        val props = HashMap<String, Any?>()
        for (i in 0 until 40) props["k%02d".format(i)] = i
        props[""] = "empty"
        props["a_long"] = "é".repeat(1500)
        props["a_nan"] = Double.NaN
        props["a_null"] = null
        val clean = Validation.cleanProps(props)
        assertEquals(30, clean.size)
        assertFalse(clean.containsKey(""))
        assertFalse(clean.containsKey("a_nan"))
        assertFalse(clean.containsKey("a_null"))
        assertTrue(Validation.utf8Length(clean["a_long"] as String) <= 2000)
    }

    @Test fun invalidConfigurationDisablesClient() {
        assertFalse(client { it.copy(appUrl = "not a url") }.isEnabled)
        assertFalse(client { it.copy(site = "") }.isEnabled)
        assertFalse(client { it.copy(disabled = true) }.isEnabled)
        assertTrue(errors.any { it is SupermetryError.InvalidConfiguration })
        val disabled = client { it.copy(disabled = true) }
        disabled.track("x")
        assertTrue(disabled.flushBlocking())
        assertEquals(0, requests.size)
    }

    @Test fun jsonEscaping() {
        assertEquals("{\"a\":\"q\\\"b\\\\s\\u0001\\u2028\",\"n\":1,\"d\":1.5,\"t\":true}",
            Json.write(linkedMapOf("a" to "q\"b\\s\u0001 ", "n" to 1, "d" to 1.5, "t" to true)))
    }

    @Test fun urlValidationAndScreenUrls() {
        assertTrue(Validation.validUrl("https://app.example.com/x?y=1"))
        assertFalse(Validation.validUrl("ftp://app.example.com/"))
        assertFalse(Validation.validUrl("https://user:pass@app.example.com/"))
        assertFalse(Validation.validUrl("/relative"))
        assertEquals("https://app.example.com/Settings/Profile", Validation.screenUrl("https://app.example.com/", "/Settings/Profile"))
        assertNotNull(Validation.newEventId())
    }
}
