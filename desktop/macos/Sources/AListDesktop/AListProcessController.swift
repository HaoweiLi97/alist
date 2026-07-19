import Foundation
import Darwin

enum DesktopHostError: LocalizedError {
    case missingEmbeddedBinary(URL)
    case preferredPortUnavailable
    case noAvailablePort
    case startupTimedOut(TimeInterval)
    case failedToLaunch(String)
    case launchAtLoginUnavailable

    var errorDescription: String? {
        switch self {
        case .missingEmbeddedBinary(let url):
            return "The embedded AList binary was not found at \(url.path). Rebuild the app bundle before launching."
        case .preferredPortUnavailable:
            return "Port 5244 is already in use."
        case .noAvailablePort:
            return "No available local port was found in the range 5245-5264."
        case .startupTimedOut(let timeout):
            return "AList did not become ready within \(Int(timeout)) seconds. Check the desktop logs for details."
        case .failedToLaunch(let reason):
            return "AList failed to launch: \(reason)"
        case .launchAtLoginUnavailable:
            return "Launch at Login requires macOS 13 or newer."
        }
    }
}

private struct ManagedProcessRecord: Codable {
    let pid: Int32
    let executablePath: String
}

@MainActor
final class AListProcessController {
    private static let allowLANAccessDefaultsKey = "AListDesktop.allowLANAccess"
    private static let startupTimeout: TimeInterval = 30
    private static let processPathBufferSize = 4096
    private static let maximumDesktopLogSize = 2 * 1024 * 1024
    private static let desktopLogBackups = 3

    let runtimeRoot: URL
    let dataDirectory: URL
    let logsDirectory: URL

    private let runDirectory: URL
    private let pidFileURL: URL
    private let hostLogURL: URL
    private let processLogURL: URL

    private var process: Process?
    private var processLogHandle: FileHandle?
    private var activeStartTask: Task<URL, Error>?
    private(set) var currentServiceURL: URL?
    private var initialAdminPassword: String?
    private let defaults: UserDefaults

    init(fileManager: FileManager = .default, defaults: UserDefaults = .standard) {
        self.defaults = defaults
        let appSupport = fileManager.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        runtimeRoot = appSupport.appendingPathComponent("AListDesktop", isDirectory: true)
        dataDirectory = runtimeRoot.appendingPathComponent("data", isDirectory: true)
        logsDirectory = runtimeRoot.appendingPathComponent("logs", isDirectory: true)
        runDirectory = runtimeRoot.appendingPathComponent("run", isDirectory: true)
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

    func start(allowFallbackPort: Bool = false) async throws -> URL {
        if let activeStartTask {
            return try await activeStartTask.value
        }

        if let process, process.isRunning, let currentServiceURL {
            return currentServiceURL
        }

        let task = Task { @MainActor () throws -> URL in
            try await self.startImpl(allowFallbackPort: allowFallbackPort)
        }
        activeStartTask = task
        defer { activeStartTask = nil }
        return try await task.value
    }

    func restart(allowFallbackPort: Bool = false) async throws -> URL {
        await stop()
        return try await start(allowFallbackPort: allowFallbackPort)
    }

    func takeInitialAdminPassword() -> String? {
        defer { initialAdminPassword = nil }
        return initialAdminPassword
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

    private func startImpl(allowFallbackPort: Bool) async throws -> URL {
        try ensureRuntimeDirectories()
        try terminateOrphanedManagedProcessIfNeeded()

        let port = try choosePort(allowFallbackPort: allowFallbackPort)
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
        process.environment = mergedEnvironment(port: port)
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
        try writeManagedProcessRecord(pid: process.processIdentifier, executableURL: binaryURL)

        do {
            try await waitUntilReady(at: serviceURL, process: process)
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
        for directory in [
            runtimeRoot,
            dataDirectory,
            dataDirectory.appendingPathComponent("temp", isDirectory: true),
            logsDirectory,
            runDirectory,
        ] {
            try fileManager.createDirectory(
                at: directory,
                withIntermediateDirectories: true,
                attributes: [.posixPermissions: 0o700]
            )
            try fileManager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
        }
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

    private func choosePort(allowFallbackPort: Bool) throws -> Int {
        if isPortAvailable(5244) {
            return 5244
        }
        guard allowFallbackPort else {
            throw DesktopHostError.preferredPortUnavailable
        }
        for port in 5245...5264 where isPortAvailable(port) {
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

    private func waitUntilReady(at serviceURL: URL, process: Process) async throws {
        let deadline = Date().addingTimeInterval(Self.startupTimeout)
        while true {
            if Task.isCancelled {
                throw CancellationError()
            }
            if !process.isRunning {
                throw DesktopHostError.failedToLaunch("the AList process exited before becoming ready")
            }
            if await isServiceReachable(at: serviceURL) {
                return
            }
            if Date() >= deadline {
                throw DesktopHostError.startupTimedOut(Self.startupTimeout)
            }
            try await Task.sleep(for: .milliseconds(250))
        }
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
        guard let data = try? Data(contentsOf: pidFileURL),
              let record = try? JSONDecoder().decode(ManagedProcessRecord.self, from: data)
        else {
            appendHostLog("Discarding a legacy or invalid managed-process record")
            removePidFile()
            return
        }

        let pid = pid_t(record.pid)

        if kill(pid, 0) != 0 {
            removePidFile()
            return
        }

        guard let executablePath = executablePath(for: pid),
              normalizedPath(executablePath) == normalizedPath(record.executablePath)
        else {
            appendHostLog("Discarding stale process record for pid \(pid); executable did not match")
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

    private func mergedEnvironment(port: Int) -> [String: String] {
        var environment = ProcessInfo.processInfo.environment
        environment["HOME"] = NSHomeDirectory()
        environment["TMPDIR"] = dataDirectory.appendingPathComponent("temp", isDirectory: true).path
        environment["ALIST_DESKTOP_MODE"] = "1"
        environment["ALIST_SCHEME_ADDR"] = bindingHost
        environment["ALIST_SCHEME_HTTP_PORT"] = String(port)
        environment["ALIST_SCHEME_HTTPS_PORT"] = "-1"
        environment.removeValue(forKey: "ALIST_SITE_URL")
        if !allowsLANAccess {
            environment["ALIST_SITE_URL"] = "http://127.0.0.1:\(port)"
        }

        if !FileManager.default.fileExists(atPath: dataDirectory.appendingPathComponent("data.db").path),
           environment["ALIST_ADMIN_PASSWORD"]?.isEmpty != false {
            let password = UUID().uuidString.replacingOccurrences(of: "-", with: "")
            environment["ALIST_ADMIN_PASSWORD"] = password
            initialAdminPassword = password
        }
        return environment
    }

    private func openLogHandle(at url: URL) throws -> FileHandle {
        let fileManager = FileManager.default
        try rotateLogIfNeeded(at: url)
        if !fileManager.fileExists(atPath: url.path) {
            fileManager.createFile(atPath: url.path, contents: nil)
        }
        try fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
        let handle = try FileHandle(forWritingTo: url)
        try handle.seekToEnd()
        return handle
    }

    private func appendHostLog(_ message: String) {
        let timestamp = ISO8601DateFormatter().string(from: Date())
        let line = "[\(timestamp)] \(message)\n"
        let data = Data(line.utf8)

        let fileManager = FileManager.default
        try? rotateLogIfNeeded(at: hostLogURL)
        if !fileManager.fileExists(atPath: hostLogURL.path) {
            fileManager.createFile(atPath: hostLogURL.path, contents: nil)
        }
        try? fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: hostLogURL.path)

        if let handle = try? FileHandle(forWritingTo: hostLogURL) {
            _ = try? handle.seekToEnd()
            try? handle.write(contentsOf: data)
            try? handle.close()
        }
    }

    private func removePidFile() {
        try? FileManager.default.removeItem(at: pidFileURL)
    }

    private func writeManagedProcessRecord(pid: pid_t, executableURL: URL) throws {
        let record = ManagedProcessRecord(
            pid: Int32(pid),
            executablePath: normalizedPath(executableURL.path)
        )
        let data = try JSONEncoder().encode(record)
        try data.write(to: pidFileURL, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: pidFileURL.path)
    }

    private func executablePath(for pid: pid_t) -> String? {
        var buffer = [CChar](repeating: 0, count: Self.processPathBufferSize)
        let result = proc_pidpath(pid, &buffer, UInt32(buffer.count))
        guard result > 0 else { return nil }
        return String(
            decoding: buffer.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) },
            as: UTF8.self
        )
    }

    private func normalizedPath(_ path: String) -> String {
        URL(fileURLWithPath: path).resolvingSymlinksInPath().standardizedFileURL.path
    }

    private func rotateLogIfNeeded(at url: URL) throws {
        let fileManager = FileManager.default
        guard fileManager.fileExists(atPath: url.path) else { return }
        let attributes = try fileManager.attributesOfItem(atPath: url.path)
        guard let size = attributes[.size] as? NSNumber,
              size.intValue >= Self.maximumDesktopLogSize
        else {
            return
        }

        for index in stride(from: Self.desktopLogBackups, through: 1, by: -1) {
            let destination = url.appendingPathExtension("\(index)")
            let source = index == 1 ? url : url.appendingPathExtension("\(index - 1)")
            guard fileManager.fileExists(atPath: source.path) else { continue }
            if fileManager.fileExists(atPath: destination.path) {
                try fileManager.removeItem(at: destination)
            }
            try fileManager.moveItem(at: source, to: destination)
            try fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: destination.path)
        }
    }
}
