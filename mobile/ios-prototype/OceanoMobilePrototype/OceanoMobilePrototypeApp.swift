import SwiftUI

@main
struct OceanoMobilePrototypeApp: App {
    @StateObject private var discovery = BonjourDiscovery()
    @StateObject private var stateClient = OceanoStateClient()
    @StateObject private var hostStore = HostStore()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(discovery)
                .environmentObject(stateClient)
                .environmentObject(hostStore)
                .onAppear {
                    discovery.startBrowsing()
                    if let url = hostStore.baseURL {
                        stateClient.connect(baseURL: url)
                    }
                }
        }
    }
}
