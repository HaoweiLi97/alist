import AppKit
import ServiceManagement

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let processController = AListProcessController()
    private var browserWindowController: BrowserWindowController?
    private var statusItem: NSStatusItem?
    private var statusMenu: NSMenu?
    private var launchAtLoginMenuItem: NSMenuItem?
    private var allowLANAccessMenuItem: NSMenuItem?

    func applicationDidFinishLaunching(_ notification: Notification) {
        configureStatusItem()
        updateLaunchAtLoginMenuItem()

        Task {
            do {
                let url = try await startWithPortRecovery()
                browserWindowController?.loadApp(at: url)
                presentInitialCredentialsIfNeeded()
            } catch {
                presentStartupError(error)
            }
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        openAList(nil)
        return true
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        Task {
            await processController.stop()
            sender.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }

    private func configureStatusItem() {
        let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        let statusImage = loadStatusBarImage()
        statusItem.button?.image = statusImage
        statusItem.button?.imagePosition = .imageOnly
        statusItem.button?.target = self
        statusItem.button?.action = #selector(handleStatusItemClick(_:))
        statusItem.button?.sendAction(on: [.leftMouseUp, .rightMouseUp])
        self.statusItem = statusItem

        let menu = NSMenu()
        menu.autoenablesItems = false
        menu.addItem(NSMenuItem(title: "Open AList", action: #selector(openAList(_:)), keyEquivalent: "o"))
        menu.addItem(NSMenuItem(title: "Open in Browser", action: #selector(openInBrowser(_:)), keyEquivalent: "b"))
        menu.addItem(NSMenuItem.separator())
        menu.addItem(NSMenuItem(title: "Restart Service", action: #selector(restartService(_:)), keyEquivalent: "r"))
        menu.addItem(NSMenuItem(title: "Show Data Directory", action: #selector(showDataDirectory(_:)), keyEquivalent: "d"))
        menu.addItem(NSMenuItem(title: "Show Logs Directory", action: #selector(showLogsDirectory(_:)), keyEquivalent: "l"))
        menu.addItem(NSMenuItem.separator())
        let launchAtLoginItem = NSMenuItem(title: "Launch at Login", action: #selector(toggleLaunchAtLogin(_:)), keyEquivalent: "")
        launchAtLoginItem.state = .off
        menu.addItem(launchAtLoginItem)
        self.launchAtLoginMenuItem = launchAtLoginItem
        let allowLANAccessItem = NSMenuItem(title: "Allow LAN Access", action: #selector(toggleAllowLANAccess(_:)), keyEquivalent: "")
        allowLANAccessItem.state = processController.allowsLANAccess ? .on : .off
        menu.addItem(allowLANAccessItem)
        allowLANAccessMenuItem = allowLANAccessItem
        menu.addItem(NSMenuItem.separator())
        menu.addItem(NSMenuItem(title: "Quit", action: #selector(quitApplication(_:)), keyEquivalent: "q"))

        for item in menu.items where item.action != nil {
            item.target = self
        }

        self.statusMenu = menu
    }

    private func loadStatusBarImage() -> NSImage {
        if
            let resourceURL = Bundle.main.resourceURL?.appendingPathComponent("MenuBarIcon.png"),
            let image = NSImage(contentsOf: resourceURL)
        {
            image.size = NSSize(width: 18, height: 18)
            image.isTemplate = true
            return image
        }

        let fallback = NSImage(
            systemSymbolName: "externaldrive.badge.wifi",
            accessibilityDescription: "AList"
        ) ?? NSImage()
        fallback.isTemplate = true
        return fallback
    }

    private func ensureBrowserWindowController() -> BrowserWindowController {
        if let browserWindowController {
            return browserWindowController
        }

        let controller = BrowserWindowController()
        controller.onOpenLogs = { [weak self] in
            self?.showLogsDirectory(nil)
        }
        controller.onOpenBrowser = { [weak self] in
            self?.openInBrowser(nil)
        }
        controller.onRestartService = { [weak self] in
            self?.restartService(nil)
        }
        browserWindowController = controller
        return controller
    }

    private func presentStartupError(_ error: Error) {
        let controller = ensureBrowserWindowController()
        controller.showError(
            title: "AList failed to start",
            message: error.localizedDescription
        )
        controller.showWindowAndActivate()
    }

    private func startWithPortRecovery(restarting: Bool = false) async throws -> URL {
        var shouldRestart = restarting
        while true {
            do {
                if shouldRestart {
                    return try await processController.restart()
                }
                return try await processController.start()
            } catch DesktopHostError.preferredPortUnavailable {
                shouldRestart = false
                if shouldUseAnotherPort() {
                    return try await processController.start(allowFallbackPort: true)
                }
            }
        }
    }

    private func shouldUseAnotherPort() -> Bool {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "Port 5244 is already in use"
        alert.informativeText = "AList can use another available port, or retry port 5244 after you close the program using it."
        alert.addButton(withTitle: "Use Another Port")
        alert.addButton(withTitle: "Retry 5244")
        return alert.runModal() == .alertFirstButtonReturn
    }

    @objc
    private func handleStatusItemClick(_ sender: Any?) {
        guard let event = NSApp.currentEvent else {
            openAList(nil)
            return
        }

        if event.type == .rightMouseUp, let button = statusItem?.button, let statusMenu {
            NSMenu.popUpContextMenu(statusMenu, with: event, for: button)
            return
        }

        openAList(nil)
    }

    @objc
    private func openAList(_ sender: Any?) {
        let controller = ensureBrowserWindowController()
        controller.showLoading(status: "Starting AList...")
        controller.showWindowAndActivate()

        Task {
            do {
                let url = try await startWithPortRecovery()
                controller.loadApp(at: url)
                controller.showWindowAndActivate()
                presentInitialCredentialsIfNeeded()
            } catch {
                controller.showError(
                    title: "AList failed to start",
                    message: error.localizedDescription
                )
                controller.showWindowAndActivate()
            }
        }
    }

    @objc
    private func openInBrowser(_ sender: Any?) {
        Task {
            do {
                let url = try await startWithPortRecovery()
                NSWorkspace.shared.open(url)
            } catch {
                presentStartupError(error)
            }
        }
    }

    @objc
    private func restartService(_ sender: Any?) {
        let controller = ensureBrowserWindowController()
        controller.showLoading(status: "Restarting AList...")
        controller.showWindowAndActivate()

        Task {
            do {
                let url = try await startWithPortRecovery(restarting: true)
                controller.loadApp(at: url)
                controller.showWindowAndActivate()
            } catch {
                controller.showError(
                    title: "AList failed to restart",
                    message: error.localizedDescription
                )
                controller.showWindowAndActivate()
            }
        }
    }

    @objc
    private func showDataDirectory(_ sender: Any?) {
        NSWorkspace.shared.activateFileViewerSelecting([processController.runtimeRoot])
    }

    @objc
    private func showLogsDirectory(_ sender: Any?) {
        NSWorkspace.shared.activateFileViewerSelecting([processController.logsDirectory])
    }

    @objc
    private func toggleLaunchAtLogin(_ sender: Any?) {
        Task {
            do {
                try toggleLaunchAtLogin()
                updateLaunchAtLoginMenuItem()
            } catch {
                presentStartupError(error)
            }
        }
    }

    private func toggleLaunchAtLogin() throws {
        guard #available(macOS 13.0, *) else {
            throw DesktopHostError.launchAtLoginUnavailable
        }

        let service = SMAppService.mainApp
        switch service.status {
        case .enabled:
            try service.unregister()
        case .notRegistered, .requiresApproval, .notFound:
            try service.register()
        @unknown default:
            try service.register()
        }
    }

    private func updateLaunchAtLoginMenuItem() {
        guard let item = launchAtLoginMenuItem else { return }

        guard #available(macOS 13.0, *) else {
            item.isEnabled = false
            item.state = .off
            return
        }

        item.isEnabled = true
        switch SMAppService.mainApp.status {
        case .enabled:
            item.state = .on
        case .requiresApproval, .notFound, .notRegistered:
            item.state = .off
        @unknown default:
            item.state = .off
        }
    }

    private func updateAllowLANAccessMenuItem() {
        allowLANAccessMenuItem?.state = processController.allowsLANAccess ? .on : .off
    }

    private func presentInitialCredentialsIfNeeded() {
        guard let password = processController.takeInitialAdminPassword() else { return }

        let alert = NSAlert()
        alert.alertStyle = .informational
        alert.messageText = "Save your AList admin password"
        alert.informativeText = "A secure password was created for the initial admin account.\n\nUsername: admin\nPassword: \(password)\n\nStore it in a password manager before continuing."
        alert.addButton(withTitle: "Copy Password")
        alert.addButton(withTitle: "Continue")
        if alert.runModal() == .alertFirstButtonReturn {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(password, forType: .string)
        }
    }

    @objc
    private func toggleAllowLANAccess(_ sender: Any?) {
        let previousValue = processController.allowsLANAccess
        let nextValue = !previousValue
        if nextValue && !confirmLANAccess() {
            return
        }
        processController.allowsLANAccess = nextValue
        updateAllowLANAccessMenuItem()

        let controller = ensureBrowserWindowController()
        controller.showLoading(status: nextValue ? "Enabling LAN access..." : "Disabling LAN access...")
        controller.showWindowAndActivate()

        Task {
            do {
                let url = try await startWithPortRecovery(restarting: true)
                controller.loadApp(at: url)
                controller.showWindowAndActivate()
            } catch {
                processController.allowsLANAccess = previousValue
                updateAllowLANAccessMenuItem()
                controller.showError(
                    title: "Failed to update LAN access",
                    message: error.localizedDescription
                )
                controller.showWindowAndActivate()
            }
        }
    }

    private func confirmLANAccess() -> Bool {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "Allow AList access from your local network?"
        alert.informativeText = "Anyone on your local network can reach this AList instance. Only enable this on a trusted network, keep your admin password private, and review your AList sharing settings."
        alert.addButton(withTitle: "Enable LAN Access")
        alert.addButton(withTitle: "Cancel")
        return alert.runModal() == .alertFirstButtonReturn
    }

    @objc
    private func quitApplication(_ sender: Any?) {
        NSApp.terminate(nil)
    }
}
