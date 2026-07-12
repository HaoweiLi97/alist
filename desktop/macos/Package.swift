// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "AListDesktop",
    platforms: [
        .macOS(.v13)
    ],
    products: [
        .executable(
            name: "AListDesktop",
            targets: ["AListDesktop"]
        )
    ],
    targets: [
        .executableTarget(
            name: "AListDesktop",
            path: "Sources/AListDesktop"
        )
    ]
)
