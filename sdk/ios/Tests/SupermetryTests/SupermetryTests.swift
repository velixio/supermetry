import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
import XCTest
@testable import Supermetry

/// Records requests and answers with scripted status codes (default 202).
final class StubURLProtocol: URLProtocol {
    struct Recorded {
        let request: URLRequest
        let body: Data
    }

    private static let lock = NSLock()
    private static var _requests: [Recorded] = []
    private static var _statuses: [Int] = []
    private static var _failNetwork = 0

    static func reset(statuses: [Int] = [], failNetwork: Int = 0) {
        lock.lock(); defer { lock.unlock() }
        _requests = []
        _statuses = statuses
        _failNetwork = failNetwork
    }

    static var requests: [Recorded] {
        lock.lock(); defer { lock.unlock() }
        return _requests
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody ?? Data()
        if body.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let read = stream.read(&buffer, maxLength: buffer.count)
                if read <= 0 { break }
                body.append(buffer, count: read)
            }
            stream.close()
        }
        Self.lock.lock()
        Self._requests.append(Recorded(request: request, body: body))
        let failNetwork = Self._failNetwork > 0
        if failNetwork { Self._failNetwork -= 1 }
        let status = Self._statuses.isEmpty ? 202 : Self._statuses.removeFirst()
        Self.lock.unlock()

        if failNetwork {
            client?.urlProtocol(self, didFailWithError: URLError(.notConnectedToInternet))
            return
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1",
                                       headerFields: ["Content-Length": "0"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data())
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

final class SupermetryTests: XCTestCase {
    private var directory: URL!
    private var defaults: UserDefaults!
    private var suiteName: String!
    private var errors: [SupermetryError] = []
    private let errorsLock = NSLock()

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        directory = FileManager.default.temporaryDirectory.appendingPathComponent("supermetry-tests-\(UUID().uuidString)")
        suiteName = "supermetry-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)!
        errors = []
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: directory)
        defaults.removePersistentDomain(forName: suiteName)
        super.tearDown()
    }

    private func makeClient(configure: (inout SupermetryConfig) -> Void = { _ in }) -> Supermetry {
        var config = SupermetryConfig(site: "site_123", host: "https://analytics.supermetry.com/", appUrl: "https://app.supermetry.com")
        let sessionConfig = URLSessionConfiguration.ephemeral
        sessionConfig.protocolClasses = [StubURLProtocol.self]
        config.urlSession = URLSession(configuration: sessionConfig)
        config.storageDirectory = directory
        config.userDefaults = defaults
        config.screenWidth = 390
        config.flushOnBackground = false
        config.retryBaseDelay = 60 // tests drive retries through flush()
        config.onError = { [weak self] error in
            guard let self else { return }
            self.errorsLock.lock(); self.errors.append(error); self.errorsLock.unlock()
        }
        configure(&config)
        return Supermetry(config: config)
    }

    private func flush(_ client: Supermetry) {
        let done = expectation(description: "flush")
        client.flush { done.fulfill() }
        wait(for: [done], timeout: 10)
    }

    private func json(_ index: Int) throws -> [String: Any] {
        let body = StubURLProtocol.requests[index].body
        return try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
    }

    func testScreenSendsPageviewWithContractFields() throws {
        let client = makeClient()
        client.screen("Checkout")
        flush(client)

        XCTAssertEqual(StubURLProtocol.requests.count, 1)
        let request = StubURLProtocol.requests[0].request
        XCTAssertEqual(request.url?.absoluteString, "https://analytics.supermetry.com/api/public/v1/events")
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")
        let ua = try XCTUnwrap(request.value(forHTTPHeaderField: "User-Agent"))
        XCTAssertTrue(ua.hasPrefix("Mozilla/5.0 ("), ua)
        XCTAssertTrue(ua.hasSuffix(") Supermetry-iOS/0.1.0"), ua)

        let body = try json(0)
        XCTAssertEqual(body["v"] as? Int, 1)
        XCTAssertEqual(body["site"] as? String, "site_123")
        XCTAssertEqual(body["name"] as? String, "pageview")
        XCTAssertEqual(body["url"] as? String, "https://app.supermetry.com/Checkout")
        XCTAssertEqual(body["screen_width"] as? Int, 390)
        let id = try XCTUnwrap(body["id"] as? String)
        XCTAssertNotNil(id.range(of: "^[0-9a-f]{32}$", options: .regularExpression))
        let ts = try XCTUnwrap((body["ts"] as? NSNumber)?.doubleValue)
        XCTAssertEqual(ts, Date().timeIntervalSince1970 * 1000, accuracy: 10_000)
        XCTAssertNil(body["profile_id"])
        XCTAssertNil(body["traits"])
        XCTAssertNil(body["revenue"])
    }

    func testTrackPropsAndRevenue() throws {
        let client = makeClient()
        client.screen("Cart/Summary")
        client.track("Purchase", props: ["plan": "pro", "seats": 3, "annual": true, "ratio": 0.5],
                     revenue: SupermetryRevenue(49.9, currency: "usd"))
        flush(client)

        XCTAssertEqual(StubURLProtocol.requests.count, 2)
        let body = try json(1)
        XCTAssertEqual(body["name"] as? String, "Purchase")
        XCTAssertEqual(body["url"] as? String, "https://app.supermetry.com/Cart/Summary")
        let props = try XCTUnwrap(body["props"] as? [String: Any])
        XCTAssertEqual(props["plan"] as? String, "pro")
        XCTAssertEqual(props["seats"] as? Int, 3)
        XCTAssertEqual(props["annual"] as? Bool, true)
        XCTAssertEqual(props["ratio"] as? Double, 0.5)
        let revenue = try XCTUnwrap(body["revenue"] as? [String: Any])
        XCTAssertEqual(revenue["amount"] as? String, "49.9")
        XCTAssertEqual(revenue["currency"] as? String, "USD")
    }

    func testRevenueIgnoredOnPageviewAndInvalidRevenueDropped() {
        XCTAssertNil(Validation.cleanRevenue(SupermetryRevenue(amount: 0)))
        XCTAssertNil(Validation.cleanRevenue(SupermetryRevenue(amount: 5, currency: "EURO")))
        XCTAssertEqual(Validation.cleanRevenue(SupermetryRevenue(amount: Decimal(string: "1.1234567")!))?.amount, "1.123457")
    }

    func testIdentifyPersistsProfileAndTraitsOnlyOnIdentify() throws {
        let client = makeClient()
        client.identify("user-42", traits: ["plan": "pro", "": "empty", "bio": String(repeating: "x", count: 257)])
        client.track("Clicked")
        flush(client)

        let identify = try json(0)
        XCTAssertEqual(identify["name"] as? String, "identify")
        XCTAssertEqual(identify["profile_id"] as? String, "user-42")
        XCTAssertEqual(identify["traits"] as? [String: String], ["plan": "pro"])
        let track = try json(1)
        XCTAssertEqual(track["profile_id"] as? String, "user-42")
        XCTAssertNil(track["traits"])

        // Persisted across launches.
        let relaunched = makeClient()
        XCTAssertEqual(relaunched.profileId, "user-42")

        relaunched.reset()
        XCTAssertNil(relaunched.profileId)
        XCTAssertNil(makeClient().profileId)
    }

    func testRetryKeepsIdAndTimestampThenSucceeds() throws {
        StubURLProtocol.reset(statuses: [503, 429])
        let client = makeClient()
        client.track("Signup")
        flush(client) // 503
        XCTAssertEqual(client.queuedCount, 1)
        flush(client) // 429
        XCTAssertEqual(client.queuedCount, 1)
        flush(client) // 202
        XCTAssertEqual(client.queuedCount, 0)

        XCTAssertEqual(StubURLProtocol.requests.count, 3)
        let first = try json(0), last = try json(2)
        XCTAssertEqual(first["id"] as? String, last["id"] as? String)
        XCTAssertEqual((first["ts"] as? NSNumber)?.int64Value, (last["ts"] as? NSNumber)?.int64Value)
    }

    func testNetworkFailureRetriesAndQueueSurvivesRelaunch() throws {
        StubURLProtocol.reset(failNetwork: 1)
        let client = makeClient()
        client.track("Offline")
        flush(client)
        XCTAssertEqual(client.queuedCount, 1)
        XCTAssertTrue(FileManager.default.fileExists(atPath: directory.appendingPathComponent("queue-site_123.json").path))
        let firstID = try json(0)["id"] as? String

        // A new client (next launch) loads the persisted queue and sends it with the same id.
        let relaunched = makeClient()
        flush(relaunched)
        XCTAssertEqual(relaunched.queuedCount, 0)
        let resent = StubURLProtocol.requests.map { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
        XCTAssertTrue(resent.dropFirst().contains { $0?["id"] as? String == firstID })
        XCTAssertFalse(FileManager.default.fileExists(atPath: directory.appendingPathComponent("queue-site_123.json").path))
    }

    func testBadRequestIsDropped() {
        StubURLProtocol.reset(statuses: [400])
        let client = makeClient()
        client.track("Bad")
        flush(client)
        XCTAssertEqual(client.queuedCount, 0)
        errorsLock.lock(); defer { errorsLock.unlock() }
        XCTAssertTrue(errors.contains(.rejected(status: 400)))
    }

    func testOptOutDropsEventsAndPersists() {
        let client = makeClient()
        client.optOut()
        client.track("Hidden")
        client.screen("Hidden")
        flush(client)
        XCTAssertEqual(StubURLProtocol.requests.count, 0)
        XCTAssertTrue(makeClient().isOptedOut)

        client.optIn()
        client.track("Visible")
        flush(client)
        XCTAssertEqual(StubURLProtocol.requests.count, 1)
        XCTAssertFalse(makeClient().isOptedOut)
    }

    func testPropsValidation() {
        var props: [String: SupermetryValue] = ["": "empty", "nan": .double(.nan), "a_long": .string(String(repeating: "é", count: 1500))]
        for i in 0..<40 { props["k\(String(format: "%02d", i))"] = .int(i) }
        let clean = Validation.cleanProps(props)
        XCTAssertEqual(clean.count, 30)
        XCTAssertNil(clean[""])
        XCTAssertNil(clean["nan"])
        if case .string(let long)? = clean["a_long"] {
            XCTAssertLessThanOrEqual(long.utf8.count, 2000)
        } else {
            XCTFail("long prop missing")
        }
    }

    func testInvalidEventsAreDroppedLocally() {
        let client = makeClient()
        client.track("")
        client.track(String(repeating: "n", count: 301))
        client.identify(String(repeating: "p", count: 129))
        client.track("Huge", props: Dictionary(uniqueKeysWithValues: (0..<10).map { ("k\($0)", SupermetryValue.string(String(repeating: "x", count: 1000))) }))
        flush(client)
        XCTAssertEqual(StubURLProtocol.requests.count, 0)
        errorsLock.lock(); defer { errorsLock.unlock() }
        XCTAssertEqual(errors.count, 4)
    }

    func testScreenURLBuilding() {
        XCTAssertEqual(Validation.screenURL(appURL: "https://app.supermetry.com/", screen: "/Settings/Profile"),
                       "https://app.supermetry.com/Settings/Profile")
        XCTAssertEqual(Validation.screenURL(appURL: "https://app.supermetry.com", screen: "Order Details"),
                       "https://app.supermetry.com/Order%20Details")
    }

    func testInvalidConfigurationDisablesClient() {
        let client = makeClient { $0.appUrl = "not a url" }
        XCTAssertFalse(client.isEnabled)
        client.track("x")
        flush(client)
        XCTAssertEqual(StubURLProtocol.requests.count, 0)
        XCTAssertFalse(makeClient { $0.disabled = true }.isEnabled)
        XCTAssertFalse(makeClient { $0.site = "" }.isEnabled)
    }

    func testCustomEndpointAndUserAgent() {
        let client = makeClient {
            $0.endpoint = "https://collector.supermetry.com/e"
            $0.userAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Supermetry-iOS/0.1.0"
        }
        client.track("x")
        flush(client)
        XCTAssertEqual(StubURLProtocol.requests.first?.request.url?.absoluteString, "https://collector.supermetry.com/e")
        XCTAssertEqual(StubURLProtocol.requests.first?.request.value(forHTTPHeaderField: "User-Agent"),
                       "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Supermetry-iOS/0.1.0")
    }

    func testSharedInstanceIsNoopUntilConfigured() async {
        XCTAssertFalse(Supermetry.shared.isEnabled)
        Supermetry.shared.track("ignored")
        await Supermetry.shared.flush()
    }

    func testUserAgentShape() {
        let ua = Device.userAgent()
        XCTAssertTrue(ua.hasPrefix("Mozilla/5.0 ("))
        XCTAssertTrue(ua.hasSuffix("Supermetry-iOS/0.1.0"))
    }
}
