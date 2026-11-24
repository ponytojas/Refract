import AppKit
import RefractCore
import Sparkle

/// Updates with Sparkle. The updater only starts when the build carries a public EdDSA key
/// (`UpdateSettings`): a development build says so instead of failing at the first check.
/// Sparkle verifies the archive's signature against that key before it extracts anything,
/// and replaces the whole bundle (the app and its tools are one update).
///
/// Before Sparkle relaunches the app for an update, the server stops through the same path
/// as ⌘Q (`stopServer`: refractFlush, then standard input closes, then wait), so pending
/// saves finish and the database is closed before the new version opens it.
final class UpdateController: NSObject, SPUUpdaterDelegate {
    let settings: UpdateSettings
    /// Runs the quit sequence's server part; calls back `true` when the app may go on
    /// (relaunch), `false` when the person chose to keep working.
    var stopServer: (_ forUpdate: Bool, _ done: @escaping (Bool) -> Void) -> Void = { $1(true) }
    /// Called when an update stopped the server and then didn't go on: start it again.
    var resumeServer: () -> Void = {}
    private var controller: SPUStandardUpdaterController?
    private var stoppedForUpdate = false
    /// Sparkle's relaunch, kept when the person chose "Don't Quit": Check for Updates… finishes it.
    private var waitingInstall: (() -> Void)?

    init(settings: UpdateSettings) {
        self.settings = settings
        super.init()
        guard case .enabled = settings else {
            AppLog.note("updates are off: \(settings)")
            return
        }
        // Automatic checks follow the person's answer to Sparkle's own question (asked on the
        // second launch because Info.plist doesn't set SUEnableAutomaticChecks), and
        // "Automatically check for updates" stays theirs to change.
        controller = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: self, userDriverDelegate: nil)
    }

    var isEnabled: Bool { controller != nil }

    @objc func checkForUpdates(_ sender: Any?) {
        if let install = waitingInstall {
            // An update is downloaded and waiting for the person's go-ahead: ask again.
            waitingInstall = nil
            relaunch(install)
        } else if let controller {
            controller.checkForUpdates(sender)
        } else {
            explainDisabled()
        }
    }

    private func explainDisabled() {
        let alert = NSAlert()
        alert.messageText = L("update.disabled.title", "Updates are turned off in this copy")
        if case .disabled(.insecureFeed) = settings {
            alert.informativeText = L("update.disabled.feed", "This copy's update address isn't https (or a local one for tests), so it can't be used. If you set REFRACT_APP_UPDATE_FEED, remove it or give an https address; otherwise download Refract again from the releases page.")
        } else if case .disabled(.noPublicKey) = settings {
            alert.informativeText = L("update.disabled.dev", "This copy of Refract was built for development and can't verify updates. Download the current version from the releases page.")
        } else {
            alert.informativeText = L("update.disabled.broken", "This copy of Refract has no valid update settings. Download it again from the releases page; your projects aren't touched.")
        }
        alert.addButton(withTitle: L("update.disabled.ok", "OK"))
        alert.addButton(withTitle: L("update.disabled.releases", "Open Releases Page"))
        if alert.runModal() == .alertSecondButtonReturn {
            NSWorkspace.shared.open(URL(string: "https://github.com/ponytojas/Refract/releases/latest")!)
        }
    }

    // MARK: SPUUpdaterDelegate

    func feedURLString(for updater: SPUUpdater) -> String? {
        if case .enabled(let url, _) = settings { return url.absoluteString }
        return nil
    }

    func updater(_ updater: SPUUpdater, shouldPostponeRelaunchForUpdate item: SUAppcastItem, untilInvokingBlock installHandler: @escaping () -> Void) -> Bool {
        AppLog.note("update \(item.displayVersionString) is ready: stopping the server before relaunching")
        relaunch(installHandler)
        return true
    }

    /// Stops the server, then lets Sparkle relaunch. If the person keeps working, the relaunch
    /// is kept for the next Check for Updates….
    private func relaunch(_ install: @escaping () -> Void) {
        stopServer(true) { [weak self] proceed in
            if proceed {
                self?.stoppedForUpdate = true
                install()
            } else {
                AppLog.note("update postponed: the person chose to keep working")
                self?.waitingInstall = install
            }
        }
    }

    func updaterWillRelaunchApplication(_ updater: SPUUpdater) {
        AppLog.note("relaunching to finish the update")
    }

    func updater(_ updater: SPUUpdater, failedToDownloadUpdate item: SUAppcastItem, error: Error) {
        AppLog.note("update \(item.displayVersionString) failed to download: \(error.localizedDescription)")
    }

    func updater(_ updater: SPUUpdater, didAbortWithError error: Error) {
        AppLog.note("update aborted: \(error.localizedDescription)")
        // If the server was stopped for an update that then didn't go on, the app stays usable.
        if stoppedForUpdate {
            stoppedForUpdate = false
            resumeServer()
        }
    }
}
