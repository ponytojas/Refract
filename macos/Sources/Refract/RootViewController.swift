import AppKit
import RefractCore

/// The window's content: the loading view, the error view or the web app, one at a time.
final class RootViewController: NSViewController {
    private let restart: () -> Void
    private let showLogs: () -> Void
    private let reinstall: () -> Void
    private var web: WebViewController?
    private var current: NSView?
    private var inset = WindowInset(left: 0, top: 0)

    init(restart: @escaping () -> Void, showLogs: @escaping () -> Void, reinstall: @escaping () -> Void) {
        self.restart = restart
        self.showLogs = showLogs
        self.reinstall = reinstall
        super.init(nibName: nil, bundle: nil)
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    override func loadView() { view = NSView() }

    /// The window has no title bar: the web app reaches the top edge and lays itself out around
    /// the traffic lights; the native views sit below them (the safe area).
    private func show(_ next: NSView, underTitlebar: Bool = false) {
        current?.removeFromSuperview()
        next.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(next)
        NSLayoutConstraint.activate([
            next.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            next.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            next.topAnchor.constraint(equalTo: underTitlebar ? view.topAnchor : view.safeAreaLayoutGuide.topAnchor),
            next.bottomAnchor.constraint(equalTo: view.bottomAnchor),
        ])
        current = next
    }

    /// Places the traffic lights on the bars' line and tells the page how much room they take
    /// (called on launch and whenever AppKit may have laid the title bar out again).
    func updateInset(for window: NSWindow) {
        let fullScreen = window.styleMask.contains(.fullScreen)
        var lights: TrafficLights?
        if !fullScreen { lights = placeTrafficLights(in: window) }
        inset = WindowInset.compute(lights: lights, fullScreen: fullScreen)
        web?.update(inset: inset)
    }

    /// Moves the three buttons, and grows the title bar container so they fit lower than its
    /// usual 28 points. AppKit resets both on resize, full screen and appearance changes: the
    /// app calls this again then (the frame is only written when it differs).
    private func placeTrafficLights(in window: NSWindow) -> TrafficLights? {
        guard let close = window.standardWindowButton(.closeButton),
            let mini = window.standardWindowButton(.miniaturizeButton),
            let zoom = window.standardWindowButton(.zoomButton),
            let bar = close.superview, let container = bar.superview
        else { return nil }
        let lights = TrafficLights.place(
            buttonWidth: Double(close.frame.width), buttonHeight: Double(close.frame.height),
            step: Double(mini.frame.minX - close.frame.minX))
        if let host = container.superview, abs(container.frame.height - lights.containerHeight) > 0.5 {
            container.frame = NSRect(
                x: container.frame.minX, y: host.bounds.height - lights.containerHeight,
                width: container.frame.width, height: lights.containerHeight)
        }
        for (button, x) in zip([close, mini, zoom], lights.buttonX) {
            let origin = NSPoint(x: x, y: lights.buttonY)
            if button.frame.origin != origin { button.setFrameOrigin(origin) }
        }
        container.postsFrameChangedNotifications = true
        return lights
    }

    private func dropWeb() {
        web?.removeFromParent()
        web = nil
    }

    func showLoading(_ message: String) {
        dropWeb()
        show(LoadingView(message: message))
    }

    func showError(_ failure: ServerFailure) {
        dropWeb()
        show(ErrorView(content: ErrorContent(failure), restart: restart, showLogs: showLogs, reinstall: reinstall))
    }

    func showWeb(url: URL, token: String?) {
        if web != nil, current === web?.view { return }
        let controller = WebViewController(origin: url, inset: inset) { [weak self] reason in
            self?.showError(.startFailed(reason))
        }
        web = controller
        addChild(controller)
        show(controller.view, underTitlebar: true)
        controller.load(token: token)
    }

    /// Saves what the page still holds. nil: there is no page to ask.
    func flushPage() async -> Bool? { await web?.flush() }
}

// MARK: Views

final class LoadingView: NSView {
    init(message: String) {
        super.init(frame: .zero)
        let spinner = NSProgressIndicator()
        spinner.style = .spinning
        spinner.isIndeterminate = true
        spinner.startAnimation(nil)
        let label = NSTextField(labelWithString: message)
        label.font = .systemFont(ofSize: 15)
        label.textColor = .secondaryLabelColor
        let stack = NSStackView(views: [spinner, label])
        stack.orientation = .vertical
        stack.spacing = 14
        stack.alignment = .centerX
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        setAccessibilityLabel(message)
    }

    required init?(coder: NSCoder) { fatalError("not used") }
}

/// What the error view says for each way the server can fail.
struct ErrorContent {
    var title: String
    var message: String
    var details: String
    var canReinstall = false

    init(_ failure: ServerFailure) {
        switch failure {
        case .installDamaged(let names):
            title = L("error.damaged.title", "Refract's files are missing or damaged")
            message = String(format: L("error.damaged.message", "The app can't find what it needs to run (%@). Download Refract again and replace this copy; your projects aren't touched."), names.joined(separator: ", "))
            details = ""
            canReinstall = true
        case .startFailed(let text):
            title = L("error.start.title", "Refract couldn't start")
            message = L("error.start.message", "Your projects and files are untouched. Try again; if it keeps failing, the log says why.")
            details = text
        case .unresponsive(let pid):
            title = L("error.unresponsive.title", "Another Refract isn't answering")
            message = String(format: L("error.unresponsive.message", "Another copy of Refract holds your projects but doesn't answer (process %@). Wait a moment and try again, or quit that process."), pid.map(String.init) ?? "?")
            details = ""
        case .anotherAppInstance:
            title = L("error.other.title", "Refract is already open")
            message = L("error.other.message", "Another copy of the app is using your projects. Quit it, then try again.")
            details = ""
        case .exited(let status, let signaled, let tail):
            title = L("error.exited.title", "Refract stopped unexpectedly")
            message = String(format: signaled
                ? L("error.exited.signal", "The server was stopped by signal %d. Your projects and files are safe; what you typed in the last moment may not be.")
                : L("error.exited.status", "The server exited with status %d. Your projects and files are safe; what you typed in the last moment may not be."), Int(status))
            details = tail
        }
    }
}

final class ErrorView: NSView {
    private let restart: () -> Void
    private let showLogs: () -> Void
    private let reinstall: () -> Void

    init(content: ErrorContent, restart: @escaping () -> Void, showLogs: @escaping () -> Void, reinstall: @escaping () -> Void) {
        self.restart = restart
        self.showLogs = showLogs
        self.reinstall = reinstall
        super.init(frame: .zero)

        let title = NSTextField(wrappingLabelWithString: content.title)
        title.font = .systemFont(ofSize: 22, weight: .semibold)
        title.isSelectable = false
        let message = NSTextField(wrappingLabelWithString: content.message)
        message.font = .systemFont(ofSize: 14)
        message.textColor = .secondaryLabelColor

        var views: [NSView] = [title, message]
        if !content.details.isEmpty {
            let text = NSTextView()
            text.isEditable = false
            text.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
            text.string = content.details
            text.textContainerInset = NSSize(width: 6, height: 6)
            let scroll = NSScrollView()
            scroll.documentView = text
            scroll.hasVerticalScroller = true
            scroll.borderType = .lineBorder
            scroll.heightAnchor.constraint(equalToConstant: 180).isActive = true
            text.autoresizingMask = [.width]
            views.append(scroll)
            // The end of the log is what says why.
            DispatchQueue.main.async { text.scrollToEndOfDocument(nil) }
            scroll.setAccessibilityLabel(L("error.log.label", "Last lines of the log"))
        }

        let restartButton = NSButton(title: L("error.restart", "Restart"), target: self, action: #selector(didRestart))
        restartButton.keyEquivalent = "\r"
        let logsButton = NSButton(title: L("error.showLogs", "Show Logs"), target: self, action: #selector(didShowLogs))
        var buttons: [NSView] = [restartButton, logsButton]
        if content.canReinstall {
            buttons.insert(NSButton(title: L("error.reinstall", "Download Refract Again"), target: self, action: #selector(didReinstall)), at: 0)
        }
        let row = NSStackView(views: buttons)
        row.spacing = 10
        views.append(row)

        let stack = NSStackView(views: views)
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 14
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            stack.widthAnchor.constraint(lessThanOrEqualToConstant: 560),
            stack.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor, constant: 32),
            stack.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -32),
        ])
        let width = stack.widthAnchor.constraint(equalToConstant: 560)
        width.priority = .defaultHigh
        width.isActive = true
        for view in views where view is NSScrollView || view is NSTextField {
            view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        }
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    @objc private func didRestart() { restart() }
    @objc private func didShowLogs() { showLogs() }
    @objc private func didReinstall() { reinstall() }
}
