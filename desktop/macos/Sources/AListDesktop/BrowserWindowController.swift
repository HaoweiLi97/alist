import AppKit
import WebKit

@MainActor
final class BrowserWindowController: NSWindowController, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate {
    var onOpenLogs: (() -> Void)?
    var onRestartService: (() -> Void)?
    var onOpenBrowser: (() -> Void)?

    private let webView: WKWebView
    private var serviceURL: URL?

    init() {
        let configuration = WKWebViewConfiguration()
        configuration.preferences.javaScriptCanOpenWindowsAutomatically = true
        configuration.websiteDataStore = .default()

        webView = WKWebView(frame: .zero, configuration: configuration)
        webView.autoresizingMask = [.width, .height]

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1200, height: 820),
            styleMask: [.titled, .closable, .resizable, .miniaturizable],
            backing: .buffered,
            defer: false
        )
        window.title = "AList"
        window.center()
        window.isReleasedWhenClosed = false
        window.setFrameAutosaveName("AListDesktopMainWindow")

        let contentView = NSView(frame: window.contentLayoutRect)
        contentView.autoresizesSubviews = true
        webView.frame = contentView.bounds
        contentView.addSubview(webView)
        window.contentView = contentView

        super.init(window: window)

        window.delegate = self
        webView.navigationDelegate = self
        webView.uiDelegate = self
        showLoading(status: "Starting AList...")
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        nil
    }

    func showWindowAndActivate() {
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func showLoading(status: String) {
        let title = "AList is starting"
        webView.loadHTMLString(
            htmlPage(
                title: title,
                message: status,
                showsActions: false
            ),
            baseURL: nil
        )
    }

    func showError(title: String, message: String) {
        webView.loadHTMLString(
            htmlPage(
                title: title,
                message: message,
                showsActions: true
            ),
            baseURL: nil
        )
    }

    func loadApp(at url: URL) {
        serviceURL = url
        if webView.url?.host == url.host, webView.url?.port == url.port {
            return
        }
        webView.load(URLRequest(url: url))
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        sender.orderOut(nil)
        return false
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction) async -> WKNavigationActionPolicy {
        guard let url = navigationAction.request.url else {
            return .allow
        }

        if url.scheme == "alistdesktop" {
            handleDesktopAction(for: url)
            return .cancel
        }

        if url.scheme == "about" {
            return .allow
        }

        if isLocalAppURL(url) {
            return .allow
        }

        NSWorkspace.shared.open(url)
        return .cancel
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let url = navigationAction.request.url {
            if isLocalAppURL(url) {
                webView.load(URLRequest(url: url))
            } else {
                NSWorkspace.shared.open(url)
            }
        }
        return nil
    }

    func webView(
        _ webView: WKWebView,
        runOpenPanelWith parameters: WKOpenPanelParameters,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping @MainActor @Sendable ([URL]?) -> Void
    ) {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = parameters.allowsDirectories
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        panel.canCreateDirectories = false
        panel.resolvesAliases = true
        panel.directoryURL = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first

        panel.beginSheetModal(for: window!) { response in
            completionHandler(response == .OK ? panel.urls : nil)
        }
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String, completionHandler: @escaping @MainActor @Sendable (URL?) -> Void) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = suggestedFilename
        panel.directoryURL = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first
        panel.begin { result in
            completionHandler(result == .OK ? panel.url : nil)
        }
    }

    func downloadDidFinish(_ download: WKDownload) {
        NSSound(named: NSSound.Name("Glass"))?.play()
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        showError(title: "Download failed", message: error.localizedDescription)
    }

    private func isLocalAppURL(_ url: URL) -> Bool {
        guard let serviceURL else { return false }
        guard let host = url.host?.lowercased() else { return false }
        guard ["127.0.0.1", "localhost"].contains(host) else { return false }
        return url.port == serviceURL.port
    }

    private func handleDesktopAction(for url: URL) {
        switch url.host?.lowercased() {
        case "restart":
            onRestartService?()
        case "open-logs":
            onOpenLogs?()
        case "open-browser":
            onOpenBrowser?()
        default:
            break
        }
    }

    private func htmlPage(title: String, message: String, showsActions: Bool) -> String {
        let escapedTitle = title.htmlEscaped
        let escapedMessage = message.htmlEscaped
        let actions = showsActions
            ? """
              <div class="actions">
                <a class="button primary" href="alistdesktop://restart">Restart Service</a>
                <a class="button" href="alistdesktop://open-browser">Open in Browser</a>
                <a class="button" href="alistdesktop://open-logs">Show Logs</a>
              </div>
              """
            : ""

        return """
        <!doctype html>
        <html lang="en">
        <head>
          <meta charset="utf-8" />
          <meta name="viewport" content="width=device-width,initial-scale=1" />
          <style>
            :root {
              color-scheme: light dark;
              --bg: #f6f4ef;
              --card: rgba(255, 255, 255, 0.88);
              --text: #1f2a2e;
              --muted: #5b666b;
              --line: rgba(31, 42, 46, 0.12);
              --accent: #136f63;
            }
            @media (prefers-color-scheme: dark) {
              :root {
                --bg: #152022;
                --card: rgba(21, 32, 34, 0.82);
                --text: #edf5f4;
                --muted: #b2c3c0;
                --line: rgba(237, 245, 244, 0.12);
              }
            }
            body {
              margin: 0;
              min-height: 100vh;
              display: grid;
              place-items: center;
              font-family: -apple-system, BlinkMacSystemFont, sans-serif;
              background:
                radial-gradient(circle at top left, rgba(19, 111, 99, 0.16), transparent 38%),
                radial-gradient(circle at bottom right, rgba(215, 128, 84, 0.16), transparent 40%),
                var(--bg);
              color: var(--text);
            }
            .card {
              width: min(640px, calc(100vw - 48px));
              padding: 28px;
              border-radius: 24px;
              background: var(--card);
              backdrop-filter: blur(18px);
              border: 1px solid var(--line);
              box-shadow: 0 20px 40px rgba(0, 0, 0, 0.08);
            }
            h1 {
              margin: 0 0 10px;
              font-size: 28px;
              line-height: 1.15;
            }
            p {
              margin: 0;
              line-height: 1.55;
              color: var(--muted);
              white-space: pre-wrap;
            }
            .actions {
              display: flex;
              gap: 12px;
              flex-wrap: wrap;
              margin-top: 24px;
            }
            .button {
              padding: 10px 16px;
              border-radius: 999px;
              border: 1px solid var(--line);
              color: var(--text);
              text-decoration: none;
              font-weight: 600;
            }
            .button.primary {
              background: var(--accent);
              color: white;
              border-color: var(--accent);
            }
          </style>
        </head>
        <body>
          <main class="card">
            <h1>\(escapedTitle)</h1>
            <p>\(escapedMessage)</p>
            \(actions)
          </main>
        </body>
        </html>
        """
    }
}

private extension String {
    var htmlEscaped: String {
        self
            .replacingOccurrences(of: "&", with: "&amp;")
            .replacingOccurrences(of: "<", with: "&lt;")
            .replacingOccurrences(of: ">", with: "&gt;")
            .replacingOccurrences(of: "\"", with: "&quot;")
            .replacingOccurrences(of: "'", with: "&#39;")
    }
}
