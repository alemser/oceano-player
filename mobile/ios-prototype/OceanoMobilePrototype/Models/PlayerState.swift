import Foundation

struct PlayerState: Decodable {
    struct Track: Decodable {
        let title: String?
        let artist: String?
        let album: String?
    }

    let source: String
    let state: String
    let track: Track?
}
