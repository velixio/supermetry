// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "Supermetry",
    platforms: [
        .iOS(.v14),
        .macOS(.v12),
        .tvOS(.v14),
        .watchOS(.v7),
    ],
    products: [
        .library(name: "Supermetry", targets: ["Supermetry"]),
    ],
    targets: [
        .target(name: "Supermetry"),
        .testTarget(name: "SupermetryTests", dependencies: ["Supermetry"]),
    ]
)
