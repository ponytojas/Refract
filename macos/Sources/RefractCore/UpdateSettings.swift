import Foundation

/// Whether this build can update itself, decided from what the build baked into Info.plist
/// (the per-architecture appcast URL, `SUFeedURL`, and the public half of the EdDSA key that
/// signs the updates, `SUPublicEDKey`) and, for tests, an environment override of the feed.
/// Sparkle itself refuses an unsigned or wrongly signed update; this is the earlier, plainer
/// check that keeps a development build (no key) from starting an updater that can't work.
public enum UpdateSettings: Equatable {
    case enabled(feedURL: URL, publicKey: String)
    case disabled(DisabledReason)

    public enum DisabledReason: Equatable {
        /// Ad hoc development builds have no key: nothing they downloaded could be verified.
        case noPublicKey
        /// The key isn't base64 of the 32 bytes of an Ed25519 public key.
        case invalidPublicKey
        case noFeed
        /// Only https feeds (or a loopback one for tests) are used.
        case insecureFeed(String)
    }

    /// `REFRACT_APP_UPDATE_FEED` points the updater at another appcast, for tests of the
    /// update path. It cannot weaken the archive check (the archive still has to carry a valid
    /// signature of the baked-in key), and it is honoured only by builds whose Info.plist has
    /// `feedOverrideKey`, which `build-app.sh` adds to ad hoc builds only (`verify-app.sh
    /// --release` refuses it): a release always reads its own https feed.
    public static let feedOverrideVariable = "REFRACT_APP_UPDATE_FEED"
    public static let feedOverrideKey = "RefractAllowsUpdateFeedOverride"

    public static func resolve(info: [String: Any], environment: [String: String] = [:]) -> UpdateSettings {
        let key = (info["SUPublicEDKey"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !key.isEmpty else { return .disabled(.noPublicKey) }
        guard let raw = Data(base64Encoded: key), raw.count == 32 else { return .disabled(.invalidPublicKey) }
        let overridable = info[feedOverrideKey] as? Bool ?? false
        let override = overridable ? environment[feedOverrideVariable]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "" : ""
        let feed = override.isEmpty
            ? ((info["SUFeedURL"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "")
            : override
        guard !feed.isEmpty, let url = URL(string: feed), url.scheme != nil else { return .disabled(.noFeed) }
        guard isAcceptableFeed(url) else { return .disabled(.insecureFeed(feed)) }
        return .enabled(feedURL: url, publicKey: key)
    }

    static func isAcceptableFeed(_ url: URL) -> Bool {
        if url.scheme?.lowercased() == "https" { return url.host != nil }
        guard url.scheme?.lowercased() == "http", let host = url.host?.lowercased() else { return false }
        return ["127.0.0.1", "localhost", "::1"].contains(host)
    }
}
