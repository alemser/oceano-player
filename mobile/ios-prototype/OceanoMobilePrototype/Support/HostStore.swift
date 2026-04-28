import Foundation
import Combine

final class HostStore: ObservableObject {
    @Published var baseURL: URL? {
        didSet {
            guard let baseURL else {
                UserDefaults.standard.removeObject(forKey: Self.baseURLKey)
                return
            }
            UserDefaults.standard.set(baseURL.absoluteString, forKey: Self.baseURLKey)
        }
    }

    private static let baseURLKey = "oceano.prototype.base_url"

    init() {
        if let raw = UserDefaults.standard.string(forKey: Self.baseURLKey),
           let url = URL(string: raw) {
            self.baseURL = url
        } else {
            self.baseURL = nil
        }
    }
}
