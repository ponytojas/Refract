import XCTest

@testable import RefractCore

final class WindowChromeTests: XCTestCase {
    func testLightsShareTheBarsCentre() {
        let l = TrafficLights.place(buttonWidth: 14, buttonHeight: 16, step: 20)
        XCTAssertEqual(l.containerHeight, 61)
        // The button's vertical centre, measured from the top of the window.
        XCTAssertEqual(l.containerHeight - (l.buttonY + 8), TrafficLights.centreFromTop, accuracy: 0.5)
        XCTAssertEqual(l.buttonX, [18, 38, 58])
        XCTAssertEqual(l.maxX, 72)
        // Left edge: inside the card (which starts at 9 points), not across it.
        XCTAssertGreaterThan(l.buttonX[0], 9)
    }

    func testInsetClearsTheButtonsByTheGap() {
        let l = TrafficLights.place(buttonWidth: 14, buttonHeight: 16, step: 20)
        XCTAssertEqual(WindowInset.compute(lights: l, fullScreen: false), WindowInset(left: 84, top: 52))
    }

    func testFullScreenReservesNothing() {
        let l = TrafficLights.place(buttonWidth: 14, buttonHeight: 16, step: 20)
        XCTAssertEqual(WindowInset.compute(lights: l, fullScreen: true), WindowInset(left: 0, top: 0))
    }

    func testNoButtonsNoLeftInset() {
        XCTAssertEqual(WindowInset.compute(lights: nil, fullScreen: false).left, 0)
    }

    func testScriptCarriesOnlyNumbers() {
        let js = WindowInset(left: 78, top: 28).script
        XCTAssertTrue(js.contains("dataset.desktop=\"mac\""))
        XCTAssertTrue(js.contains("--window-inset-left\",\"78px\""))
        XCTAssertTrue(js.contains("--window-inset-top\",\"28px\""))
    }

    let origin = URL(string: "http://127.0.0.1:51234")!

    func parse(_ body: Any, main: Bool = true, host: String? = "127.0.0.1", port: Int? = 51234, scheme: String? = "http") -> WindowMessage? {
        WindowMessage.parse(body, isMainFrame: main, scheme: scheme, host: host, port: port, origin: origin)
    }

    func testDragFromTheAppIsAccepted() {
        XCTAssertEqual(parse(["type": "drag"]), .drag)
        XCTAssertEqual(parse(["type": "drag"], host: "127.0.0.1".uppercased()), .drag)
    }

    func testOtherSendersAndShapesAreDropped() {
        XCTAssertNil(parse(["type": "drag"], main: false))
        XCTAssertNil(parse(["type": "drag"], host: "evil.example"))
        XCTAssertNil(parse(["type": "drag"], port: 51235))
        XCTAssertNil(parse(["type": "drag"], scheme: "https"))
        XCTAssertNil(parse(["type": "close"]))
        XCTAssertNil(parse(["type": "drag", "x": 1]))
        XCTAssertNil(parse("drag"))
    }

    func testDoubleClickSetting() {
        XCTAssertEqual(DoubleClickAction(setting: "Maximize"), .zoom)
        XCTAssertEqual(DoubleClickAction(setting: nil), .zoom)
        XCTAssertEqual(DoubleClickAction(setting: "Minimize"), .minimize)
        XCTAssertEqual(DoubleClickAction(setting: "None"), .nothing)
    }
}
