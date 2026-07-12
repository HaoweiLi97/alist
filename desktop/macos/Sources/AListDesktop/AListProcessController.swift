import Foundation
import Darwin

enum DesktopHostError: LocalizedError {
    case missingEmbeddedBinary(URL)
    case invalidConfiguration(URL)
    case noAvailablePort
    case failedToLaunch(String)
    case timedOut(URL)
    case launchAtLoginUnavailable

    var errorDescription: String? {
        switch self {
        case .missingEmbeddedBinary(let url):
            return "The embedded AList binary was not found at \(url.path). Rebuild the app bundle before launching."
        case .invalidConfiguration(let url):
            return "The AList configuration file is invalid JSON: \(url.path)"
        case .noAvailablePort:
            return "No available local port was found in the range 5244-5264."
        case .failedToLaunch(let reason):
            return "AList failed to launch: \(reason)"
        case .timedOut(let logURL):
            return "AList did not become ready within 15 seconds. Check the logs in \(logURL.path)."
        case .launchAtLoginUnavailable:
            return "Launch at Login requires macOS 13 or newer."
        }
    }
}

@MainActor
final class AListProcessController {
    private static let allowLANAccessDefaultsKey = "AListDesktop.allowLANAccess"

    let runtimeRoot: URL
    let dataDirectory: URL
    let logsDirectory: URL

    private let runDirectory: URL
    private let configURL: URL
    private let pidFileURL: URL
    private let hostLogURL: URL
    private let processLogURL: URL

    private var process: Process?
    private var processLogHandle: FileHandle?
    private var activeStartTask: Task<URL, Error>?
    private(set) var currentServiceURL: URL?
    private let defaults: UserDefaults

    init(fileManager: FileManager = .default, defaults: UserDefaults = .standard) {
        self.defaults = defaults
        let appSupport = fileManager.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        runtimeRoot = appSupport.appendingPathComponent("AListDesktop", isDirectory: true)
        dataDirectory = runtimeRoot.appendingPathComponent("data", isDirectory: true)
        logsDirectory = runtimeRoot.appendingPathComponent("logs", isDirectory: true)
        runDirectory = runtimeRoot.appendingPathComponent("run", isDirectory: true)
        configURL = dataDirectory.appendingPathComponent("config.json", isDirectory: false)
        pidFileURL = runDirectory.appendingPathComponent("alist.pid", isDirectory: false)
        hostLogURL = logsDirectory.appendingPathComponent("desktop.log", isDirectory: false)
        processLogURL = logsDirectory.appendingPathComponent("alist.log", isDirectory: false)
    }

    var allowsLANAccess: Bool {
        get { defaults.bool(forKey: Self.allowLANAccessDefaultsKey) }
        set { defaults.set(newValue, forKey: Self.allowLANAccessDefaultsKey) }
    }

    private var bindingHost: String {
        allowsLANAccess ? "0.0.0.0" : "127.0.0.1"
    }

    func start() async throws -> URL {
        if let activeStartTask {
            return try await activeStartTask.value
        }

        if let process, process.isRunning, let currentServiceURL {
            return currentServiceURL
        }

        let task = Task { @MainActor () throws -> URL in
            try await self.startImpl()
        }
        activeStartTask = task
        defer { activeStartTask = nil }
        return try await task.value
    }

    func restart() async throws -> URL {
        await stop()
        return try await start()
    }

    func stop() async {
        activeStartTask?.cancel()
        activeStartTask = nil

        guard let process else {
            removePidFile()
            return
        }

        appendHostLog("Stopping AList process \(process.processIdentifier)")

        if process.isRunning {
            process.terminate()
            let deadline = Date().addingTimeInterval(3)
            while process.isRunning && deadline.timeIntervalSinceNow > 0 {
                try? await Task.sleep(for: .milliseconds(100))
            }
            if process.isRunning {
                kill(process.processIdentifier, SIGKILL)
            }
        }

        cleanupAfterExit()
    }

    private func startImpl() async throws -> URL {
        try ensureRuntimeDirectories()
        try terminateOrphanedManagedProcessIfNeeded()

        let port = try prepareRuntimeConfiguration()
        let serviceURL = URL(string: "http://127.0.0.1:\(port)")!
        let binaryURL = try resolveEmbeddedBinaryURL()

        appendHostLog("Launching AList on port \(port) with host \(bindingHost)")

        let handle = try openLogHandle(at: processLogURL)
        let process = Process()
        process.executableURL = binaryURL
        process.arguments = ["server", "--data", dataDirectory.path, "--log-std"]
        process.currentDirectoryURL = dataDirectory
        process.standardOutput = handle
        process.standardError = handle
        process.environment = mergedEnvironment()
        process.terminationHandler = { [weak self] _ in
            Task { @MainActor in
                self?.appendHostLog("AList process exited")
                self?.cleanupAfterExit()
            }
        }

        do {
            try process.run()
        } catch {
            handle.closeFile()
            appendHostLog("Process launch failed: \(error.localizedDescription)")
            throw DesktopHostError.failedToLaunch(error.localizedDescription)
        }

        self.process = process
        self.processLogHandle = handle
        self.currentServiceURL = serviceURL
        try String(process.processIdentifier).write(to: pidFileURL, atomically: true, encoding: .utf8)

        do {
            try await waitUntilReady(at: serviceURL, timeout: 15)
            appendHostLog("AList became ready at \(serviceURL.absoluteString)")
            return serviceURL
        } catch {
            appendHostLog("AList failed to become ready: \(error.localizedDescription)")
            await stop()
            throw error
        }
    }

    private func cleanupAfterExit() {
        process = nil
        currentServiceURL = nil
        processLogHandle?.closeFile()
        processLogHandle = nil
        removePidFile()
    }

    private func ensureRuntimeDirectories() throws {
        let fileManager = FileManager.default
        try fileManager.createDirectory(at: runtimeRoot, withIntermediateDirectories: true)
        try fileManager.createDirectory(at: dataDirectory, withIntermediateDirectories: true)
        try fileManager.createDirectory(at: logsDirectory, withIntermediateDirectories: true)
        try fileManager.createDirectory(at: runDirectory, withIntermediateDirectories: true)
    }

    private func resolveEmbeddedBinaryURL() throws -> URL {
        if let overridePath = ProcessInfo.processInfo.environment["ALIST_DESKTOP_ALIST_BINARY"], !overridePath.isEmpty {
            let url = URL(fileURLWithPath: overridePath)
            if FileManager.default.isExecutableFile(atPath: url.path) {
                return url
            }
        }

        if let resourceURL = Bundle.main.resourceURL {
            let embedded = resourceURL.appendingPathComponent("bin/alist", isDirectory: false)
            if FileManager.default.isExecutableFile(atPath: embedded.path) {
                return embedded
            }
            throw DesktopHostError.missingEmbeddedBinary(embedded)
        }

        let fallback = URL(fileURLWithPath: CommandLine.arguments[0])
            .deletingLastPathComponent()
            .appendingPathComponent("../Resources/bin/alist")
            .standardizedFileURL
        if FileManager.default.isExecutableFile(atPath: fallback.path) {
            return fallback
        }
        throw DesktopHostError.missingEmbeddedBinary(fallback)
    }

    private func prepareRuntimeConfiguration() throws -> Int {
        let fileManager = FileManager.default
        let port = try choosePort()

        var root: [String: Any] = [:]
        if fileManager.fileExists(atPath: configURL.path) {
            let data = try Data(contentsOf: configURL)
            if !data.isEmpty {
                let json = try JSONSerialization.jsonObject(with: data, options: [])
                guard let object = json as? [String: Any] else {
                    appendHostLog("Configuration is not a JSON object")
                    throw DesktopHostError.invalidConfiguration(configURL)
                }
                root = object
            }
        }

        var scheme = root["scheme"] as? [String: Any] ?? [:]
        scheme["address"] = bindingHost
        scheme["http_port"] = port
        scheme["https_port"] = -1
        root["scheme"] = scheme
        if allowsLANAccess {
            root.removeValue(forKey: "site_url")
        } else {
            root["site_url"] = "http://127.0.0.1:\(port)"
        }

        let data = try JSONSerialization.data(withJSONObject: root, options: [.prettyPrinted, .sortedKeys])
        try data.write(to: configURL, options: .atomic)
        appendHostLog("Wrote runtime config to \(configURL.path)")
        return port
    }

    private func choosePort() throws -> Int {
        let preferredPorts = [5244] + Array(5245...5264)
        for port in preferredPorts where isPortAvailable(port) {
            return port
        }
        throw DesktopHostError.noAvailablePort
    }

    private func isPortAvailable(_ port: Int) -> Bool {
        let descriptor = socket(AF_INET, SOCK_STREAM, 0)
        guard descriptor >= 0 else { return false }
        defer { close(descriptor) }

        var value: Int32 = 1
        setsockopt(descriptor, SOL_SOCKET, SO_REUSEADDR, &value, socklen_t(MemoryLayout<Int32>.size))

        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = in_port_t(UInt16(port).bigEndian)
        address.sin_addr = in_addr(s_addr: inet_addr(bindingHost))

        let result = withUnsafePointer(to: &address) { pointer -> Int32 in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockaddrPointer in
                bind(descriptor, sockaddrPointer, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }

        return result == 0
    }

    private func waitUntilReady(at serviceURL: URL, timeout: TimeInterval) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if Task.isCancelled {
                throw CancellationError()
            }
            if let process, !process.isRunning {
                throw DesktopHostError.failedToLaunch("the AList process exited before becoming ready")
            }
            if await isServiceReachable(at: serviceURL) {
                return
            }
            try await Task.sleep(for: .milliseconds(250))
        }
        throw DesktopHostError.timedOut(logsDirectory)
    }

    private func isServiceReachable(at serviceURL: URL) async -> Bool {
        var request = URLRequest(url: serviceURL)
        request.timeoutInterval = 1
        request.cachePolicy = .reloadIgnoringLocalCacheData
        do {
            _ = try await URLSession.shared.data(for: request)
            return true
        } catch {
            return false
        }
    }

    private func terminateOrphanedManagedProcessIfNeeded() throws {
        let fileManager = FileManager.default
        guard fileManager.fileExists(atPath: pidFileURL.path) else { return }
        let rawPid = try String(contentsOf: pidFileURL, encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines)
        guard let pid = pid_t(rawPid) else {
            removePidFile()
            return
        }

        if kill(pid, 0) != 0 {
            removePidFile()
            return
        }

        appendHostLog("Found orphaned managed AList process \(pid), terminating it")
        kill(pid, SIGTERM)
        let deadline = Date().addingTimeInterval(2)
        while Date() < deadline {
            if kill(pid, 0) != 0 {
                removePidFile()
                return
            }
            Thread.sleep(forTimeInterval: 0.1)
        }

        kill(pid, SIGKILL)
        removePidFile()
    }

    private func mergedEnvironment() -> [String: String] {
        var environment = ProcessInfo.processInfo.environment
        environment["HOME"] = NSHomeDirectory()
        environment["TMPDIR"] = dataDirectory.appendingPathComponent("temp", isDirectory: true).path
        environment["ALIST_DESKTOP_MODE"] = "1"
        return environment
    }

    private func openLogHandle(at url: URL) throws -> FileHandle {
        let fileManager = FileManager.default
        if !fileManager.fileExists(atPath: url.path) {
            fileManager.createFile(atPath: url.path, contents: nil)
        }
        let handle = try FileHandle(forWritingTo: url)
        try handle.seekToEnd()
        return handle
    }

    private func appendHostLog(_ message: String) {
        let timestamp = ISO8601DateFormatter().string(from: Date())
        let line = "[\(timestamp)] \(message)\n"
        let data = Data(line.utf8)

        let fileManager = FileManager.default
        if !fileManager.fileExists(atPath: hostLogURL.path) {
            fileManager.createFile(atPath: hostLogURL.path, contents: nil)
        }

        if let handle = try? FileHandle(forWritingTo: hostLogURL) {
            _ = try? handle.seekToEnd()
            try? handle.write(contentsOf: data)
            try? handle.close()
        }
    }

    private func removePidFile() {
        try? FileManager.default.removeItem(at: pidFileURL)
    }
}

private extension pid_t {
    init?(_ string: String) {
        guard let value = Int(string) else { return nil }
        self = pid_t(value)
    }
}
