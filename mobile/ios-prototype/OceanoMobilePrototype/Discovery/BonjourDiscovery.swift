import Foundation
import Combine

struct DiscoveredOceanoHost: Identifiable, Equatable {
    let id: String
    let displayName: String
    let host: String
    let port: Int

    var baseURL: URL? {
        URL(string: "http://\(host):\(port)")
    }
}

final class BonjourDiscovery: NSObject, ObservableObject {
    @Published private(set) var hosts: [DiscoveredOceanoHost] = []
    @Published private(set) var isBrowsing = false

    private let browser = NetServiceBrowser()
    private var services: [NetService] = []
    private var resolvedServiceIDs: Set<String> = []

    override init() {
        super.init()
        browser.delegate = self
    }

    func startBrowsing() {
        guard !isBrowsing else { return }
        isBrowsing = true
        browser.searchForServices(ofType: "_http._tcp.", inDomain: "local.")
    }

    func stopBrowsing() {
        browser.stop()
        isBrowsing = false
    }

    private func serviceID(_ service: NetService) -> String {
        "\(service.name)|\(service.type)|\(service.domain)"
    }

    private func shouldConsider(_ service: NetService) -> Bool {
        // Consider all HTTP Bonjour services, then validate via /api/status.
        true
    }

    private func appendHost(service: NetService) {
        guard let host = service.hostName?.trimmingCharacters(in: .whitespacesAndNewlines),
              !host.isEmpty,
              service.port > 0 else { return }

        let trimmedHost = host.hasSuffix(".") ? String(host.dropLast()) : host
        let item = DiscoveredOceanoHost(
            id: serviceID(service),
            displayName: service.name,
            host: trimmedHost,
            port: service.port
        )
        if !hosts.contains(where: { $0.id == item.id }) {
            hosts.append(item)
        }
    }

    private func resolvedHostAndPort(from service: NetService) -> (host: String, port: Int)? {
        guard let host = service.hostName?.trimmingCharacters(in: .whitespacesAndNewlines),
              !host.isEmpty,
              service.port > 0 else { return nil }
        let trimmedHost = host.hasSuffix(".") ? String(host.dropLast()) : host
        return (trimmedHost, service.port)
    }

    private func isOceanoService(_ service: NetService) async -> Bool {
        guard let endpoint = resolvedHostAndPort(from: service),
              let url = URL(string: "http://\(endpoint.host):\(endpoint.port)/api/status") else {
            return false
        }

        var request = URLRequest(url: url)
        request.timeoutInterval = 2.5
        request.cachePolicy = .reloadIgnoringLocalCacheData

        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            guard let http = response as? HTTPURLResponse, (200...299).contains(http.statusCode) else {
                return false
            }
            guard
                let json = try JSONSerialization.jsonObject(with: data) as? [String: Any],
                json["source"] != nil,
                json["state"] != nil
            else {
                return false
            }
            return true
        } catch {
            return false
        }
    }
}

extension BonjourDiscovery: NetServiceBrowserDelegate {
    func netServiceBrowser(_ browser: NetServiceBrowser, didFind service: NetService, moreComing: Bool) {
        guard shouldConsider(service) else { return }
        services.append(service)
        service.delegate = self
        service.resolve(withTimeout: 5)
    }

    func netServiceBrowser(_ browser: NetServiceBrowser, didRemove service: NetService, moreComing: Bool) {
        let id = serviceID(service)
        services.removeAll { serviceID($0) == id }
        hosts.removeAll { $0.id == id }
        resolvedServiceIDs.remove(id)
    }
}

extension BonjourDiscovery: NetServiceDelegate {
    func netServiceDidResolveAddress(_ sender: NetService) {
        let id = serviceID(sender)
        guard !resolvedServiceIDs.contains(id) else { return }
        resolvedServiceIDs.insert(id)
        Task {
            let isOceano = await isOceanoService(sender)
            await MainActor.run {
                if isOceano {
                    appendHost(service: sender)
                } else {
                    // Allow re-validation if this service is resolved again later.
                    resolvedServiceIDs.remove(id)
                }
            }
        }
    }
}
