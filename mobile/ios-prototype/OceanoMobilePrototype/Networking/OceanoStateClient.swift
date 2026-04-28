import Foundation
import Combine

@MainActor
final class OceanoStateClient: ObservableObject {
    @Published private(set) var state: PlayerState?
    @Published private(set) var isConnected = false
    @Published private(set) var lastError: String?

    private var baseURL: URL?
    private var pollingTask: Task<Void, Never>?

    func connect(baseURL: URL) {
        self.baseURL = baseURL
        self.pollingTask?.cancel()
        self.pollingTask = Task { [weak self] in
            await self?.pollLoop()
        }
    }

    func disconnect() {
        pollingTask?.cancel()
        pollingTask = nil
        isConnected = false
    }

    private func pollLoop() async {
        while !Task.isCancelled {
            await fetchStatusOnce()
            try? await Task.sleep(nanoseconds: 1_500_000_000)
        }
    }

    private func fetchStatusOnce() async {
        guard let baseURL else { return }
        do {
            let url = baseURL.appending(path: "/api/status")
            let (data, response) = try await URLSession.shared.data(from: url)
            guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                throw URLError(.badServerResponse)
            }
            let decoded = try JSONDecoder().decode(PlayerState.self, from: data)
            self.state = decoded
            self.isConnected = true
            self.lastError = nil
        } catch {
            self.isConnected = false
            self.lastError = error.localizedDescription
        }
    }
}
