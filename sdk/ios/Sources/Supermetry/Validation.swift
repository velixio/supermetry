import Foundation

/// Local validation mirroring the server limits (internal/ingest/payload.go).
enum Validation {
    static let maxBodyBytes = 8192
    static let maxNameBytes = 300
    static let maxURLBytes = 2000
    static let maxProps = 30
    static let maxPropKeyBytes = 300
    static let maxPropValueBytes = 2000
    static let maxProfileIDChars = 128
    static let maxTraits = 20
    static let maxTraitKeyChars = 64
    static let maxTraitValueChars = 256
    static let maxScreenWidth = 32767

    static func validName(_ name: String) -> Bool {
        !name.isEmpty && name.utf8.count <= maxNameBytes
    }

    static func validURL(_ value: String) -> Bool {
        guard !value.isEmpty, value.utf8.count <= maxURLBytes,
              let components = URLComponents(string: value),
              let scheme = components.scheme?.lowercased(), scheme == "http" || scheme == "https",
              let host = components.host, !host.isEmpty,
              components.user == nil, components.password == nil
        else { return false }
        return true
    }

    /// Unicode scalar count, matching Go's utf8.RuneCountInString.
    static func charCount(_ value: String) -> Int { value.unicodeScalars.count }

    static func validProfileID(_ value: String) -> Bool {
        !value.isEmpty && charCount(value) <= maxProfileIDChars
    }

    /// Keeps valid keys (sorted, like the server), truncates long strings, caps at 30 keys.
    static func cleanProps(_ props: [String: SupermetryValue]) -> [String: SupermetryValue] {
        var clean: [String: SupermetryValue] = [:]
        for key in props.keys.sorted() {
            if clean.count >= maxProps { break }
            guard !key.isEmpty, key.utf8.count <= maxPropKeyBytes, let value = props[key] else { continue }
            switch value {
            case .string(let text):
                clean[key] = .string(truncateUTF8(text, maxBytes: maxPropValueBytes))
            case .double(let number):
                if number.isFinite { clean[key] = value }
            case .int, .bool:
                clean[key] = value
            }
        }
        return clean
    }

    /// Drops invalid traits instead of letting the server reject the whole identify event.
    static func cleanTraits(_ traits: [String: String]) -> [String: String] {
        var clean: [String: String] = [:]
        for key in traits.keys.sorted() {
            if clean.count >= maxTraits { break }
            guard let value = traits[key], !key.isEmpty,
                  charCount(key) <= maxTraitKeyChars, charCount(value) <= maxTraitValueChars
            else { continue }
            clean[key] = value
        }
        return clean
    }

    static func cleanRevenue(_ revenue: SupermetryRevenue) -> EventPayload.Revenue? {
        var amount = revenue.amount
        var rounded = Decimal()
        NSDecimalRound(&rounded, &amount, 6, .plain)
        guard rounded > 0 else { return nil }
        var currency: String?
        if let code = revenue.currency, !code.isEmpty {
            let upper = code.uppercased()
            guard upper.count == 3, upper.unicodeScalars.allSatisfy({ $0.value >= 65 && $0.value <= 90 }) else {
                return nil
            }
            currency = upper
        }
        return EventPayload.Revenue(amount: decimalString(rounded), currency: currency)
    }

    static func decimalString(_ value: Decimal) -> String {
        // Decimal's description is locale-independent ("49.9").
        "\(value)"
    }

    static func truncateUTF8(_ value: String, maxBytes: Int) -> String {
        if value.utf8.count <= maxBytes { return value }
        var result = ""
        var bytes = 0
        for scalar in value.unicodeScalars {
            let size = String(scalar).utf8.count
            if bytes + size > maxBytes { break }
            bytes += size
            result.unicodeScalars.append(scalar)
        }
        return result
    }

    /// 32 hex characters (UUID without dashes): a valid, stable dedup id.
    static func newEventID() -> String {
        UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased()
    }

    /// `appUrl + "/" + screen path`, percent-encoding the screen name.
    static func screenURL(appURL: String, screen: String) -> String {
        var base = appURL
        while base.hasSuffix("/") { base.removeLast() }
        var path = screen.trimmingCharacters(in: .whitespaces)
        while path.hasPrefix("/") { path.removeFirst() }
        let encoded = path.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? path
        return base + "/" + encoded
    }
}
