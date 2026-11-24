import AppKit
import RefractCore

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var supervisor: ServerSupervisor!
    private var root: RootViewController!
    private var window: NSWindow!
    private var quitting = false
    private var pendingStops: [(Bool) -> Void] = []
    private var terminateReplyPending = false
    private var updates: UpdateController!
    /// A restart was asked for: only then is "stopping" the first half of one.
    private var restarting = false
    private let portKey = "serverPort"
    /// `REFRACT_APP_DEFAULTS_SUITE` names a separate UserDefaults suite (scratch runs and tests).
    private static let defaults: UserDefaults = {
        if let suite = ProcessInfo.processInfo.environment["REFRACT_APP_DEFAULTS_SUITE"], !suite.isEmpty,
           let d = UserDefaults(suiteName: suite) { return d }
        return .standard
    }()

    func applicationDidFinishLaunching(_ notification: Notification) {
        if activateRunningInstance() {
            exit(0)
        }
        let layout = BundleLayout()
        var config = ServerSupervisor.Config(
            executable: layout.server,
            environment: ServerSupervisor.serverEnvironment(from: ProcessInfo.processInfo.environment),
            log: AppLog.store,
            layout: layout)
        // Same data folder as `refract local`: no REFRACT_DATA_DIR, so the server uses
        // ~/Library/Application Support/Refract.
        config.workingDirectory = NSHomeDirectory()
        supervisor = ServerSupervisor(config: config)
        updates = UpdateController(settings: UpdateSettings.resolve(
            info: Bundle.main.infoDictionary ?? [:], environment: ProcessInfo.processInfo.environment))
        updates.stopServer = { [weak self] forUpdate, done in self?.stopServer(forUpdate: forUpdate, then: done) }
        updates.resumeServer = { [weak self] in
            guard let self else { return }
            self.quitting = false
            self.supervisor.restart()
        }
        installMenu()
        let saved = Self.defaults.integer(forKey: portKey)
        supervisor.preferredPort = saved > 0 ? saved : nil

        root = RootViewController(
            restart: { [weak self] in
                self?.restarting = true
                self?.supervisor.restart()
            },
            showLogs: { NSWorkspace.shared.activateFileViewerSelecting([AppLog.store.file]) },
            reinstall: { NSWorkspace.shared.open(URL(string: "https://github.com/ponytojas/Refract/releases/latest")!) })
        window = NSWindow(contentViewController: root)
        // No title bar of its own: the web app reaches the top edge, the traffic lights float
        // over it. The title stays (Mission Control, the Window menu, VoiceOver) but isn't drawn.
        window.styleMask = [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView]
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.title = "Refract"
        window.minSize = NSSize(width: 640, height: 420)
        window.setContentSize(NSSize(width: 1280, height: 820))
        window.setFrameAutosaveName("RefractMainWindow")
        if !window.setFrameUsingName("RefractMainWindow") { window.center() }
        window.makeKeyAndOrderFront(nil)
        root.updateInset(for: window)
        for name in [NSWindow.didEnterFullScreenNotification, NSWindow.didExitFullScreenNotification,
            NSWindow.didResizeNotification, NSWindow.didEndLiveResizeNotification,
            NSWindow.didBecomeKeyNotification, NSWindow.didChangeScreenNotification] {
            NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) { [weak self] _ in
                guard let self else { return }
                self.root.updateInset(for: self.window)
            }
        }
        // AppKit lays the title bar out again for its own reasons (appearance, tabs, full screen):
        // when its container changes, the lights go back where the bars expect them.
        if let container = window.standardWindowButton(.closeButton)?.superview?.superview {
            NotificationCenter.default.addObserver(
                forName: NSView.frameDidChangeNotification, object: container, queue: .main
            ) { [weak self] _ in
                guard let self else { return }
                self.root.updateInset(for: self.window)
            }
        }
        NSApp.activate(ignoringOtherApps: true)

        supervisor.onChange = { [weak self] in self?.apply($0) }
        root.showLoading(L("loading.starting", "Starting Refract…"))
        supervisor.start()
    }

    /// A second launch of the app (a copy elsewhere, or a launch from the terminal) brings
    /// the first one forward instead of starting a second server on the same data folder.
    private func activateRunningInstance() -> Bool {
        guard let id = Bundle.main.bundleIdentifier else { return false }
        let others = NSRunningApplication.runningApplications(withBundleIdentifier: id)
            .filter { $0.processIdentifier != ProcessInfo.processInfo.processIdentifier }
        guard let other = others.first else { return false }
        other.activate(options: [.activateIgnoringOtherApps])
        return true
    }

    private func apply(_ state: ServerState) {
        guard !quitting else { return }
        switch state {
        case .idle, .starting:
            restarting = false
            root.showLoading(L("loading.starting", "Starting Refract…"))
        case .stopping, .stopped:
            // A stop that isn't a restart (a damaged install) goes on to its own view.
            if restarting { root.showLoading(L("loading.restarting", "Restarting Refract…")) }
        case .ready(let url, let owns):
            if owns, let port = url.port {
                Self.defaults.set(port, forKey: portKey)
                supervisor.preferredPort = port
            }
            let token = owns ? supervisor.token : nil
            root.showWeb(url: url, token: token)
            if owns { checkTools(url: url, token: token) }
        case .failed(let failure):
            // Not the failure itself: its log tail is already in the log.
            AppLog.note("showing the error view: \(String(describing: failure).prefix(while: { $0 != "(" }))")
            root.showError(failure)
        }
    }

    /// The server logs and reports (/api/me) the tools it couldn't find: that is a damaged
    /// install, whatever the person's PATH has.
    private func checkTools(url: URL, token: String?) {
        Task { @MainActor in
            let missing = await fetchMissingTools(url: url, token: token)
            guard !missing.isEmpty, case .ready(let now, true) = supervisor.state, now == url else { return }
            AppLog.note("the server reports missing tools: \(missing.joined(separator: ", "))")
            supervisor.reportMissingTools(missing)
        }
    }

    // MARK: Quitting

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let supervisor else { return .terminateNow }
        // A terminal instance has nothing to stop, but its page may hold unsaved typing.
        switch supervisor.state {
        case .starting, .stopping, .ready: break
        default: return .terminateNow
        }
        // A second ⌘Q while one is pending waits for the same reply.
        if terminateReplyPending { return .terminateLater }
        terminateReplyPending = true
        stopServer(forUpdate: false) { proceed in
            self.terminateReplyPending = false
            if proceed { AppLog.note("quitting after the server stopped") }
            NSApp.reply(toApplicationShouldTerminate: proceed)
        }
        return .terminateLater
    }

    /// The quit sequence up to the server's end, shared by ⌘Q and by an update's relaunch: the
    /// page saves what was typed (asking, if it can't), standard input closes, and the server
    /// finishes its requests and closes the database (killed only after 30 seconds, see
    /// ServerSupervisor). Calls back `true` once the server is gone, `false` if the person
    /// chose to keep working. Calls arriving while one runs wait for its result.
    private func stopServer(forUpdate: Bool = false, then done: @escaping (Bool) -> Void) {
        pendingStops.append(done)
        guard pendingStops.count == 1 else { return }
        switch supervisor.state {
        case .starting, .stopping, .ready: break
        default:
            finishStop(true)
            return
        }
        quitting = true
        Task { @MainActor in
            // What was typed in the last moment only exists in the page: save it first.
            if case .ready = supervisor.state, await root.flushPage() == false {
                let alert = NSAlert()
                alert.alertStyle = .warning
                alert.messageText = L("quit.unsaved.title", "Some changes couldn't be saved")
                if forUpdate {
                    alert.informativeText = L("update.unsaved.message", "Refract couldn't save everything you typed (the project may be locked or gone). Install the update anyway and lose it?")
                    alert.addButton(withTitle: L("update.unsaved.cancel", "Not Now"))
                    alert.addButton(withTitle: L("update.unsaved.install", "Install Anyway"))
                } else {
                    alert.informativeText = L("quit.unsaved.message", "Refract couldn't save everything you typed (the project may be locked or gone). Quit anyway and lose it?")
                    alert.addButton(withTitle: L("quit.unsaved.cancel", "Don't Quit"))
                    alert.addButton(withTitle: L("quit.unsaved.quit", "Quit Anyway"))
                }
                if alert.runModal() == .alertFirstButtonReturn {
                    quitting = false
                    apply(supervisor.state)
                    finishStop(false)
                    return
                }
            }
            // Progress only when stopping isn't instant.
            let root = self.root!
            let slow = Timer.scheduledTimer(withTimeInterval: 1, repeats: false) { _ in
                MainActor.assumeIsolated { root.showLoading(L("loading.stopping", "Saving and closing Refract…")) }
            }
            supervisor.stop { outcome in
                slow.invalidate()
                AppLog.note("the server stopped (\(outcome))")
                self.finishStop(true)
            }
        }
    }

    private func finishStop(_ proceed: Bool) {
        let waiting = pendingStops
        pendingStops = []
        waiting.forEach { $0(proceed) }
    }

    // MARK: Menu (a minimum: ⌘Q, ⌘W and the editing commands the web view needs; #68 replaces it)

    private func installMenu() {
        let main = NSMenu()
        func add(_ menu: NSMenu, _ title: String, _ action: Selector?, _ key: String = "", _ mods: NSEvent.ModifierFlags = .command) {
            let item = menu.addItem(withTitle: title, action: action, keyEquivalent: key)
            item.keyEquivalentModifierMask = mods
        }
        func submenu(_ title: String) -> NSMenu {
            let item = main.addItem(withTitle: title, action: nil, keyEquivalent: "")
            let menu = NSMenu(title: title)
            main.setSubmenu(menu, for: item)
            return menu
        }
        let app = submenu("Refract")
        add(app, L("menu.about", "About Refract"), #selector(NSApplication.orderFrontStandardAboutPanel(_:)))
        let check = app.addItem(withTitle: L("menu.checkForUpdates", "Check for Updates…"), action: #selector(UpdateController.checkForUpdates(_:)), keyEquivalent: "")
        check.target = updates
        app.addItem(.separator())
        add(app, L("menu.hide", "Hide Refract"), #selector(NSApplication.hide(_:)), "h")
        add(app, L("menu.hideOthers", "Hide Others"), #selector(NSApplication.hideOtherApplications(_:)), "h", [.command, .option])
        add(app, L("menu.showAll", "Show All"), #selector(NSApplication.unhideAllApplications(_:)))
        app.addItem(.separator())
        add(app, L("menu.quit", "Quit Refract"), #selector(NSApplication.terminate(_:)), "q")

        let edit = submenu(L("menu.edit", "Edit"))
        add(edit, L("menu.undo", "Undo"), Selector(("undo:")), "z")
        add(edit, L("menu.redo", "Redo"), Selector(("redo:")), "z", [.command, .shift])
        edit.addItem(.separator())
        add(edit, L("menu.cut", "Cut"), #selector(NSText.cut(_:)), "x")
        add(edit, L("menu.copy", "Copy"), #selector(NSText.copy(_:)), "c")
        add(edit, L("menu.paste", "Paste"), #selector(NSText.paste(_:)), "v")
        add(edit, L("menu.selectAll", "Select All"), #selector(NSText.selectAll(_:)), "a")

        let windowMenu = submenu(L("menu.window", "Window"))
        add(windowMenu, L("menu.minimize", "Minimize"), #selector(NSWindow.performMiniaturize(_:)), "m")
        add(windowMenu, L("menu.zoom", "Zoom"), #selector(NSWindow.performZoom(_:)))
        add(windowMenu, L("menu.close", "Close Window"), #selector(NSWindow.performClose(_:)), "w")
        NSApp.mainMenu = main
        NSApp.windowsMenu = windowMenu
    }
}
