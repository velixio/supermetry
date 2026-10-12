import Foundation
#if canImport(UIKit)
import UIKit
#endif
#if canImport(AppKit) && os(macOS)
import AppKit
#endif

/// Browser-shaped User-Agent and screen width, so the server parses OS/device and does not
/// classify events as bot traffic (generic HTTP client UAs are on its crawler list).
enum Device {
    static func userAgent(version: String = Supermetry.version) -> String {
        "Mozilla/5.0 (\(platformToken())) Supermetry-iOS/\(version)"
    }

    static func osVersionToken() -> String {
        let v = ProcessInfo.processInfo.operatingSystemVersion
        var parts = ["\(v.majorVersion)", "\(v.minorVersion)"]
        if v.patchVersion > 0 { parts.append("\(v.patchVersion)") }
        return parts.joined(separator: "_")
    }

    static func platformToken() -> String {
        let version = osVersionToken()
        #if os(iOS) && targetEnvironment(macCatalyst)
        return "Macintosh; Intel Mac OS X \(version)"
        #elseif os(iOS)
        if hardwareModel().hasPrefix("iPad") {
            return "iPad; CPU OS \(version) like Mac OS X"
        }
        return "iPhone; CPU iPhone OS \(version) like Mac OS X"
        #elseif os(tvOS)
        return "Apple TV; CPU OS \(version) like Mac OS X"
        #elseif os(watchOS)
        return "Apple Watch; CPU Watch OS \(version) like Mac OS X"
        #elseif os(macOS)
        return "Macintosh; Intel Mac OS X \(version)"
        #elseif os(Linux)
        return "X11; Linux x86_64"
        #else
        return "Unknown"
        #endif
    }

    /// e.g. "iPhone15,2" / "iPad13,4"; the simulated model on the simulator.
    static func hardwareModel() -> String {
        if let simulated = ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] {
            return simulated
        }
        #if canImport(Darwin)
        var size = 0
        sysctlbyname("hw.machine", nil, &size, nil, 0)
        guard size > 0 else { return "" }
        var machine = [CChar](repeating: 0, count: size)
        sysctlbyname("hw.machine", &machine, &size, nil, 0)
        return String(cString: machine)
        #else
        return ""
        #endif
    }

    /// Screen width in points (CSS pixels), when it can be read synchronously on the main thread.
    static func screenWidth() -> Int? {
        #if canImport(UIKit) && !os(watchOS) && !os(visionOS)
        guard Thread.isMainThread else { return nil }
        return MainActor.assumeIsolated { Int(UIScreen.main.bounds.width) }
        #elseif os(watchOS) || os(visionOS)
        return nil
        #elseif canImport(AppKit) && os(macOS)
        guard Thread.isMainThread else { return nil }
        return MainActor.assumeIsolated { NSScreen.main.map { Int($0.frame.width) } }
        #else
        return nil
        #endif
    }
}
