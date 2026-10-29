import AppKit
import CryptoKit
import Foundation
import SwiftUI

private let repositoryAPI = URL(string: "https://api.github.com/repos/palermostest25/RekordLink/releases/latest")!

private struct RuntimeState: Codable {
    var configured: Bool
    var running: Bool
    var state: String
}

private struct SavedConfig: Codable {
    var excludePlaylists: [String]?
    var sharedPlaylists: [String]?
    var mode: String
    var name: String?
    var library: String?
    var output: String?
    var audioRoot: String?
    var allowAudio: Bool
    var inviteConfigured: Bool?
    var listen: String?
    var advertise: String?
    var publicEndpoint: String?
    var interval: String?

    enum CodingKeys: String, CodingKey {
        case excludePlaylists = "exclude_playlists"
        case sharedPlaylists = "shared_playlists"
        case mode, name, library, output
        case audioRoot = "audio_root"
        case allowAudio = "allow_audio"
        case inviteConfigured = "invite_configured"
        case listen, advertise
        case publicEndpoint = "public_endpoint"
        case interval
    }
}

private struct Receipt: Codable {
    var state: String
    var message: String?
    var generated: String?
    var tracks: Int
    var playlists: Int
    var audioReady: Int
    var audioTotal: Int

    enum CodingKeys: String, CodingKey {
        case state, message, generated, tracks, playlists
        case audioReady = "audio_ready"
        case audioTotal = "audio_total"
    }
}

private struct AppState: Codable {
    var version: String
    var service: RuntimeState
    var config: SavedConfig?
    var receipt: Receipt
}

private struct PlaylistChoice: Codable, Identifiable {
    var path: String
    var label: String
    var folder: Bool
    var id: String { path }
}

private struct PlaylistResponse: Codable { var playlists: [PlaylistChoice] }
private struct LogsResponse: Codable { var logs: String }
private struct InviteResponse: Codable { var invite: String }

private struct Release: Codable {
    struct Asset: Codable {
        var name: String
        var browserDownloadURL: URL
        enum CodingKeys: String, CodingKey {
            case name
            case browserDownloadURL = "browser_download_url"
        }
    }
    var tagName: String
    var htmlURL: URL
    var assets: [Asset]
    enum CodingKeys: String, CodingKey {
        case tagName = "tag_name"
        case htmlURL = "html_url"
        case assets
    }
}

private func readableError(_ error: Error) -> String {
    switch error {
    case DecodingError.keyNotFound(let key, _):
        return "The RekordLink engine response is missing “\(key.stringValue)”."
    case DecodingError.typeMismatch(_, let context), DecodingError.valueNotFound(_, let context), DecodingError.dataCorrupted(let context):
        return "The RekordLink engine returned unreadable data at \(context.codingPath.map(\.stringValue).joined(separator: "."))."
    default:
        return error.localizedDescription
    }
}

private struct SetupPayload: Encodable {
    var excludePlaylists: [String]
    var sharedPlaylists: [String]
    var mode: String
    var name: String
    var library: String
    var output: String
    var audioRoot: String
    var allowAudio: Bool
    var invite: String
    var listen: String
    var advertise: String
    var publicEndpoint: String
    var interval: String

    enum CodingKeys: String, CodingKey {
        case excludePlaylists = "exclude_playlists"
        case sharedPlaylists = "shared_playlists"
        case mode, name, library, output
        case audioRoot = "audio_root"
        case allowAudio = "allow_audio"
        case invite, listen, advertise
        case publicEndpoint = "public_endpoint"
        case interval
    }
}

private final class RekordLinkModel: ObservableObject {
    @Published var appState: AppState?
    @Published var mode = "host"
    @Published var name = ""
    @Published var library = ""
    @Published var output = ""
    @Published var audioRoot = ""
    @Published var allowAudio = true
    @Published var invite = ""
    @Published var listen = ":9777"
    @Published var advertise = ""
    @Published var publicEndpoint = ""
    @Published var interval = "15s"
    @Published var playlists: [PlaylistChoice] = []
    @Published var excluded = Set<String>()
    @Published var shared = Set<String>()
    @Published var logs = ""
    @Published var busy = false
    @Published var message = ""
    @Published var errorMessage: String?
    @Published var availableRelease: Release?
    @Published var updateProgress = ""
    @Published var automaticallyChecksForUpdates = UserDefaults.standard.object(forKey: "AutomaticallyChecksForUpdates") as? Bool ?? true {
        didSet { UserDefaults.standard.set(automaticallyChecksForUpdates, forKey: "AutomaticallyChecksForUpdates") }
    }

    var stateDidChange: ((AppState?) -> Void)?
    private var loadedConfiguration = false

    init() {
        repairServiceIdentityIfNeeded()
        if automaticallyChecksForUpdates { checkForUpdates(silent: true) }
    }

    private func repairServiceIdentityIfNeeded() {
        if ProcessInfo.processInfo.environment["REKORDLINK_NO_SERVICE_REPAIR"] == "1" {
            refresh()
            return
        }
        let key = "BackgroundServiceAppPath"
        let currentPath = Bundle.main.bundleURL.standardizedFileURL.path
        guard UserDefaults.standard.string(forKey: key) != currentPath else {
            refresh()
            return
        }
        bridge("repair", decode: [String: Bool].self) { [weak self] result in
            if case .success = result { UserDefaults.standard.set(currentPath, forKey: key) }
            self?.refresh()
        }
    }

    func refresh() {
        bridge("state", decode: AppState.self) { [weak self] result in
            guard let self = self else { return }
            switch result {
            case .success(let state):
                self.appState = state
                if !self.loadedConfiguration, let config = state.config {
                    self.apply(config)
                    self.loadedConfiguration = true
                }
                self.stateDidChange?(state)
            case .failure(let error):
                self.errorMessage = readableError(error)
            }
        }
    }

    private func apply(_ config: SavedConfig) {
        mode = config.mode
        name = config.name ?? ""
        library = config.library ?? ""
        output = config.output ?? ""
        audioRoot = config.audioRoot ?? ""
        allowAudio = config.allowAudio
        listen = config.listen?.isEmpty == false ? config.listen! : ":9777"
        advertise = config.advertise ?? ""
        publicEndpoint = config.publicEndpoint ?? ""
        interval = config.interval?.isEmpty == false ? config.interval! : "15s"
        excluded = Set(config.excludePlaylists ?? [])
        shared = Set(config.sharedPlaylists ?? [])
        if !library.isEmpty { loadPlaylists() }
    }

    func loadPlaylists() {
        guard !library.isEmpty else { playlists = []; return }
        bridge("playlists", payload: ["library": library], decode: PlaylistResponse.self) { [weak self] result in
            switch result {
            case .success(let response): self?.playlists = response.playlists
            case .failure(let error): self?.errorMessage = readableError(error)
            }
        }
    }

    func save() {
        busy = true
        let payload = SetupPayload(excludePlaylists: Array(excluded).sorted(), sharedPlaylists: Array(shared).sorted(), mode: mode, name: name, library: library, output: output, audioRoot: audioRoot, allowAudio: allowAudio, invite: invite, listen: listen, advertise: advertise, publicEndpoint: publicEndpoint, interval: interval)
        bridge("setup", payload: payload, decode: [String: String].self) { [weak self] result in
            guard let self = self else { return }
            self.busy = false
            switch result {
            case .success(let response):
                self.message = response["message"] ?? "Background sync started."
                self.refresh()
            case .failure(let error): self.errorMessage = readableError(error)
            }
        }
    }

    func control(_ action: String) {
        busy = true
        bridge("control", payload: ["action": action], decode: [String: String].self) { [weak self] result in
            guard let self = self else { return }
            self.busy = false
            switch result {
            case .success(let response):
                self.message = response["message"] ?? "Done."
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { self.refresh() }
            case .failure(let error): self.errorMessage = readableError(error)
            }
        }
    }

    func copyInvite() {
        bridge("invite", decode: InviteResponse.self) { [weak self] result in
            switch result {
            case .success(let response):
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(response.invite, forType: .string)
                self?.message = "Invite copied. Send it privately to your other DJ."
            case .failure(let error): self?.errorMessage = readableError(error)
            }
        }
    }

    func loadLogs() {
        bridge("logs", decode: LogsResponse.self) { [weak self] result in
            switch result {
            case .success(let response): self?.logs = response.logs
            case .failure(let error): self?.errorMessage = readableError(error)
            }
        }
    }

    func chooseLibrary() {
        let panel = NSOpenPanel()
        panel.title = "Choose rekordbox XML export"
        panel.allowedFileTypes = ["xml"]
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        if panel.runModal() == .OK, let path = panel.url?.path {
            library = path
            if output.isEmpty { output = URL(fileURLWithPath: path).deletingLastPathComponent().appendingPathComponent("rekordlink-shared.xml").path }
            loadPlaylists()
        }
    }

    func chooseOutput() {
        let panel = NSSavePanel()
        panel.title = "Choose combined RekordLink XML"
        panel.allowedFileTypes = ["xml"]
        panel.nameFieldStringValue = "rekordlink-shared.xml"
        if panel.runModal() == .OK, let path = panel.url?.path { output = path }
    }

    func chooseAudioRoot() {
        let panel = NSOpenPanel()
        panel.title = "Choose synchronized audio folder"
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        if panel.runModal() == .OK, let path = panel.url?.path { audioRoot = path }
    }

    func setExcluded(_ path: String, _ value: Bool) {
        if value { excluded.insert(path); shared.remove(path) } else { excluded.remove(path) }
    }

    func setShared(_ path: String, _ value: Bool) {
        if value { shared.insert(path); excluded.remove(path) } else { shared.remove(path) }
    }

    func checkForUpdates(silent: Bool = false) {
        if !silent { updateProgress = "Checking…" }
        var request = URLRequest(url: repositoryAPI)
        request.setValue("RekordLink/\(appState?.version ?? currentVersion)", forHTTPHeaderField: "User-Agent")
        URLSession.shared.dataTask(with: request) { [weak self] data, _, error in
            DispatchQueue.main.async {
                guard let self = self else { return }
                if let error = error {
                    if !silent { self.errorMessage = "Could not check for updates: \(error.localizedDescription)" }
                    self.updateProgress = ""
                    return
                }
                guard let data = data, let release = try? JSONDecoder().decode(Release.self, from: data) else {
                    if !silent { self.errorMessage = "GitHub returned an unreadable update response." }
                    self.updateProgress = ""
                    return
                }
                let latest = release.tagName.trimmingCharacters(in: CharacterSet(charactersIn: "vV"))
                let installed = self.appState?.version ?? currentVersion
                if latest.compare(installed, options: .numeric) == .orderedDescending {
                    self.availableRelease = release
                    self.updateProgress = "Version \(latest) is ready."
                } else {
                    self.availableRelease = nil
                    self.updateProgress = silent ? "" : "RekordLink is up to date."
                }
            }
        }.resume()
    }

    func installAvailableUpdate() {
        guard let release = availableRelease else { return }
        let version = release.tagName.trimmingCharacters(in: CharacterSet(charactersIn: "vV"))
        guard let archive = release.assets.first(where: { $0.name.hasPrefix("RekordLink_") && $0.name.hasSuffix("_macOS_universal.zip") }),
              let sums = release.assets.first(where: { $0.name == "SHA256SUMS" }) else {
            errorMessage = "This release does not contain the universal macOS update and checksum."
            return
        }
        updateProgress = "Downloading version \(version)…"
        download(archive.browserDownloadURL) { [weak self] archiveResult in
            guard let self = self else { return }
            switch archiveResult {
            case .failure(let error): DispatchQueue.main.async { self.errorMessage = error.localizedDescription; self.updateProgress = "" }
            case .success(let archiveData):
                self.download(sums.browserDownloadURL) { sumsResult in
                    switch sumsResult {
                    case .failure(let error): DispatchQueue.main.async { self.errorMessage = error.localizedDescription; self.updateProgress = "" }
                    case .success(let sumsData): self.stageUpdate(data: archiveData, sums: sumsData, archiveName: archive.name)
                    }
                }
            }
        }
    }

    private func stageUpdate(data: Data, sums: Data, archiveName: String) {
        let digest = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
        let text = String(data: sums, encoding: .utf8) ?? ""
        guard text.split(separator: "\n").contains(where: { line in
            let fields = line.split(whereSeparator: { $0 == " " || $0 == "\t" })
            let listed = fields.last.map { URL(fileURLWithPath: String($0).replacingOccurrences(of: "*", with: "")).lastPathComponent }
            return fields.count >= 2 && fields[0].lowercased() == digest && listed == archiveName
        }) else {
            DispatchQueue.main.async { self.errorMessage = "The downloaded update failed its SHA-256 integrity check."; self.updateProgress = "" }
            return
        }
        do {
            let root = FileManager.default.temporaryDirectory.appendingPathComponent("RekordLink-Update-\(UUID().uuidString)")
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
            let zip = root.appendingPathComponent(archiveName)
            try data.write(to: zip, options: .atomic)
            let extract = root.appendingPathComponent("Extracted")
            try FileManager.default.createDirectory(at: extract, withIntermediateDirectories: true)
            try runTool("/usr/bin/ditto", ["-x", "-k", zip.path, extract.path])
            guard let app = FileManager.default.enumerator(at: extract, includingPropertiesForKeys: nil)?.allObjects.compactMap({ $0 as? URL }).first(where: { $0.lastPathComponent == "RekordLink.app" }) else {
                throw NSError(domain: "RekordLinkUpdate", code: 1, userInfo: [NSLocalizedDescriptionKey: "The update did not contain RekordLink.app."])
            }
            try runTool("/usr/bin/codesign", ["--verify", "--deep", "--strict", app.path])
            guard NSDictionary(contentsOf: app.appendingPathComponent("Contents/Info.plist"))?["CFBundleIdentifier"] as? String == Bundle.main.bundleIdentifier else {
                throw NSError(domain: "RekordLinkUpdate", code: 2, userInfo: [NSLocalizedDescriptionKey: "The update has the wrong application identity."])
            }
            try launchInstaller(stagedApp: app, destination: Bundle.main.bundleURL, root: root)
            DispatchQueue.main.async {
                self.updateProgress = "Installing…"
                NSApp.terminate(nil)
            }
        } catch {
            DispatchQueue.main.async { self.errorMessage = "Could not install update: \(error.localizedDescription)"; self.updateProgress = "" }
        }
    }

    private func download(_ url: URL, completion: @escaping (Result<Data, Error>) -> Void) {
        var request = URLRequest(url: url)
        request.setValue("RekordLink/\(currentVersion)", forHTTPHeaderField: "User-Agent")
        URLSession.shared.dataTask(with: request) { data, _, error in
            if let error = error { completion(.failure(error)); return }
            guard let data = data else { completion(.failure(NSError(domain: "RekordLinkUpdate", code: 3, userInfo: [NSLocalizedDescriptionKey: "The update download was empty."]))); return }
            completion(.success(data))
        }.resume()
    }
}

private let currentVersion = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"

private func bridge<Input: Encodable, Output: Decodable>(_ action: String, payload: Input? = Optional<String>.none, decode: Output.Type, completion: @escaping (Result<Output, Error>) -> Void) {
    DispatchQueue.global(qos: .userInitiated).async {
        do {
            let task = Process()
            task.executableURL = Bundle.main.resourceURL?.appendingPathComponent("rekordlink")
            task.arguments = ["_native", action]
            let outputPipe = Pipe()
            let errorPipe = Pipe()
            task.standardOutput = outputPipe
            task.standardError = errorPipe
            if let payload = payload {
                let inputPipe = Pipe()
                task.standardInput = inputPipe
                try task.run()
                inputPipe.fileHandleForWriting.write(try JSONEncoder().encode(payload))
                inputPipe.fileHandleForWriting.closeFile()
            } else {
                task.standardInput = FileHandle.nullDevice
                try task.run()
            }
            let data = outputPipe.fileHandleForReading.readDataToEndOfFile()
            let errorData = errorPipe.fileHandleForReading.readDataToEndOfFile()
            task.waitUntilExit()
            guard task.terminationStatus == 0 else {
                let message = String(data: errorData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "Native engine failed."
                throw NSError(domain: "RekordLink", code: Int(task.terminationStatus), userInfo: [NSLocalizedDescriptionKey: message.replacingOccurrences(of: "rekordlink: ", with: "")])
            }
            let decoded = try JSONDecoder().decode(Output.self, from: data)
            DispatchQueue.main.async { completion(.success(decoded)) }
        } catch {
            DispatchQueue.main.async { completion(.failure(error)) }
        }
    }
}

private func bridge<Output: Decodable>(_ action: String, decode: Output.Type, completion: @escaping (Result<Output, Error>) -> Void) {
    bridge(action, payload: Optional<String>.none, decode: decode, completion: completion)
}

private func runTool(_ path: String, _ arguments: [String]) throws {
    let task = Process()
    task.executableURL = URL(fileURLWithPath: path)
    task.arguments = arguments
    task.standardOutput = FileHandle.nullDevice
    let errorPipe = Pipe()
    task.standardError = errorPipe
    try task.run()
    task.waitUntilExit()
    if task.terminationStatus != 0 {
        let message = String(data: errorPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "Command failed."
        throw NSError(domain: "RekordLink", code: Int(task.terminationStatus), userInfo: [NSLocalizedDescriptionKey: message])
    }
}

private func launchInstaller(stagedApp: URL, destination: URL, root: URL) throws {
    let script = root.appendingPathComponent("install-update.sh")
    let source = """
    #!/bin/sh
    pid="$1"
    staged="$2"
    destination="$3"
    while kill -0 "$pid" 2>/dev/null; do sleep 0.2; done
    backup="${destination}.previous"
    rm -rf "$backup"
    if [ -e "$destination" ]; then mv "$destination" "$backup"; fi
    if /usr/bin/ditto "$staged" "$destination"; then
      "$destination/Contents/Resources/rekordlink" _native repair >/dev/null 2>&1 || true
      /usr/bin/open "$destination"
      rm -rf "$backup"
    else
      rm -rf "$destination"
      if [ -e "$backup" ]; then mv "$backup" "$destination"; fi
      /usr/bin/open "$destination"
    fi
    rm -rf "$(dirname "$0")"
    """
    try source.write(to: script, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: script.path)
    let task = Process()
    task.executableURL = URL(fileURLWithPath: "/bin/sh")
    task.arguments = [script.path, String(ProcessInfo.processInfo.processIdentifier), stagedApp.path, destination.path]
    task.standardOutput = FileHandle.nullDevice
    task.standardError = FileHandle.nullDevice
    try task.run()
}

private struct StatusHeader: View {
    @ObservedObject var model: RekordLinkModel
    var body: some View {
        let running = model.appState?.service.running == true
        let ready = model.appState?.receipt.state == "READY"
        HStack(spacing: 16) {
            ZStack {
                Circle().fill((running ? Color.green : Color.secondary).opacity(0.16)).frame(width: 46, height: 46)
                Image(systemName: running ? "arrow.triangle.2.circlepath.circle.fill" : "pause.circle.fill")
                    .font(.system(size: 27)).foregroundColor(running ? .green : .secondary)
            }
            VStack(alignment: .leading, spacing: 3) {
                Text(running ? "Background sync is running" : "Background sync is stopped").font(.title2).fontWeight(.semibold)
                Text(model.appState?.receipt.message ?? "Loading RekordLink status…").foregroundColor(.secondary).lineLimit(2)
            }
            Spacer()
            if ready, let receipt = model.appState?.receipt {
                VStack(alignment: .trailing, spacing: 3) {
                    Label("Library ready", systemImage: "checkmark.circle.fill").foregroundColor(.green).font(.headline)
                    Text("\(receipt.tracks) tracks · \(receipt.playlists) playlists").font(.caption).foregroundColor(.secondary)
                }
            }
        }
        .padding(18)
        .background(Color(NSColor.controlBackgroundColor))
        .cornerRadius(12)
    }
}

private struct PathRow: View {
    var title: String
    @Binding var value: String
    var action: () -> Void
    var body: some View {
        HStack {
            Text(title).frame(width: 118, alignment: .trailing)
            TextField("Choose a location", text: $value)
            Button("Choose…", action: action)
        }
    }
}

private struct SetupView: View {
    @ObservedObject var model: RekordLinkModel
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            GroupBox(label: Label("Connection", systemImage: "person.2.fill")) {
                VStack(alignment: .leading, spacing: 12) {
                    Picker("This Mac", selection: $model.mode) {
                        Text("Starts the duo").tag("host")
                        Text("Joins a duo").tag("join")
                        Text("Runs a relay").tag("relay")
                    }.pickerStyle(SegmentedPickerStyle())
                    if model.mode != "relay" {
                        HStack { Text("DJ name").frame(width: 118, alignment: .trailing); TextField("Your name", text: $model.name) }
                    }
                    if model.mode == "join" {
                        HStack(alignment: .top) { Text("Private invite").frame(width: 118, alignment: .trailing); TextEditor(text: $model.invite).font(.system(.body, design: .monospaced)).frame(height: 58) }
                    }
                    if model.mode == "host" {
                        HStack { Text("Listen address").frame(width: 118, alignment: .trailing); TextField(":9777", text: $model.listen); Text("Usually leave this alone").font(.caption).foregroundColor(.secondary) }
                        HStack { Text("Advertise as").frame(width: 118, alignment: .trailing); TextField("Optional VPN name or IP", text: $model.advertise) }
                    }
                    if model.mode == "relay" {
                        HStack { Text("Public address").frame(width: 118, alignment: .trailing); TextField("https://your-relay.example", text: $model.publicEndpoint) }
                        HStack { Text("Listen address").frame(width: 118, alignment: .trailing); TextField(":9777", text: $model.listen) }
                    }
                }.padding(10)
            }

            if model.mode != "relay" {
                GroupBox(label: Label("rekordbox library", systemImage: "music.note.list")) {
                    VStack(spacing: 10) {
                        PathRow(title: "Your XML export", value: $model.library, action: model.chooseLibrary)
                        PathRow(title: "Combined XML", value: $model.output, action: model.chooseOutput)
                        HStack { Text("Check every").frame(width: 118, alignment: .trailing); TextField("15s", text: $model.interval).frame(width: 90); Spacer() }
                    }.padding(10)
                }

                GroupBox(label: Label("Audio", systemImage: "waveform")) {
                    VStack(alignment: .leading, spacing: 10) {
                        Toggle("Synchronize the audio files referenced by both libraries", isOn: $model.allowAudio)
                        if model.allowAudio { PathRow(title: "Managed folder", value: $model.audioRoot, action: model.chooseAudioRoot) }
                        Text("Choose removable drives with the button so macOS can remember your approval.").font(.caption).foregroundColor(.secondary)
                    }.padding(10)
                }

                GroupBox(label: HStack { Label("Playlist sharing", systemImage: "rectangle.stack.fill"); Spacer(); Button("Reload", action: model.loadPlaylists).disabled(model.library.isEmpty) }) {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Everything is shared unless you mark it private. Mark your contribution playlist to build one editable Duo Library together.").font(.callout).foregroundColor(.secondary)
                        if model.playlists.isEmpty {
                            Text(model.library.isEmpty ? "Choose your rekordbox XML first." : "No playlists found.").frame(maxWidth: .infinity, minHeight: 54).foregroundColor(.secondary)
                        } else {
                            ScrollView {
                                LazyVStack(spacing: 0) {
                                    ForEach(model.playlists) { playlist in
                                        HStack {
                                            Label(playlist.label, systemImage: playlist.folder ? "folder.fill" : "music.note.list").lineLimit(1).help(playlist.path)
                                            Spacer()
                                            Toggle("Duo Library", isOn: Binding(get: { model.shared.contains(playlist.path) }, set: { model.setShared(playlist.path, $0) })).toggleStyle(CheckboxToggleStyle()).frame(width: 112)
                                            Toggle("Private", isOn: Binding(get: { model.excluded.contains(playlist.path) }, set: { model.setExcluded(playlist.path, $0) })).toggleStyle(CheckboxToggleStyle()).frame(width: 78)
                                        }.padding(.vertical, 7)
                                        Divider()
                                    }
                                }
                            }.frame(height: 180)
                        }
                    }.padding(10)
                }
            }

            HStack {
                if model.appState?.service.configured == true {
                    if model.appState?.service.running == true { Button("Stop Sync") { model.control("stop") } }
                    else { Button("Start Sync") { model.control("start") } }
                    Button("Restart") { model.control("restart") }
                    if model.mode == "host" || model.mode == "relay" { Button("Copy Invite") { model.copyInvite() } }
                }
                Spacer()
                ProgressView().controlSize(.small).opacity(model.busy ? 1 : 0)
                Button("Save & Start Sync", action: model.save).keyboardShortcut(.defaultAction).disabled(model.busy)
            }
        }
    }
}

private struct ActivityView: View {
    @ObservedObject var model: RekordLinkModel
    @State private var showLogs = true
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            GroupBox(label: Label("Combined library", systemImage: "checkmark.seal.fill")) {
                let receipt = model.appState?.receipt
                HStack(spacing: 28) {
                    metric("Tracks", receipt?.tracks ?? 0)
                    metric("Playlists", receipt?.playlists ?? 0)
                    metric("Audio ready", receipt?.audioReady ?? 0)
                    Spacer()
                    if let generated = receipt?.generated { Text("Updated \(generated)").font(.caption).foregroundColor(.secondary) }
                }.padding(12)
            }
            DisclosureGroup("Recent activity", isExpanded: $showLogs) {
                ScrollView {
                    Text(model.logs.isEmpty ? "No activity has been written yet." : model.logs)
                        .font(.system(.caption, design: .monospaced)).frame(maxWidth: .infinity, alignment: .leading).padding(10)
                }.frame(minHeight: 290).background(Color(NSColor.textBackgroundColor)).cornerRadius(8)
            }
            HStack { Spacer(); Button("Refresh Activity") { model.loadLogs(); model.refresh() } }
        }.onAppear { model.loadLogs() }
    }
    private func metric(_ title: String, _ value: Int) -> some View {
        VStack(alignment: .leading, spacing: 2) { Text("\(value)").font(.title).fontWeight(.semibold); Text(title).font(.caption).foregroundColor(.secondary) }
    }
}

private struct UpdatesView: View {
    @ObservedObject var model: RekordLinkModel
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            GroupBox(label: Label("Software updates", systemImage: "arrow.down.circle.fill")) {
                VStack(alignment: .leading, spacing: 14) {
                    HStack {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("RekordLink \(model.appState?.version ?? currentVersion)").font(.headline)
                            Text(model.updateProgress.isEmpty ? "Updates are securely downloaded from GitHub Releases and verified before installation." : model.updateProgress).foregroundColor(.secondary)
                        }
                        Spacer()
                        Button("Check Now") { model.checkForUpdates() }
                        if model.availableRelease != nil { Button("Install Update") { model.installAvailableUpdate() }.keyboardShortcut(.defaultAction) }
                    }
                    Toggle("Automatically check for updates", isOn: $model.automaticallyChecksForUpdates)
                }.padding(12)
            }
            GroupBox(label: Label("File access", systemImage: "externaldrive.fill")) {
                VStack(alignment: .leading, spacing: 10) {
                    Text("RekordLink now keeps background sync attached to this app. Choose XML and audio locations through the Setup screen so macOS remembers the files and removable drives you approved.")
                    Button("Open Privacy Settings") {
                        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles") { NSWorkspace.shared.open(url) }
                    }
                }.padding(12)
            }
            Spacer()
        }
    }
}

private struct RekordLinkView: View {
    @ObservedObject var model: RekordLinkModel
    @State private var page = 0
    var body: some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) { Text("RekordLink").font(.largeTitle).fontWeight(.bold); Text("Your duo, in sync.").foregroundColor(.secondary) }
                Spacer()
                Picker("", selection: $page) { Text("Setup").tag(0); Text("Activity").tag(1); Text("Updates").tag(2) }.pickerStyle(SegmentedPickerStyle()).frame(width: 300)
            }.padding(.horizontal, 24).padding(.vertical, 18)
            Divider()
            ScrollView {
                VStack(spacing: 18) {
                    StatusHeader(model: model)
                    if page == 0 { SetupView(model: model) }
                    else if page == 1 { ActivityView(model: model) }
                    else { UpdatesView(model: model) }
                }.padding(24)
            }
            if !model.message.isEmpty {
                Divider()
                HStack { Image(systemName: "checkmark.circle.fill").foregroundColor(.green); Text(model.message); Spacer(); Button(action: { model.message = "" }) { Image(systemName: "xmark").accessibilityLabel("Dismiss") }.buttonStyle(PlainButtonStyle()) }.padding(12)
            }
        }
        .frame(minWidth: 760, idealWidth: 860, minHeight: 620, idealHeight: 760)
        .alert(item: Binding(get: { model.errorMessage.map { AppError(message: $0) } }, set: { _ in model.errorMessage = nil })) { error in
            Alert(title: Text("RekordLink needs attention"), message: Text(error.message), dismissButton: .default(Text("OK")))
        }
    }
}

private struct AppError: Identifiable { let id = UUID(); let message: String }

private func launchedAtLogin(_ event: NSAppleEventDescriptor?) -> Bool {
    event?.eventID == kAEOpenApplication &&
        event?.paramDescriptor(forKeyword: keyAEPropData)?.enumCodeValue == keyAELaunchedAsLogInItem
}

private final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    let model = RekordLinkModel()
    var window: NSWindow!
    var statusItem: NSStatusItem!
    var stateItem: NSMenuItem!
    var timer: Timer?
    var updateTimer: Timer?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        let content = RekordLinkView(model: model)
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 860, height: 760), styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView], backing: .buffered, defer: false)
        window.title = "RekordLink"
        window.titlebarAppearsTransparent = true
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.contentView = NSHostingView(rootView: content)
        window.center()

        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        statusItem.button?.image = NSImage(systemSymbolName: "arrow.triangle.2.circlepath", accessibilityDescription: "RekordLink")
        statusItem.button?.image?.isTemplate = true
        statusItem.button?.toolTip = "RekordLink duo library sync"
        let menu = NSMenu()
        stateItem = menu.addItem(withTitle: "Sync: Checking…", action: nil, keyEquivalent: "")
        menu.addItem(.separator())
        menu.addItem(withTitle: "Open RekordLink", action: #selector(showWindow), keyEquivalent: "o").target = self
        menu.addItem(withTitle: "Start Background Sync", action: #selector(startSync), keyEquivalent: "").target = self
        menu.addItem(withTitle: "Stop Background Sync", action: #selector(stopSync), keyEquivalent: "").target = self
        menu.addItem(withTitle: "Restart Background Sync", action: #selector(restartSync), keyEquivalent: "").target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: "Check for Updates…", action: #selector(checkUpdates), keyEquivalent: "").target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit RekordLink", action: #selector(quit), keyEquivalent: "q").target = self
        statusItem.menu = menu
        model.stateDidChange = { [weak self] state in self?.stateItem.title = state?.service.running == true ? "Sync: Running" : (state?.service.configured == true ? "Sync: Stopped" : "Sync: Not configured") }
        timer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak model] _ in model?.refresh() }
        updateTimer = Timer.scheduledTimer(withTimeInterval: 24 * 60 * 60, repeats: true) { [weak model] _ in
            if model?.automaticallyChecksForUpdates == true { model?.checkForUpdates(silent: true) }
        }
        if !launchedAtLogin(NSAppleEventManager.shared().currentAppleEvent) {
            showWindow()
        }
    }

    @objc func showWindow() { window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true) }
    @objc func startSync() { model.control("start") }
    @objc func stopSync() { model.control("stop") }
    @objc func restartSync() { model.control("restart") }
    @objc func checkUpdates() { showWindow(); model.checkForUpdates() }
    @objc func quit() { NSApp.terminate(nil) }
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool { showWindow(); return true }
}

private func runServiceWrapperIfNeeded() -> Never? {
    let args = CommandLine.arguments
    guard args.count == 3, args[1] == "_service_run" else { return nil }
    let engine = Bundle.main.resourceURL!.appendingPathComponent("rekordlink")
    let task = Process()
    task.executableURL = engine
    task.arguments = ["_service_run", args[2]]
    task.standardInput = FileHandle.standardInput
    task.standardOutput = FileHandle.standardOutput
    task.standardError = FileHandle.standardError
    do { try task.run(); task.waitUntilExit(); exit(task.terminationStatus) }
    catch { fputs("rekordlink: \(error.localizedDescription)\n", stderr); exit(1) }
}

private func installToStableLocationIfNeeded() -> Bool {
    if ProcessInfo.processInfo.environment["REKORDLINK_NO_RELOCATE"] == "1" { return false }
    let current = Bundle.main.bundleURL.standardizedFileURL
    let homeApps = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Applications", isDirectory: true)
    let systemApps = URL(fileURLWithPath: "/Applications", isDirectory: true)
    if current.path.hasPrefix(homeApps.path + "/") || current.path.hasPrefix(systemApps.path + "/") { return false }
    do {
        try FileManager.default.createDirectory(at: homeApps, withIntermediateDirectories: true)
        let destination = homeApps.appendingPathComponent("RekordLink.app", isDirectory: true)
        if FileManager.default.fileExists(atPath: destination.path) { try FileManager.default.removeItem(at: destination) }
        try FileManager.default.copyItem(at: current, to: destination)
        try runTool("/usr/bin/open", ["-n", destination.path])
        return true
    } catch {
        NSAlert(error: error).runModal()
        return false
    }
}

@main
private struct RekordLinkApplication {
    private static var appDelegate: AppDelegate?

    static func main() {
        _ = runServiceWrapperIfNeeded()
        let application = NSApplication.shared
        if !installToStableLocationIfNeeded() {
            appDelegate = AppDelegate()
            application.delegate = appDelegate
            application.run()
        }
    }
}
