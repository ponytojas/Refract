import AppKit
import RefractCore
import WebKit

/// The web app in a WKWebView, kept to the server's own origin.
final class WebViewController: NSViewController, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate {
    private let origin: URL
    private let webView: WKWebView
    private var loaded = false
    /// Where each running download is written (a temporary name) and where it ends up.
    private var downloadTargets: [ObjectIdentifier: (temp: URL, final: URL, replaces: Bool)] = [:]
    /// When the page process last ended: reloading stops after 3 in a minute.
    private var reloads: [Date] = []
    /// Called when the page can't be shown safely (the origin rules didn't compile).
    private let failed: (String) -> Void
    /// The space the page leaves for the traffic lights; see `WindowInset`.
    private(set) var inset: WindowInset

    init(origin: URL, inset: WindowInset, failed: @escaping (String) -> Void) {
        self.origin = origin
        self.inset = inset
        self.failed = failed
        let config = WKWebViewConfiguration()
        // The default store keeps what the web app saves in the browser (its settings), per
        // origin; the app asks for the same port every launch so the origin stays the same.
        config.websiteDataStore = .default()
        // window.open after an await (the GitHub device-flow page) counts as not user-initiated and
        // would be dropped. Safe: createWebViewWith never returns a window, and only the main
        // frame may send anything to the browser.
        config.preferences.javaScriptCanOpenWindowsAutomatically = true
        // The desktop marker and the inset exist before the page's first script runs (so the
        // first paint is already laid out), and only in the main frame.
        config.userContentController.addUserScript(
            WKUserScript(source: inset.script, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        webView = WKWebView(frame: .zero, configuration: config)
        super.init(nibName: nil, bundle: nil)
        config.userContentController.add(WindowMessageProxy(self), contentWorld: .page, name: windowMessageName)
        webView.navigationDelegate = self
        webView.uiDelegate = self
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    override func loadView() { view = webView }

    /// Full screen or a new window state changed the inset: tell the loaded page, and keep the
    /// script for the next load.
    func update(inset next: WindowInset) {
        guard next != inset else { return }
        inset = next
        let controller = webView.configuration.userContentController
        controller.removeAllUserScripts()
        controller.addUserScript(WKUserScript(source: next.script, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        webView.evaluateJavaScript(next.script, completionHandler: nil)
    }

    // MARK: Window drag

    /// The page asked to move the window (a mousedown on a drag region): only from the app's
    /// own main frame. The current event is the mouse press that caused the message.
    fileprivate func handle(_ message: WKScriptMessage) {
        let frame = message.frameInfo
        let source = frame.securityOrigin
        guard WindowMessage.parse(message.body, isMainFrame: frame.isMainFrame, scheme: source.protocol,
            host: source.host, port: source.port, origin: origin) != nil,
            let window = view.window, let event = NSApp.currentEvent, event.type == .leftMouseDown
        else { return }
        if event.clickCount == 2 {
            switch DoubleClickAction(setting: UserDefaults.standard.string(forKey: "AppleActionOnDoubleClick")) {
            case .zoom: window.performZoom(nil)
            case .minimize: window.performMiniaturize(nil)
            case .nothing: break
            }
        } else {
            window.performDrag(with: event)
        }
    }

    /// Sets the token cookie in the web view's own store, then loads the app: the very
    /// first request already carries it. token is nil for a terminal instance, which has none.
    func load(token: String?) {
        Task { @MainActor in
            // Nothing outside the origin loads, subresources included: the cookie would go
            // with it. Without the rules nothing loads at all.
            guard let rules = originOnlyRules(origin: origin) else {
                return refuse("no port in \(origin.absoluteString)")
            }
            do {
                let list = try await WKContentRuleListStore.default().compileContentRuleList(
                    forIdentifier: "refract-origin-only", encodedContentRuleList: rules)
                guard let list else { return refuse("the origin rules compiled to nothing") }
                webView.configuration.userContentController.removeAllContentRuleLists()
                webView.configuration.userContentController.add(list)
            } catch {
                return refuse("the origin rules don't compile: \(error.localizedDescription)")
            }
            let store = webView.configuration.websiteDataStore.httpCookieStore
            // Cookies ignore ports: drop the token of an earlier launch so that it never
            // goes to another server on this host (a terminal instance gets none).
            for old in await store.allCookies() where old.name == embedCookieName && old.domain == origin.host {
                await store.deleteCookie(old)
            }
            if let token {
                let cookie = HTTPCookie(properties: [
                    .name: embedCookieName,
                    .value: token,
                    .path: "/",
                    .originURL: origin,  // host only: no Domain attribute
                    .sameSitePolicy: HTTPCookieStringPolicy.sameSiteStrict,
                    HTTPCookiePropertyKey("HttpOnly"): "TRUE",
                ])
                if let cookie { await store.setCookie(cookie) }
            }
            webView.load(URLRequest(url: origin))
        }
    }

    private func refuse(_ reason: String) {
        AppLog.note("not loading the web app: \(reason)")
        failed(reason)
    }

    /// Calls window.refractFlush(): true when everything typed is saved, false when some
    /// of it couldn't be (or the page didn't answer in time), nil when no page is loaded.
    func flush() async -> Bool? {
        guard loaded else { return nil }
        // callAsyncJavaScript ignores cancellation, so a task group would wait for it past the
        // limit: whichever of the call and the timer finishes first resumes, once.
        return await withCheckedContinuation { (continuation: CheckedContinuation<Bool, Never>) in
            var resumed = false
            let finish: @MainActor (Bool) -> Void = { value in
                guard !resumed else { return }
                resumed = true
                continuation.resume(returning: value)
            }
            Task { @MainActor in
                let result = try? await self.webView.callAsyncJavaScript(
                    "return await window.refractFlush?.() ?? true", arguments: [:], in: nil, contentWorld: .page)
                finish((result as? Bool) ?? false)
            }
            Task { @MainActor in
                try? await Task.sleep(nanoseconds: 8_000_000_000)
                finish(false)
            }
        }
    }

    // MARK: Navigation

    /// Opens a URL outside the app, only for the schemes LinkPolicy allows. Nothing of the
    /// web view (its token cookie) goes along: the browser gets the bare URL.
    private func openExternally(_ url: URL?) {
        guard let url, LinkPolicy.classify(url, origin: origin) == .external else {
            AppLog.note("refused to open \(url?.scheme ?? "?"): link")
            return
        }
        NSWorkspace.shared.open(url)
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        let url = navigationAction.request.url
        switch LinkPolicy.classify(url, origin: origin) {
        case .ours:
            // data:, blob: and about: are ours only as downloads or inside frames; in the main frame
            // they would replace the app.
            let web = ["http", "https"].contains(url?.scheme?.lowercased() ?? "")
            if navigationAction.shouldPerformDownload {
                decisionHandler(.download)
            } else if web || !(navigationAction.targetFrame?.isMainFrame ?? true) {
                decisionHandler(.allow)
            } else {
                AppLog.note("blocked a main-frame navigation to a \(url?.scheme ?? "?"): page")
                decisionHandler(.cancel)
            }
        case .external:
            // Only the page itself sends the person elsewhere: a frame inside it (an embed, an ad)
            // can't open browser tabs on its own, whatever its target.
            if navigationAction.sourceFrame.isMainFrame { openExternally(url) }
            decisionHandler(.cancel)
        case .refused:
            AppLog.note("blocked navigation to a \(url?.scheme ?? "?"): link")
            decisionHandler(.cancel)
        }
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse,
        decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        let disposition = (navigationResponse.response as? HTTPURLResponse)?
            .value(forHTTPHeaderField: "Content-Disposition")?.lowercased() ?? ""
        let attachment = disposition.hasPrefix("attachment")
        decisionHandler(attachment || !navigationResponse.canShowMIMEType ? .download : .allow)
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        // target="_blank" and window.open never make a window: elsewhere goes to the browser.
        // The app's own pages are ignored: loading them here would skip the page's unsaved-work
        // guard, and nothing in the app needs a second window of itself.
        let url = navigationAction.request.url
        switch LinkPolicy.classify(url, origin: origin) {
        case .ours: AppLog.note("ignored a new window for the app's own page")
        case .external:
            if navigationAction.sourceFrame.isMainFrame { openExternally(url) }
        case .refused: AppLog.note("blocked a new window for a \(url?.scheme ?? "?"): link")
        }
        return nil
    }

    // MARK: Downloads

    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String,
        completionHandler: @escaping (URL?) -> Void) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = DownloadName.sanitize(suggestedFilename)
        panel.directoryURL = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first
        panel.canCreateDirectories = true
        let finish: (NSApplication.ModalResponse) -> Void = { result in
            if result == .OK, let url = panel.url {
                // The panel already asked about replacing, but the old file stays until the new one
                // is complete: download beside it under a temporary name, then move over it.
                let temp = url.deletingLastPathComponent()
                    .appendingPathComponent(".refract-download-\(UUID().uuidString)")
                // Only a file the user saw (and approved replacing) in the panel may be replaced later.
                let replaces = FileManager.default.fileExists(atPath: url.path)
                self.downloadTargets[ObjectIdentifier(download)] = (temp, url, replaces)
                completionHandler(temp)
            } else {
                completionHandler(nil)
            }
        }
        if let window = view.window {
            panel.beginSheetModal(for: window, completionHandler: finish)
        } else {
            finish(panel.runModal())
        }
    }

    func downloadDidFinish(_ download: WKDownload) {
        guard let target = downloadTargets.removeValue(forKey: ObjectIdentifier(download)) else { return }
        settle(temp: target.temp, final: target.final, replaces: target.replaces)
    }

    /// Puts the finished download in place. Without the user's approval to replace, nothing is
    /// overwritten: if the name was taken meanwhile, the user chooses again (or cancels).
    private func settle(temp: URL, final: URL, replaces: Bool) {
        let fm = FileManager.default
        do {
            if replaces && fm.fileExists(atPath: final.path) {
                _ = try fm.replaceItemAt(final, withItemAt: temp)
            } else {
                try fm.moveItem(at: temp, to: final)
            }
        } catch {
            if !replaces && fm.fileExists(atPath: final.path) {
                askAgain(temp: temp, taken: final)
                return
            }
            try? fm.removeItem(at: temp)
            reportDownloadFailure(error)
        }
    }

    private func askAgain(temp: URL, taken: URL) {
        let panel = NSSavePanel()
        panel.message = L("download.exists.message", "A file with that name appeared while downloading. Choose where to save it.")
        panel.nameFieldStringValue = taken.lastPathComponent
        panel.directoryURL = taken.deletingLastPathComponent()
        panel.canCreateDirectories = true
        let finish: (NSApplication.ModalResponse) -> Void = { result in
            if result == .OK, let url = panel.url {
                // The panel asked about replacing an existing file, so that choice is approved.
                self.settle(temp: temp, final: url, replaces: FileManager.default.fileExists(atPath: url.path))
            } else {
                try? FileManager.default.removeItem(at: temp)
            }
        }
        if let window = view.window {
            panel.beginSheetModal(for: window, completionHandler: finish)
        } else {
            finish(panel.runModal())
        }
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        let target = downloadTargets.removeValue(forKey: ObjectIdentifier(download))
        if let target { try? FileManager.default.removeItem(at: target.temp) }
        // Cancelling the save panel ends the download with a cancellation: not an error.
        if (error as NSError).code == NSURLErrorCancelled, target == nil { return }
        reportDownloadFailure(error)
    }

    private func reportDownloadFailure(_ error: Error) {
        AppLog.note("download failed: \(error.localizedDescription)")
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = L("download.failed.title", "The download failed")
        alert.informativeText = L("download.failed.message", "Refract couldn't save the file: %@")
            .replacingOccurrences(of: "%@", with: error.localizedDescription)
        alert.addButton(withTitle: L("download.failed.ok", "OK"))
        if let window = view.window { alert.beginSheetModal(for: window) } else { alert.runModal() }
    }

    // MARK: Dialogs

    /// window.confirm answers false without this: the web app asks before leaving a page with
    /// unsaved text.
    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void) {
        let alert = NSAlert()
        alert.messageText = message
        alert.addButton(withTitle: L("dialog.ok", "OK"))
        alert.addButton(withTitle: L("dialog.cancel", "Cancel"))
        if let window = view.window {
            alert.beginSheetModal(for: window) { completionHandler($0 == .alertFirstButtonReturn) }
        } else {
            completionHandler(alert.runModal() == .alertFirstButtonReturn)
        }
    }

    /// The web app doesn't call alert or prompt; without these WebKit would drop the message
    /// silently and answer null.
    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping () -> Void) {
        let alert = NSAlert()
        alert.messageText = message
        alert.addButton(withTitle: L("dialog.ok", "OK"))
        if let window = view.window {
            alert.beginSheetModal(for: window) { _ in completionHandler() }
        } else {
            alert.runModal()
            completionHandler()
        }
    }

    func webView(_ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String,
        defaultText: String?, initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping (String?) -> Void) {
        let alert = NSAlert()
        alert.messageText = prompt
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 260, height: 24))
        field.stringValue = defaultText ?? ""
        alert.accessoryView = field
        alert.addButton(withTitle: L("dialog.ok", "OK"))
        alert.addButton(withTitle: L("dialog.cancel", "Cancel"))
        alert.window.initialFirstResponder = field
        let finish: (NSApplication.ModalResponse) -> Void = {
            completionHandler($0 == .alertFirstButtonReturn ? field.stringValue : nil)
        }
        if let window = view.window { alert.beginSheetModal(for: window, completionHandler: finish) } else { finish(alert.runModal()) }
    }

    // MARK: File inputs

    func webView(_ webView: WKWebView, runOpenPanelWith parameters: WKOpenPanelParameters,
        initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping ([URL]?) -> Void) {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        // A directory input (webkitdirectory) picks folders only; a file input picks files.
        panel.canChooseDirectories = parameters.allowsDirectories
        panel.canChooseFiles = !parameters.allowsDirectories
        let finish: (NSApplication.ModalResponse) -> Void = { result in
            completionHandler(result == .OK ? panel.urls : nil)
        }
        if let window = view.window {
            panel.beginSheetModal(for: window, completionHandler: finish)
        } else {
            finish(panel.runModal())
        }
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        loaded = true
        AppLog.note("page loaded: \(webView.url?.absoluteString ?? "?")")
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        AppLog.note("page failed: \(error.localizedDescription)")
        failedBeforeLoad(error)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        AppLog.note("page failed to load: \(error.localizedDescription)")
        failedBeforeLoad(error)
    }

    /// The first load failing leaves an empty window (the server stopped between its
    /// readiness check and the page, say): show the error view, with Restart and the logs.
    /// Later failures leave the loaded page in place, and cancellations aren't failures.
    private func failedBeforeLoad(_ error: Error) {
        guard !loaded, !isCancellation(error) else { return }
        failed(String(format: L("error.pageLoad", "The page didn't load: %@"), error.localizedDescription))
    }

    private func isCancellation(_ error: Error) -> Bool {
        let e = error as NSError
        // -999: a newer load replaced it; 102: a navigation policy (ours) stopped it.
        return (e.domain == NSURLErrorDomain && e.code == NSURLErrorCancelled)
            || (e.domain == "WebKitErrorDomain" && e.code == 102)
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        let now = Date()
        reloads = reloads.filter { now.timeIntervalSince($0) < 60 }
        guard reloads.count < 3 else {
            refuse("the web content process keeps ending")
            return
        }
        reloads.append(now)
        AppLog.note("the web content process ended: reloading")
        webView.reload()
    }
}

let windowMessageName = "refractWindow"

/// The user content controller keeps its handlers alive: this one holds the controller weakly,
/// so the web view can go away.
private final class WindowMessageProxy: NSObject, WKScriptMessageHandler {
    weak var target: WebViewController?
    init(_ target: WebViewController) { self.target = target }

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.handle(message)
    }
}
