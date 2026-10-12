import Foundation

/// A custom-event property value: string, number or boolean (the server stringifies it).
public enum SupermetryValue: Codable, Equatable, Sendable {
    case string(String)
    case int(Int)
    case double(Double)
    case bool(Bool)

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Int.self) {
            self = .int(value)
        } else if let value = try? container.decode(Double.self) {
            self = .double(value)
        } else {
            self = .string(try container.decode(String.self))
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value): try container.encode(value)
        case .int(let value): try container.encode(value)
        case .double(let value): try container.encode(value)
        case .bool(let value): try container.encode(value)
        }
    }
}

extension SupermetryValue: ExpressibleByStringLiteral, ExpressibleByIntegerLiteral,
    ExpressibleByFloatLiteral, ExpressibleByBooleanLiteral
{
    public init(stringLiteral value: String) { self = .string(value) }
    public init(integerLiteral value: Int) { self = .int(value) }
    public init(floatLiteral value: Double) { self = .double(value) }
    public init(booleanLiteral value: Bool) { self = .bool(value) }
}

/// Revenue attached to a custom event. Ignored on screen views.
public struct SupermetryRevenue: Equatable, Sendable {
    /// Positive amount; rounded to 6 decimals.
    public var amount: Decimal
    /// ISO 4217 code (e.g. "USD"). Must match the site currency, or be nil to use it.
    public var currency: String?

    public init(amount: Decimal, currency: String? = nil) {
        self.amount = amount
        self.currency = currency
    }

    public init(_ amount: Double, currency: String? = nil) {
        self.init(amount: Decimal(string: String(format: "%.6f", amount)) ?? Decimal(amount), currency: currency)
    }
}

/// Wire format of one event (`POST /api/public/v1/events`).
struct EventPayload: Codable, Equatable {
    struct Revenue: Codable, Equatable {
        var amount: String
        var currency: String?
    }

    var v: Int = 1
    var id: String
    var ts: Int64
    var site: String
    var name: String
    var url: String
    var referrer: String?
    var screen_width: Int?
    var props: [String: SupermetryValue]?
    var revenue: Revenue?
    var profile_id: String?
    var traits: [String: String]?
}

/// A queued event and its delivery attempts, persisted to disk.
struct QueuedEvent: Codable, Equatable {
    var payload: EventPayload
    var attempts: Int = 0
}

/// Errors reported through `SupermetryConfig.onError`. Tracking never throws.
public enum SupermetryError: Error, Equatable, CustomStringConvertible {
    /// The event was invalid and was dropped locally.
    case invalidEvent(String)
    /// The server rejected the event (400) and it was dropped.
    case rejected(status: Int)
    /// A retryable failure (network, 429, 5xx); the event stays queued.
    case retrying(status: Int?, message: String)
    /// The configuration is invalid; the client is disabled.
    case invalidConfiguration(String)

    public var description: String {
        switch self {
        case .invalidEvent(let reason): return "supermetry: invalid event (\(reason))"
        case .rejected(let status): return "supermetry: event rejected with HTTP \(status)"
        case .retrying(let status, let message):
            return "supermetry: delivery failed (\(status.map { "HTTP \($0)" } ?? message)), will retry"
        case .invalidConfiguration(let reason): return "supermetry: invalid configuration (\(reason))"
        }
    }
}
