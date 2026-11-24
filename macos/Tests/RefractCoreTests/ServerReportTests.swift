import XCTest
@testable import RefractCore

final class ServerReportTests: XCTestCase {
    func testReady() throws {
        let line = #"{"state":"ready","url":"http://127.0.0.1:51234","pid":1234,"version":"v0.3.0","dataDir":"/d","lockFile":"/d/lock"}"#
        guard case .ready(let info) = try parseServerReport(line) else { return XCTFail("not ready") }
        XCTAssertEqual(info.url, URL(string: "http://127.0.0.1:51234"))
        XCTAssertEqual(info.pid, 1234)
        XCTAssertEqual(info.version, "v0.3.0")
        XCTAssertFalse(info.embedded)
    }

    func testRunningFromTerminalAndFromAnotherApp() throws {
        guard case .running(let term) = try parseServerReport(#"{"state":"running","url":"http://127.0.0.1:1","pid":2}"#),
            case .running(let app) = try parseServerReport(#"{"state":"running","url":"http://127.0.0.1:1","pid":2,"embedded":true}"#)
        else { return XCTFail("not running") }
        XCTAssertFalse(term.embedded)
        XCTAssertTrue(app.embedded)
    }

    func testUnresponsiveAndError() throws {
        XCTAssertEqual(try parseServerReport(#"{"state":"error","error":"no key"}"#), .error("no key"))
        guard case .unresponsive(let info) = try parseServerReport(#"{"state":"unresponsive","pid":7}"#) else { return XCTFail() }
        XCTAssertEqual(info.pid, 7)
    }

    func testStartingInstanceIsUnresponsive() throws {
        guard case .unresponsive(let info) = try parseServerReport(#"{"state":"starting","pid":8}"#) else { return XCTFail() }
        XCTAssertEqual(info.pid, 8)
        XCTAssertNil(info.url)
    }

    func testRejectsWhatIsNotAReport() {
        XCTAssertThrowsError(try parseServerReport("2026/01/01 log line"))
        XCTAssertThrowsError(try parseServerReport(#"{"state":"weird"}"#)) { XCTAssertEqual($0 as? ServerReportError, .unknownState("weird")) }
        XCTAssertThrowsError(try parseServerReport(#"{"state":"ready"}"#)) { XCTAssertEqual($0 as? ServerReportError, .missingURL("ready")) }
    }

    func testTokenIsLongBase64URL() {
        let a = makeLaunchToken(), b = makeLaunchToken()
        XCTAssertNotEqual(a, b)
        XCTAssertGreaterThanOrEqual(a.count, 16)
        XCTAssertNil(a.rangeOfCharacter(from: CharacterSet(charactersIn: "+/=, ;\"\\")))
    }

    func testServerEnvironmentDropsWhatChangesTheMode() {
        let env = ServerSupervisor.serverEnvironment(from: [
            "HOME": "/h", "PATH": "/usr/bin", "REFRACT_DATA_DIR": "/x", "REFRACT_AUTH": "none", "REFRACT_EMBED_TOKEN": "t", "REFRACT_TECTONIC_BIN": "/t",
            "REFRACT_APP_LOG_DIR": "/l", "REFRACT_APP_DEFAULTS_SUITE": "s",
            "REFRACT_SECRET_KEY_FILE": "/x/key",
        ])
        XCTAssertEqual(env, ["HOME": "/h", "PATH": "/usr/bin", "REFRACT_TECTONIC_BIN": "/t"])
    }
}

final class LogStoreTests: XCTestCase {
    func testRotatesBigLogsAndKeepsTwo() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("refract-log-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let store = LogStore(directory: dir, maxBytes: 10, keep: 2)
        for n in 1...4 {
            let fd = try store.open()
            store.note("run \(n) with enough text to pass the limit", fd: fd)
            close(fd)
        }
        let names = try FileManager.default.contentsOfDirectory(atPath: dir.path).sorted()
        XCTAssertEqual(names, ["refract.log", "refract.log.1", "refract.log.2"])
        XCTAssertTrue(store.tail().contains("run 4"))
        XCTAssertFalse(try String(contentsOf: dir.appendingPathComponent("refract.log.2")).contains("run 1"))
    }

    func testTailKeepsTheLastLines() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("refract-log-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let store = LogStore(directory: dir)
        let fd = try store.open()
        for n in 1...30 { store.note("line \(n)", fd: fd) }
        close(fd)
        let lines = store.tail(lines: 3).split(separator: "\n")
        XCTAssertEqual(lines.count, 3)
        XCTAssertTrue(lines.last!.hasSuffix("line 30"))
    }

    func testTailOfTheCurrentRunStartsAtTheLastStart() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("refract-log-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let store = LogStore(directory: dir)
        let fd = try store.open()
        store.note("starting refract (first)", fd: fd)
        store.note("old failure", fd: fd)
        store.note("starting refract (second)", fd: fd)
        store.note("new failure", fd: fd)
        close(fd)
        let tail = store.tail(currentRun: true)
        XCTAssertTrue(tail.contains("second") && tail.contains("new failure"))
        XCTAssertFalse(tail.contains("old failure"))
        XCTAssertTrue(store.tail().contains("old failure"))
    }
}

final class BundleLayoutTests: XCTestCase {
    func testReportsWhatIsMissing() throws {
        let contents = FileManager.default.temporaryDirectory.appendingPathComponent("refract-app-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: contents) }
        let layout = BundleLayout(contents: contents)
        XCTAssertEqual(layout.missing(), ["refract", "tectonic", "synctex", "rg", "git", "git-core"])

        func make(_ rel: String, executable: Bool = true) throws {
            let url = contents.appendingPathComponent(rel)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            FileManager.default.createFile(atPath: url.path, contents: Data("#!/bin/sh\n".utf8), attributes: [.posixPermissions: executable ? 0o755 : 0o644])
        }
        try make("Resources/Server/bin/refract")
        for t in BundleLayout.requiredTools { try make("Resources/Server/libexec/refract/\(t)") }
        try FileManager.default.createDirectory(at: contents.appendingPathComponent("Resources/Server/libexec/refract/git/libexec/git-core"), withIntermediateDirectories: true)
        XCTAssertEqual(layout.missing(), [])

        try make("Resources/Server/libexec/refract/rg", executable: false)  // damaged: not executable
        XCTAssertEqual(layout.missing(), ["rg"])
    }
}
