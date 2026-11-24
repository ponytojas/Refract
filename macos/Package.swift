// swift-tools-version: 6.0
import PackageDescription

// Refract.app is assembled from this package by build-app.sh (see the README,
// "Building the macOS app"). RefractCore holds everything that doesn't touch
// the screen (the server's lifecycle) so that it is tested with `swift test`.
let package = Package(
    name: "Refract",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "Refract", targets: ["Refract"])],
    dependencies: [
        // Exact: the framework is embedded in a signed app and verified by its checksum
        // (SwiftPM compares the zip against the one in Sparkle's own Package.swift at this tag).
        .package(url: "https://github.com/sparkle-project/Sparkle", exact: "2.10.1"),
    ],
    targets: [
        .target(name: "RefractCore"),
        .executableTarget(
            name: "Refract",
            dependencies: ["RefractCore", .product(name: "Sparkle", package: "Sparkle")]),
        .testTarget(name: "RefractCoreTests", dependencies: ["RefractCore"]),
    ],
    swiftLanguageModes: [.v5]
)
