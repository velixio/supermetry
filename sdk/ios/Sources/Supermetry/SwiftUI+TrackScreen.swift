#if canImport(SwiftUI) && canImport(Combine)
import SwiftUI

private struct TrackScreenModifier: ViewModifier {
    let name: String
    let props: [String: SupermetryValue]
    let client: Supermetry?

    func body(content: Content) -> some View {
        content.onAppear {
            (client ?? Supermetry.shared).screen(name, props: props)
        }
    }
}

extension View {
    /// Sends a screen view (`pageview` for `appUrl/name`) each time this view appears.
    ///
    /// ```swift
    /// CheckoutView().trackScreen("Checkout")
    /// ```
    public func trackScreen(_ name: String, props: [String: SupermetryValue] = [:], client: Supermetry? = nil) -> some View {
        modifier(TrackScreenModifier(name: name, props: props, client: client))
    }
}
#endif
