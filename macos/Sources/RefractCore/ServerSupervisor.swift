import Foundation

public enum ServerFailure: Equatable {
    /// The server or a bundled tool is missing, not executable or reported missing by the server.
    case installDamaged([String])
    /// The server could not start: its message, or the end of its log.
    case startFailed(String)
    /// The lock is held but nothing answers: another Refract is starting or stuck.
    case unresponsive(pid: Int?)
    /// Another launch of this app owns the data folder; its token is unknown to this one.
    case anotherAppInstance
    /// The server exited after it was ready.
    case exited(status: Int32, signaled: Bool, logTail: String)
}

public enum ServerState: Equatable {
    case idle
    case starting
    /// `ownsServer` is false when a `refract local` from a terminal answers at `url`:
    /// the app loads it without a token and has nothing to stop.
    case ready(url: URL, ownsServer: Bool)
    case stopping
    case stopped
    case failed(ServerFailure)
}

public enum StopOutcome: Equatable {
    case notRunning
    case graceful
    /// The server didn't stop within the allowed time and was killed.
    case killed
}

/// Runs `refract local --embedded` as a child process and follows its life: the token on
/// a pipe, the JSON line on standard output, the log on standard error, standard input
/// closed to stop it. Everything runs on the main queue.
public final class ServerSupervisor {
    public struct Config {
        public var executable: URL
        public var arguments = ["local", "--embedded", "--token-fd", "3"]
        public var environment: [String: String]
        public var log: LogStore
        public var workingDirectory = NSHomeDirectory()
        /// Checked before every start; nil skips the check.
        public var layout: BundleLayout?
        /// Longer than the server's own wait for another refract local that is still
        /// starting (8 + 60 seconds), so that its report, naming that process, comes first.
        public var readyTimeout: TimeInterval = 75
        /// The server stops itself within about 25 seconds; this is when it is killed.
        public var stopTimeout: TimeInterval = 30

        public init(executable: URL, environment: [String: String], log: LogStore, layout: BundleLayout? = nil) {
            self.executable = executable
            self.environment = environment
            self.log = log
            self.layout = layout
        }
    }

    public var config: Config
    public private(set) var state: ServerState = .idle { didSet { if state != oldValue { onChange?(state) } } }
    /// The token of the current launch: the app sets it as the `refract_embed` cookie.
    public private(set) var token = ""
    /// A port to try first, so the web app's origin (and what the browser stores for it)
    /// stays the same between launches. If it can't be used the server picks a free one.
    public var preferredPort: Int?
    public var onChange: ((ServerState) -> Void)?

    private final class Run {
        var pid: pid_t = 0
        var stdin: Int32 = -1
        var stdout: Int32 = -1
        var source: DispatchSourceRead?
        var buffer = Data()
        var gotLine = false
        var report: ServerReport?
        var timeout: DispatchWorkItem?
        var killTimer: DispatchWorkItem?
        var port: Int?
        var stopRequested = false
        var killed = false
        var superseded = false
        var exited = false
        var stopCompletions: [(StopOutcome) -> Void] = []
    }

    private var run: Run?

    public init(config: Config) { self.config = config }

    // MARK: Start

    public func start() {
        switch state {
        case .starting, .stopping: return
        case .ready(_, let owns) where owns: return
        default: break
        }
        if let layout = config.layout {
            let missing = layout.missing()
            if !missing.isEmpty {
                state = .failed(.installDamaged(missing))
                return
            }
        }
        state = .starting
        attempt(port: preferredPort)
    }

    private func attempt(port: Int?) {
        let run = Run()
        run.port = port
        do {
            try spawn(run, port: port)
        } catch {
            state = .failed(.startFailed(error.localizedDescription))
            return
        }
        self.run = run
        let item = DispatchWorkItem { [weak self, weak run] in
            guard let self, let run, !run.gotLine, !run.exited else { return }
            run.superseded = true
            self.kill(run)
            self.state = .failed(.startFailed(String(format: L("error.timeout", "Refract did not answer within %d seconds."), Int(self.config.readyTimeout))))
        }
        run.timeout = item
        DispatchQueue.main.asyncAfter(deadline: .now() + config.readyTimeout, execute: item)
    }

    private static func cloexecPipe() throws -> (read: Int32, write: Int32) {
        var p: [Int32] = [-1, -1]
        guard pipe(&p) == 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
        // Above the descriptors the child gets (0 to 3) and not inherited by anything else.
        let r = fcntl(p[0], F_DUPFD_CLOEXEC, 10)
        let w = fcntl(p[1], F_DUPFD_CLOEXEC, 10)
        let saved = errno
        close(p[0]); close(p[1])
        guard r >= 0, w >= 0 else {
            if r >= 0 { close(r) }
            if w >= 0 { close(w) }
            throw POSIXError(POSIXErrorCode(rawValue: saved) ?? .EIO)
        }
        return (r, w)
    }

    private func spawn(_ run: Run, port: Int?) throws {
        let log = config.log
        let logFD = try log.open()
        var toClose: [Int32] = [logFD]
        defer { toClose.forEach { close($0) } }

        let tokenPipe = try Self.cloexecPipe()
        toClose.append(tokenPipe.read)
        let stdinPipe = try Self.cloexecPipe()
        toClose.append(stdinPipe.read)
        let stdoutPipe = try Self.cloexecPipe()
        toClose.append(stdoutPipe.write)
        var keep: [Int32] = [tokenPipe.write, stdinPipe.write, stdoutPipe.read]
        defer { keep.forEach { close($0) } }

        let token = makeLaunchToken()
        // The pipe holds it; the server reads until end of file.
        let written = token.withCString { Darwin.write(tokenPipe.write, $0, strlen($0)) }
        guard written == token.utf8.count else { throw POSIXError(.EIO) }
        close(tokenPipe.write); keep.removeAll { $0 == tokenPipe.write }

        var args = [config.executable.path] + config.arguments
        if let port { args += ["--port", String(port)] }
        log.note("starting \(config.executable.path) \(config.arguments.joined(separator: " "))" + (port.map { " --port \($0)" } ?? ""), fd: logFD)

        var actions: posix_spawn_file_actions_t? = nil
        posix_spawn_file_actions_init(&actions)
        defer { posix_spawn_file_actions_destroy(&actions) }
        posix_spawn_file_actions_adddup2(&actions, stdinPipe.read, 0)
        posix_spawn_file_actions_adddup2(&actions, stdoutPipe.write, 1)
        posix_spawn_file_actions_adddup2(&actions, logFD, 2)
        posix_spawn_file_actions_adddup2(&actions, tokenPipe.read, 3)
        posix_spawn_file_actions_addchdir_np(&actions, config.workingDirectory)

        var attr: posix_spawnattr_t? = nil
        posix_spawnattr_init(&attr)
        defer { posix_spawnattr_destroy(&attr) }
        // Its own process group, so that stopping it can also reach what it started;
        // and nothing inherited from the app except the four descriptors above.
        var all = sigset_t(), none = sigset_t()
        sigfillset(&all); sigemptyset(&none)
        posix_spawnattr_setsigdefault(&attr, &all)
        posix_spawnattr_setsigmask(&attr, &none)
        posix_spawnattr_setpgroup(&attr, 0)
        posix_spawnattr_setflags(&attr, Int16(POSIX_SPAWN_SETPGROUP | POSIX_SPAWN_SETSIGDEF | POSIX_SPAWN_SETSIGMASK | POSIX_SPAWN_CLOEXEC_DEFAULT))

        let env = config.environment.map { "\($0.key)=\($0.value)" }
        var pid: pid_t = 0
        let argv = args.map { strdup($0) } + [nil]
        let envp = env.map { strdup($0) } + [nil]
        defer { argv.forEach { free($0) }; envp.forEach { free($0) } }
        let rc = posix_spawn(&pid, config.executable.path, &actions, &attr, argv, envp)
        guard rc == 0 else {
            throw NSError(domain: NSPOSIXErrorDomain, code: Int(rc),
                userInfo: [NSLocalizedDescriptionKey: String(format: L("error.spawn", "Can't start %@: %@"), config.executable.path, String(cString: strerror(rc)))])
        }

        self.token = token
        run.pid = pid
        run.stdin = stdinPipe.write
        run.stdout = stdoutPipe.read
        keep.removeAll { $0 == stdinPipe.write || $0 == stdoutPipe.read }
        _ = fcntl(run.stdout, F_SETFL, fcntl(run.stdout, F_GETFL) | O_NONBLOCK)

        let source = DispatchSource.makeReadSource(fileDescriptor: run.stdout, queue: .main)
        source.setEventHandler { [weak self, weak run] in
            guard let self, let run else { return }
            if !self.readStdout(run) { run.source?.cancel(); run.source = nil }
        }
        // A descriptor watched by a source is closed only once the source is cancelled.
        let stdoutFD = run.stdout
        source.setCancelHandler { [weak run] in
            close(stdoutFD)
            run?.stdout = -1
        }
        run.source = source
        source.resume()

        // Waits without reaping: until didExit reaps it on the main queue, the pid (and its
        // process group) can't be reused, so the kills that target them hit nothing else.
        DispatchQueue.global().async { [weak self] in
            var info = siginfo_t()
            while waitid(P_PID, id_t(pid), &info, WEXITED | WNOWAIT) < 0 && errno == EINTR {}
            DispatchQueue.main.async { self?.didExit(run) }
        }
    }

    // MARK: Standard output

    /// Reads what is available; false at the end of the stream.
    private func readStdout(_ run: Run) -> Bool {
        var chunk = [UInt8](repeating: 0, count: 4096)
        while true {
            let n = read(run.stdout, &chunk, chunk.count)
            if n > 0 {
                run.buffer.append(contentsOf: chunk[0..<n])
                consumeLines(run)
            } else if n == 0 {
                return false
            } else {
                return errno == EAGAIN || errno == EINTR
            }
        }
    }

    private func consumeLines(_ run: Run) {
        while let newline = run.buffer.firstIndex(of: 0x0A) {
            let line = String(decoding: run.buffer[run.buffer.startIndex..<newline], as: UTF8.self)
            run.buffer.removeSubrange(run.buffer.startIndex...newline)
            if run.gotLine {
                note("unexpected line on standard output: \(line)")
                continue
            }
            run.gotLine = true
            run.timeout?.cancel()
            handle(line: line, run: run)
        }
    }

    private func handle(line: String, run: Run) {
        guard run === self.run, !run.stopRequested else { return }
        let report: ServerReport
        do {
            report = try parseServerReport(line)
        } catch {
            note("unreadable status line from the server: \(line)")
            run.superseded = true
            kill(run)
            state = .failed(.startFailed(config.log.tail(currentRun: true)))
            return
        }
        run.report = report
        switch report {
        case .ready(let info):
            note("server ready at \(info.url!.absoluteString) (pid \(info.pid ?? 0), \(info.version ?? "?"))")
            state = .ready(url: info.url!, ownsServer: true)
        case .running(let info) where !info.embedded:
            note("a refract local from a terminal answers at \(info.url!.absoluteString): using it")
            state = .ready(url: info.url!, ownsServer: false)
        case .running:
            note("another instance of the app holds the data folder")
            state = .failed(.anotherAppInstance)
        case .unresponsive(let info):
            note("the data folder is held by pid \(info.pid ?? 0), which doesn't answer")
            state = .failed(.unresponsive(pid: info.pid))
        case .error(let message):
            note("server error: \(message)")
            if let port = run.port {
                // The remembered port may be taken: go back to a free one, once.
                note("retrying without --port \(port)")
                run.superseded = true
                attempt(port: nil)
            } else {
                state = .failed(.startFailed(message))
            }
        }
    }

    // MARK: Exit

    private func didExit(_ run: Run) {
        run.timeout?.cancel()
        run.killTimer?.cancel()
        // Whatever it left in its process group goes with it (compiles run in groups of
        // their own, which the server stops itself), then the zombie is reaped.
        Darwin.kill(-run.pid, SIGKILL)
        var status: Int32 = 0
        while waitpid(run.pid, &status, 0) < 0 && errno == EINTR {}
        run.exited = true
        if run.stdin >= 0 { close(run.stdin); run.stdin = -1 }
        if run.source != nil {
            _ = readStdout(run)
            run.source?.cancel()  // its cancel handler closes standard output
            run.source = nil
        }

        let signaled = (status & 0x7f) != 0
        let code = signaled ? status & 0x7f : (status >> 8) & 0xff
        note("server exited (\(signaled ? "signal" : "status") \(code))")

        let completions = run.stopCompletions
        run.stopCompletions = []
        if run.stopRequested {
            if run === self.run { state = .stopped }
            let outcome: StopOutcome = run.killed ? .killed : .graceful
            completions.forEach { $0(outcome) }
            return
        }
        guard run === self.run, !run.superseded else { return }
        switch run.report {
        case nil:
            // It left without a line: the end of its log says why (exit code 2 is a crash).
            state = .failed(.startFailed(config.log.tail(currentRun: true)))
        case .ready:
            state = .failed(.exited(status: code, signaled: signaled, logTail: config.log.tail(currentRun: true)))
        default:
            break  // running, unresponsive, error: the line already decided the state
        }
    }

    // MARK: Stop

    /// Closes the server's standard input and waits for it to finish (it saves and
    /// releases the data folder), killing it after `stopTimeout`. Call it after the web
    /// view saved what it holds (`window.refractFlush()`).
    public func stop(completion: ((StopOutcome) -> Void)? = nil) {
        guard let run, !run.exited else {
            if case .ready = state { state = .stopped }
            completion?(.notRunning)
            return
        }
        if let completion { run.stopCompletions.append(completion) }
        guard !run.stopRequested else { return }
        run.stopRequested = true
        run.timeout?.cancel()
        state = .stopping
        note("stopping the server: closing its standard input")
        if run.stdin >= 0 { close(run.stdin); run.stdin = -1 }
        let item = DispatchWorkItem { [weak self, weak run] in
            guard let self, let run, !run.exited else { return }
            self.note("the server didn't stop in \(Int(self.config.stopTimeout)) seconds: killing it")
            self.kill(run)
        }
        run.killTimer = item
        DispatchQueue.main.asyncAfter(deadline: .now() + config.stopTimeout, execute: item)
    }

    /// Stops the server if it runs, then starts it again.
    public func restart() {
        switch state {
        case .starting, .stopping, .ready(_, true):
            stop { [weak self] _ in self?.start() }
        default:
            start()
        }
    }

    /// The server reported tools it can't find: stop it and show the damaged install.
    public func reportMissingTools(_ names: [String]) {
        stop { [weak self] _ in self?.state = .failed(.installDamaged(names)) }
    }

    private func kill(_ run: Run) {
        run.killed = true
        Darwin.kill(-run.pid, SIGKILL)
        Darwin.kill(run.pid, SIGKILL)
    }

    private func note(_ text: String) {
        guard let fd = try? config.log.open(rotating: false) else { return }
        config.log.note(text, fd: fd)
        close(fd)
    }

    /// The environment the server gets: the app's own, without what would make it
    /// something other than `refract local` for this person (another data folder or
    /// secret key, a sign-in mode, a fixed address or a token that must come from the pipe).
    public static func serverEnvironment(from env: [String: String]) -> [String: String] {
        let blocked: Set = [
            "REFRACT_DATA_DIR", "REFRACT_SECRET_KEY", "REFRACT_SECRET_KEY_FILE",
            "REFRACT_AUTH", "REFRACT_PROFILE", "REFRACT_EMBED_TOKEN", "REFRACT_ADDR",
        ]
        // REFRACT_APP_* configure the app itself, not the server.
        return env.filter { !blocked.contains($0.key) && !$0.key.hasPrefix("REFRACT_APP_") }
    }
}
