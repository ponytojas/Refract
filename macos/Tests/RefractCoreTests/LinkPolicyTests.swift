import XCTest

@testable import RefractCore

final class LinkPolicyTests: XCTestCase {
    let origin = URL(string: "http://127.0.0.1:51234")!

    func classify(_ s: String) -> LinkDecision { LinkPolicy.classify(URL(string: s), origin: origin) }

    func testOurOriginStays() {
        XCTAssertEqual(classify("http://127.0.0.1:51234/projects/1"), .ours)
        XCTAssertEqual(classify("blob:http://127.0.0.1:51234/abc"), .ours)
        XCTAssertEqual(classify("about:blank"), .ours)
        XCTAssertEqual(classify("about:srcdoc"), .ours)
        XCTAssertEqual(classify("HTTP://127.0.0.1:51234/x"), .ours)
        XCTAssertEqual(classify("data:text/plain,hi"), .ours)
        // userinfo is not the host
        XCTAssertEqual(classify("http://evil@127.0.0.1:51234/"), .ours)
    }

    func testLookalikesAreNotOurs() {
        XCTAssertEqual(classify("http://127.0.0.1:51234@evil.example/"), .external)
        XCTAssertEqual(classify("http://127.0.0.1.evil.example:51234/"), .external)
        XCTAssertEqual(classify("http://[::1]:51234/"), .external)
        XCTAssertEqual(classify("http://127.0.0.1/"), .external)
        XCTAssertEqual(classify("blob:https://evil.example/abc"), .refused)
        XCTAssertEqual(classify("blob:http://127.0.0.1:51235/abc"), .refused)
        XCTAssertEqual(classify("blob:null/abc"), .refused)
        XCTAssertEqual(classify("about:config"), .refused)
    }

    func testOtherPortOrHostIsExternal() {
        XCTAssertEqual(classify("http://127.0.0.1:51235/"), .external)
        XCTAssertEqual(classify("http://localhost:51234/"), .external)
        XCTAssertEqual(classify("https://127.0.0.1:51234/"), .external)
        XCTAssertEqual(classify("https://example.com/help"), .external)
        XCTAssertEqual(classify("mailto:a@b.c"), .external)
    }

    func testOtherSchemesAreRefused() {
        XCTAssertEqual(classify("file:///etc/passwd"), .refused)
        XCTAssertEqual(classify("javascript:alert(1)"), .refused)
        XCTAssertEqual(classify("x-apple.systempreferences:"), .refused)
        XCTAssertEqual(classify("ssh://host"), .refused)
        XCTAssertEqual(LinkPolicy.classify(nil, origin: origin), .refused)
    }

    func testDownloadNames() {
        XCTAssertEqual(DownloadName.sanitize("My thesis.zip"), "My thesis.zip")
        XCTAssertEqual(DownloadName.sanitize("../../etc/passwd"), "-..-etc-passwd")
        XCTAssertEqual(DownloadName.sanitize("a:b\\c.pdf"), "a-b-c.pdf")
        XCTAssertEqual(DownloadName.sanitize("Thesis: Draft 2.zip"), "Thesis- Draft 2.zip")
        XCTAssertEqual(DownloadName.sanitize(""), "download")
        XCTAssertEqual(DownloadName.sanitize("..."), "download")
        XCTAssertEqual(DownloadName.sanitize(".hidden"), "hidden")
        XCTAssertEqual(DownloadName.sanitize("a\u{0}b.pdf"), "ab.pdf")
        XCTAssertEqual(DownloadName.sanitize("x", fallback: "f"), "x")
        XCTAssertEqual(DownloadName.sanitize("report\u{202E}fdp.exe"), "reportfdp.exe")
        XCTAssertEqual(DownloadName.sanitize("a\u{200B}b.zip"), "ab.zip")
        XCTAssertEqual(DownloadName.sanitize(". .zip"), "zip")
        let long = DownloadName.sanitize(String(repeating: "é", count: 300) + ".zip")
        XCTAssertLessThanOrEqual(long.utf8.count, 255)
        XCTAssertTrue(long.hasSuffix("é.zip"))
        XCTAssertEqual(DownloadName.sanitize(String(repeating: "a", count: 300)).utf8.count, 255)
    }
}
