import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
#if canImport(UIKit)
import UIKit
#endif
#if canImport(AppKit) && os(macOS)
import AppKit
#endif

/// Configuration for a `Supermetry` client.
public struct SupermetryConfig {
    /// Public site ID.
    public var site: String
    /// Supermetry base URL, e.g. `https://analytics.supermetry.com`.
    public var host: String
    /// Base URL for screen views, e.g. `https://app.supermetry.com`. Its host must be an allowed
    /// host of the site.
    public var appUrl: String
    /// Full events endpoint; defaults to `host + "/api/public/v1/events"`.
    public var endpoint: String?
    /// When true every call is a no-op.
    public var disabled: Bool = false
    /// Overrides the browser-shaped default User-Agent.
    public var userAgent: String?
    /// Overrides the detected screen width (points).
    public var screenWidth: Int?
    /// Maximum queued events; the oldest are dropped beyond it.
    public var maxQueueSize: Int = 500
    /// First retry delay after a retryable failure; doubles per attempt.
    public var retryBaseDelay: TimeInterval = 2
    /// Upper bound for the retry delay.
    public var maxRetryDelay: TimeInterval = 300
    /// Flush when the app moves to the background.
    public var flushOnBackground: Bool = true
    /// Directory for the persisted queue; defaults to Application Support/Supermetry.
    public var storageDirectory: URL?
    /// Where the profile ID and opt-out flag are persisted.
    public var userDefaults: UserDefaults = .standard
    /// Session used for requests (e.g. for tests).
    public var urlSession: URLSession?
    /// Called (on an internal queue) for dropped events and delivery failures.
    public var onError: ((SupermetryError) -> Void)?

    public init(site: String, host: String, appUrl: String) {
        self.site = site
        self.host = host
        self.appUrl = appUrl
    }
}

/// Supermetry analytics client for Apple platforms.
///
/// ```swift
/// Supermetry.configure(site: "site_123", host: "https://analytics.supermetry.com", appUrl: "https://app.supermetry.com")
/// Supermetry.shared.screen("Checkout")
/// Supermetry.shared.track("Purchase", props: ["plan": "pro"], revenue: .init(49.9, currency: "USD"))
/// ```
///
/// All methods are thread-safe and return immediately; work happens on a private serial queue.
/// Events are persisted to disk and retried with the same `id`/`ts` on network errors, 429
/// and 5xx, and dropped on other 4xx.
public final class Supermetry: @unchecked Sendable {
    public static let version = "0.1.0"
    static let eventsPath = "/api/public/v1/events"
    static let maxEventAgeMS: Int64 = 24 * 60 * 60 * 1000

    /// Lock-protected holder for the shared instance.
    private final class SharedBox: @unchecked Sendable {
        let lock = NSLock()
        var instance: Supermetry?
    }

    private static let sharedBox = SharedBox()

    /// The instance created by `configure`, or a disabled no-op instance before that.
    public static var shared: Supermetry {
        sharedBox.lock.lock()
        defer { sharedBox.lock.unlock() }
        if let instance = sharedBox.instance { return instance }
        var config = SupermetryConfig(site: "", host: "", appUrl: "")
        config.disabled = true
        let placeholder = Supermetry(config: config)
        sharedBox.instance = placeholder
        return placeholder
    }

    /// Configure the shared instance.
    @discardableResult
    public static func configure(site: String, host: String, appUrl: String) -> Supermetry {
        configure(SupermetryConfig(site: site, host: host, appUrl: appUrl))
    }

    /// Configure the shared instance with full options.
    @discardableResult
    public static func configure(_ config: SupermetryConfig) -> Supermetry {
        let instance = Supermetry(config: config)
        sharedBox.lock.lock()
        let previous = sharedBox.instance
        sharedBox.instance = instance
        sharedBox.lock.unlock()
        previous?.stopObserving()
        return instance
    }

    // MARK: State (only touched on `queue`)

    private let config: SupermetryConfig
    private let queue = DispatchQueue(label: "com.supermetry.client")
    private let enabled: Bool
    private let endpoint: URL?
    private let session: URLSession
    private let userAgent: String
    private var screenWidth: Int?
    private let queueFile: URL?
    private let defaultsPrefix: String

    private var pending: [QueuedEvent] = []
    private var inFlight = false
    private var flushCompletions: [@Sendable () -> Void] = []
    private var retryWork: DispatchWorkItem?
    private var profileIDValue: String?
    private var optedOutValue: Bool
    private var currentScreenURL: String?
    private var observers: [NSObjectProtocol] = []

    /// Create a standalone client (most apps use `configure` + `shared` instead).
    public init(config: SupermetryConfig) {
        self.config = config
        self.defaultsPrefix = "supermetry.\(config.site)."
        self.userAgent = config.userAgent ?? Device.userAgent()
        self.screenWidth = config.screenWidth ?? Device.screenWidth()
        if let session = config.urlSession {
            self.session = session
        } else {
            let configuration = URLSessionConfiguration.default
            configuration.timeoutIntervalForRequest = 15
            self.session = URLSession(configuration: configuration)
        }

        var endpoint: URL?
        if let explicit = config.endpoint {
            endpoint = URL(string: explicit)
        } else if !config.host.isEmpty {
            var host = config.host
            while host.hasSuffix("/") { host.removeLast() }
            endpoint = URL(string: host + Supermetry.eventsPath)
        }
        self.endpoint = endpoint

        var problem: String?
        if config.disabled {
            problem = nil
        } else if config.site.isEmpty {
            problem = "site is required"
        } else if endpoint == nil || !Validation.validURL(endpoint!.absoluteString) {
            problem = "host must be an absolute http(s) URL"
        } else if !Validation.validURL(config.appUrl) {
            problem = "appUrl must be an absolute http(s) URL without credentials"
        }
        self.enabled = !config.disabled && problem == nil

        self.profileIDValue = config.userDefaults.string(forKey: defaultsPrefix + "profileId")
        self.optedOutValue = config.userDefaults.bool(forKey: defaultsPrefix + "optOut")

        if enabled {
            let directory = config.storageDirectory ?? Supermetry.defaultStorageDirectory()
            let safeSite = config.site.unicodeScalars
                .map { CharacterSet.alphanumerics.contains($0) || $0 == "_" || $0 == "-" ? String($0) : "_" }
                .joined()
            self.queueFile = directory.map { $0.appendingPathComponent("queue-\(safeSite).json") }
        } else {
            self.queueFile = nil
        }

        if let problem {
            config.onError?(.invalidConfiguration(problem))
            return
        }
        guard enabled else { return }

        if config.screenWidth == nil && screenWidth == nil {
            // Created off the main thread: read the screen on main and apply it later.
            DispatchQueue.main.async { [weak self] in
                guard let self, let width = Device.screenWidth() else { return }
                self.queue.async { self.screenWidth = width }
            }
        }
        queue.async {
            self.loadQueue()
            if self.optedOutValue { self.pending.removeAll(); self.persistQueue() }
            self.drain()
        }
        startObserving()
    }

    deinit {
        for observer in observers { NotificationCenter.default.removeObserver(observer) }
    }

    // MARK: Public API

    /// The identified profile ID, persisted across launches.
    public var profileId: String? { queue.sync { profileIDValue } }

    /// Whether tracking is opted out (persisted).
    public var isOptedOut: Bool { queue.sync { optedOutValue } }

    /// Whether the client is configured and enabled.
    public var isEnabled: Bool { enabled }

    /// Track a custom event.
    public func track(_ name: String, props: [String: SupermetryValue] = [:], revenue: SupermetryRevenue? = nil) {
        guard enabled else { return }
        queue.async {
            self.enqueue(name: name, url: self.currentScreenURL ?? self.config.appUrl, props: props, revenue: revenue, traits: nil)
        }
    }

    /// Track a screen view: a `pageview` with `url = appUrl + "/" + name`.
    public func screen(_ name: String, props: [String: SupermetryValue] = [:]) {
        guard enabled else { return }
        queue.async {
            let url = Validation.screenURL(appURL: self.config.appUrl, screen: name)
            self.currentScreenURL = url
            self.enqueue(name: "pageview", url: url, props: props, revenue: nil, traits: nil)
        }
    }

    /// Identify the user. Persists the profile ID so later events (and launches) carry it.
    /// Passing nil is the same as `reset()`.
    public func identify(_ profileId: String?, traits: [String: String] = [:]) {
        guard let profileId, !profileId.isEmpty else {
            reset()
            return
        }
        guard enabled else { return }
        queue.async {
            guard Validation.validProfileID(profileId) else {
                self.config.onError?(.invalidEvent("profile id must be 1-128 characters"))
                return
            }
            self.profileIDValue = profileId
            self.config.userDefaults.set(profileId, forKey: self.defaultsPrefix + "profileId")
            self.enqueue(name: "identify", url: self.currentScreenURL ?? self.config.appUrl, props: [:], revenue: nil,
                         traits: Validation.cleanTraits(traits))
        }
    }

    /// Forget the identified profile.
    public func reset() {
        queue.async {
            self.profileIDValue = nil
            self.config.userDefaults.removeObject(forKey: self.defaultsPrefix + "profileId")
        }
    }

    /// Stop tracking and discard queued events. Persisted across launches.
    public func optOut() {
        queue.async {
            self.optedOutValue = true
            self.config.userDefaults.set(true, forKey: self.defaultsPrefix + "optOut")
            self.pending.removeAll()
            self.retryWork?.cancel()
            self.persistQueue()
        }
    }

    /// Resume tracking after `optOut()`.
    public func optIn() {
        queue.async {
            self.optedOutValue = false
            self.config.userDefaults.set(false, forKey: self.defaultsPrefix + "optOut")
        }
    }

    /// Send queued events now, ignoring any retry backoff. `completion` runs (on an internal
    /// queue) once the queue is empty or a retryable failure stops delivery.
    public func flush(completion: (@Sendable () -> Void)? = nil) {
        guard enabled else {
            completion?()
            return
        }
        queue.async {
            if let completion { self.flushCompletions.append(completion) }
            self.retryWork?.cancel()
            self.retryWork = nil
            self.drain()
        }
    }

    /// Async variant of `flush(completion:)`.
    public func flush() async {
        await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
            flush { continuation.resume() }
        }
    }

    /// Number of events waiting to be delivered.
    public var queuedCount: Int { queue.sync { pending.count } }

    // MARK: Queueing

    private func enqueue(name: String, url: String, props: [String: SupermetryValue], revenue: SupermetryRevenue?,
                         traits: [String: String]?) {
        guard !optedOutValue else { return }
        guard Validation.validName(name) else {
            config.onError?(.invalidEvent("name must be 1-300 bytes"))
            return
        }
        guard Validation.validURL(url) else {
            config.onError?(.invalidEvent("url must be an absolute http(s) URL of at most 2000 bytes"))
            return
        }
        var payload = EventPayload(
            id: Validation.newEventID(),
            ts: Int64(Date().timeIntervalSince1970 * 1000),
            site: config.site,
            name: name,
            url: url
        )
        if let width = screenWidth, width >= 0, width <= Validation.maxScreenWidth {
            payload.screen_width = width
        }
        let cleanProps = Validation.cleanProps(props)
        if !cleanProps.isEmpty { payload.props = cleanProps }
        if name != "pageview", let revenue {
            payload.revenue = Validation.cleanRevenue(revenue)
            if payload.revenue == nil { config.onError?(.invalidEvent("invalid revenue dropped")) }
        }
        if let profileID = profileIDValue { payload.profile_id = profileID }
        if name == "identify", let traits, !traits.isEmpty { payload.traits = traits }

        guard let body = try? Supermetry.encode(payload), body.count <= Validation.maxBodyBytes else {
            config.onError?(.invalidEvent("event body exceeds 8 KB"))
            return
        }
        pending.append(QueuedEvent(payload: payload))
        if pending.count > config.maxQueueSize {
            pending.removeFirst(pending.count - config.maxQueueSize)
        }
        persistQueue()
        if retryWork == nil { drain() }
    }

    /// Deliver events one by one. Runs on `queue`.
    private func drain() {
        guard !inFlight else { return }
        guard !optedOutValue, let next = pending.first else {
            finishFlush()
            return
        }
        let nowMS = Int64(Date().timeIntervalSince1970 * 1000)
        if nowMS - next.payload.ts > Supermetry.maxEventAgeMS {
            pending.removeFirst()
            persistQueue()
            drain()
            return
        }
        guard let endpoint, let body = try? Supermetry.encode(next.payload) else {
            pending.removeFirst()
            persistQueue()
            drain()
            return
        }
        var request = URLRequest(url: endpoint)
        request.httpMethod = "POST"
        request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue(userAgent, forHTTPHeaderField: "User-Agent")
        inFlight = true
        let eventID = next.payload.id
        session.dataTask(with: request) { _, response, error in
            let status = (response as? HTTPURLResponse)?.statusCode
            let retryAfter = (response as? HTTPURLResponse)?.value(forHTTPHeaderField: "Retry-After").flatMap(TimeInterval.init)
            self.queue.async {
                self.inFlight = false
                self.handleResult(eventID: eventID, status: status, error: error, retryAfter: retryAfter)
            }
        }.resume()
    }

    private func handleResult(eventID: String, status: Int?, error: Error?, retryAfter: TimeInterval?) {
        let index = pending.firstIndex { $0.payload.id == eventID }
        if let status, error == nil, (200..<300).contains(status) {
            if let index { pending.remove(at: index) }
            persistQueue()
            drain()
            return
        }
        if let status, error == nil, status >= 400, status < 500, status != 408, status != 429 {
            if let index { pending.remove(at: index) }
            persistQueue()
            config.onError?(.rejected(status: status))
            drain()
            return
        }
        // Retryable: keep the event (same id/ts), back off.
        var attempts = 1
        if let index {
            pending[index].attempts += 1
            attempts = pending[index].attempts
        }
        persistQueue()
        config.onError?(.retrying(status: status, message: error.map { "\($0)" } ?? "no response"))
        let backoff = min(config.maxRetryDelay, config.retryBaseDelay * pow(2, Double(max(0, attempts - 1))))
        scheduleRetry(after: max(backoff, retryAfter ?? 0))
        finishFlush()
    }

    private func scheduleRetry(after delay: TimeInterval) {
        retryWork?.cancel()
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.retryWork = nil
            self.drain()
        }
        retryWork = work
        queue.asyncAfter(deadline: .now() + delay, execute: work)
    }

    private func finishFlush() {
        let completions = flushCompletions
        flushCompletions.removeAll()
        completions.forEach { $0() }
    }

    // MARK: Persistence

    static func encode(_ payload: EventPayload) throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return try encoder.encode(payload)
    }

    static func defaultStorageDirectory() -> URL? {
        guard let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else {
            return nil
        }
        return base.appendingPathComponent("Supermetry", isDirectory: true)
    }

    private func loadQueue() {
        guard let queueFile, let data = try? Data(contentsOf: queueFile) else { return }
        if let stored = try? JSONDecoder().decode([QueuedEvent].self, from: data) {
            pending = stored + pending
        }
    }

    private func persistQueue() {
        guard let queueFile else { return }
        do {
            if pending.isEmpty {
                if FileManager.default.fileExists(atPath: queueFile.path) {
                    try FileManager.default.removeItem(at: queueFile)
                }
                return
            }
            try FileManager.default.createDirectory(at: queueFile.deletingLastPathComponent(),
                                                    withIntermediateDirectories: true)
            try JSONEncoder().encode(pending).write(to: queueFile, options: .atomic)
        } catch {
            // The in-memory queue still works; persistence is best effort.
        }
    }

    // MARK: App lifecycle

    private func startObserving() {
        guard config.flushOnBackground else { return }
        let center = NotificationCenter.default
        var names: [Notification.Name] = []
        #if canImport(UIKit) && !os(watchOS)
        names = [UIApplication.didEnterBackgroundNotification, UIApplication.didBecomeActiveNotification]
        #elseif canImport(AppKit) && os(macOS)
        names = [NSApplication.didResignActiveNotification, NSApplication.willTerminateNotification]
        #endif
        observers = names.map { name in
            center.addObserver(forName: name, object: nil, queue: nil) { [weak self] _ in
                self?.flush()
            }
        }
    }

    private func stopObserving() {
        for observer in observers { NotificationCenter.default.removeObserver(observer) }
        observers.removeAll()
    }
}
