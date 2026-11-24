import Foundation

/// What the app does with a URL the web view wants to open. Pure, so it is tested.
public enum LinkDecision: Equatable {
    /// The server's own origin (and about:blank/srcdoc, data: and our own blob: pages): stays in the app.
    case ours
    /// http, https or mailto elsewhere: the default browser (or mail app).
    case external
    /// Any other scheme (file:, javascript:, custom app schemes, another origin's blob:): never opened.
    case refused
}

public enum LinkPolicy {
    public static let externalSchemes: Set<String> = ["http", "https", "mailto"]

    public static func classify(_ url: URL?, origin: URL) -> LinkDecision {
        guard let url, let scheme = url.scheme?.lowercased() else { return .refused }
        switch scheme {
        case "about":
            let page = url.absoluteString.dropFirst("about:".count).prefix(while: { $0 != "?" && $0 != "#" })
            return page == "blank" || page == "srcdoc" ? .ours : .refused
        case "data": return .ours
        case "blob":
            // blob:<origin>/<uuid>: only blobs our pages made.
            let inner = URL(string: String(url.absoluteString.dropFirst("blob:".count)))
            return inner.map { sameOrigin($0, origin) } == true ? .ours : .refused
        default: break
        }
        if sameOrigin(url, origin) { return .ours }
        return externalSchemes.contains(scheme) ? .external : .refused
    }

    private static func sameOrigin(_ url: URL, _ origin: URL) -> Bool {
        guard let scheme = url.scheme?.lowercased(), scheme == origin.scheme?.lowercased(),
            let host = url.host?.lowercased(), host == origin.host?.lowercased()
        else { return false }
        return effectivePort(url) == effectivePort(origin)
    }

    private static func effectivePort(_ url: URL) -> Int? {
        if let port = url.port { return port }
        switch url.scheme?.lowercased() {
        case "http": return 80
        case "https": return 443
        default: return nil
        }
    }
}

public enum DownloadName {
    /// The longest name APFS and HFS+ take, in UTF-8 bytes.
    static let maxBytes = 255

    /// A file name that is safe to offer in a save panel: no path parts, no control or format
    /// characters (an RTL override can disguise "fdp.exe" as "exe.pdf"), never empty or hidden,
    /// at most 255 bytes with its extension kept. The server's names come from project names.
    public static func sanitize(_ suggested: String, fallback: String = "download") -> String {
        // Separators become "-" so "Thesis: Draft 2.zip" keeps its whole name.
        let last = String(suggested.map { $0 == "/" || $0 == "\\" || $0 == ":" ? "-" : $0 })
        // controlCharacters is Cc and Cf, so bidi overrides and zero-width characters go too.
        let cleaned = String(last.unicodeScalars.filter { !CharacterSet.controlCharacters.contains($0) })
            .trimmingCharacters(in: .whitespaces)
        let name = String(cleaned.drop(while: { $0 == "." || $0.isWhitespace }))
        if name.isEmpty { return fallback }
        return truncate(name)
    }

    private static func truncate(_ name: String) -> String {
        if name.utf8.count <= maxBytes { return name }
        let ext = (name as NSString).pathExtension
        let suffix = ext.isEmpty || ext.utf8.count > 16 ? "" : "." + ext
        var stem = Substring(suffix.isEmpty ? name : String(name.dropLast(suffix.count)))
        // Drop whole characters so no multibyte sequence is cut in half.
        while stem.utf8.count + suffix.utf8.count > maxBytes { stem = stem.dropLast() }
        return String(stem) + suffix
    }
}
