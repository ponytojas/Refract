import Foundation

/// Where the traffic lights go. The window has no title bar, so the lights are placed by the app
/// to share one horizontal line with the web app's bars (`--bar-h` in `index.css`: the bars'
/// content rows are centred `centreFromTop` points below the window's top edge, and start
/// `leading` points from the left edge, inside the card or the sidebar, never across its edge).
public struct TrafficLights: Equatable {
    public static let centreFromTop = 30.5
    public static let leading = 18.0
    /// Space between the zoom button and the first control of a bar.
    public static let gap = 12.0

    /// Height the title bar container needs so the lights fit at `centreFromTop`.
    public var containerHeight: Double
    /// Y of each button's origin inside the title bar view (AppKit coordinates: from the bottom).
    public var buttonY: Double
    /// X of each button's origin, close, minimize, zoom.
    public var buttonX: [Double]
    /// Right edge of the zoom button.
    public var maxX: Double

    /// `size`: the buttons' size; `step`: the distance between two buttons' origins (the system's).
    public static func place(buttonWidth: Double, buttonHeight: Double, step: Double) -> TrafficLights {
        let height = (centreFromTop * 2).rounded(.up)
        let xs = (0..<3).map { leading + Double($0) * step }
        return TrafficLights(
            containerHeight: height,
            buttonY: (height - centreFromTop - buttonHeight / 2).rounded(),
            buttonX: xs,
            maxX: xs[2] + buttonWidth)
    }
}

/// The space the web app leaves at the top left of the window for the traffic lights. Zero in
/// full screen, where the buttons hide. Pure numbers, so the rules are testable without AppKit.
public struct WindowInset: Equatable {
    public var left: Double
    public var top: Double

    public init(left: Double, top: Double) {
        self.left = left
        self.top = top
    }

    /// `lights`: where the buttons are (nil: a window without them reserves nothing at the left).
    public static func compute(lights: TrafficLights?, fullScreen: Bool) -> WindowInset {
        if fullScreen { return WindowInset(left: 0, top: 0) }
        return WindowInset(left: lights.map { ($0.maxX + TrafficLights.gap).rounded() } ?? 0, top: 52)
    }

    /// JavaScript that marks the page as the desktop app and sets the two CSS variables.
    /// Only numbers go in, so nothing of the page's can reach the script.
    public var script: String {
        """
        (function(){var r=document.documentElement;r.dataset.desktop="mac";\
        r.style.setProperty("--window-inset-left","\(Int(left))px");\
        r.style.setProperty("--window-inset-top","\(Int(top))px");})();
        """
    }
}

/// What the web app asks of the window through the `refractWindow` script message handler.
public enum WindowMessage: Equatable {
    case drag

    /// Only `{type: "drag"}` from the main frame of the app's own origin counts; anything else
    /// (another frame, another origin, another shape) is dropped.
    public static func parse(_ body: Any, isMainFrame: Bool, scheme: String?, host: String?, port: Int?, origin: URL) -> WindowMessage? {
        guard isMainFrame,
            scheme?.lowercased() == origin.scheme?.lowercased(),
            host?.lowercased() == origin.host?.lowercased(),
            port == origin.port,
            let dict = body as? [String: Any], dict.count == 1,
            dict["type"] as? String == "drag"
        else { return nil }
        return .drag
    }
}

/// What a double click on the draggable part of the window does: the system setting
/// `AppleActionOnDoubleClick`.
public enum DoubleClickAction: Equatable {
    case zoom
    case minimize
    case nothing

    public init(setting: String?) {
        switch setting?.lowercased() {
        case "minimize": self = .minimize
        case "none": self = .nothing
        default: self = .zoom  // "Maximize" and the unset default
        }
    }
}
