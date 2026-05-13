// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "NexusApp",
    platforms: [.macOS(.v14)],
    dependencies: [
        .package(url: "https://github.com/migueldeicaza/SwiftTerm.git", from: "1.2.0"),
        .package(url: "https://github.com/orlandos-nl/Citadel.git", from: "0.8.0"),
    ],
    targets: [
        // ── Core library: all business logic, models, client ──────────
        .target(
            name: "NexusCore",
            dependencies: [
                .product(name: "Citadel", package: "Citadel"),
            ],
            path: "Sources/NexusCore",
            swiftSettings: [
                .enableExperimentalFeature("StrictConcurrency"),
                .define("FEATURE_EXPORT_IMPORT"),
            ]
        ),

        // ── App executable: SwiftUI entry point + views ───────────────
        .executableTarget(
            name: "NexusApp",
            dependencies: [
                "NexusCore",
                .product(name: "SwiftTerm", package: "SwiftTerm"),
                .product(name: "Citadel", package: "Citadel"),
            ],
            path: "Sources/NexusApp",
            swiftSettings: [
                .enableExperimentalFeature("StrictConcurrency"),
                .define("FEATURE_EXPORT_IMPORT"),
            ]
        ),

        // ── Integration test suite ────────────────────────────────────
        .testTarget(
            name: "NexusAppTests",
            dependencies: ["NexusCore"],
            path: "Tests/NexusAppTests"
        ),
    ]
)
