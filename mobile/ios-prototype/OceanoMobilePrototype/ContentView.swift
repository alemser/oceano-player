import SwiftUI

struct ContentView: View {
    @EnvironmentObject private var discovery: BonjourDiscovery
    @EnvironmentObject private var stateClient: OceanoStateClient
    @EnvironmentObject private var hostStore: HostStore

    @State private var manualHost = ""
    @State private var manualPort = "8080"
    @State private var showManualEntry = false

    var body: some View {
        Group {
            if let baseURL = hostStore.baseURL {
                ConnectedShellView(baseURL: baseURL)
                    .environmentObject(stateClient)
                    .environmentObject(hostStore)
            } else {
                DiscoveryGateView(
                    discoveredHost: discovery.hosts.first,
                    showManualEntry: $showManualEntry,
                    onConnectDiscovered: { host in useDiscoveredHost(host) },
                    onQuickConnectLocalHostname: { useKnownLocalHostname() }
                )
                .environmentObject(stateClient)
                .sheet(isPresented: $showManualEntry) {
                    NavigationStack {
                        Form {
                            TextField("Host or IP", text: $manualHost)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                            TextField("Port", text: $manualPort)
                                .keyboardType(.numberPad)
                        }
                        .navigationTitle("Manual Connection")
                        .toolbar {
                            ToolbarItem(placement: .cancellationAction) {
                                Button("Cancel") { showManualEntry = false }
                            }
                            ToolbarItem(placement: .confirmationAction) {
                                Button("Connect") {
                                    useManualHost()
                                    showManualEntry = false
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    private func useDiscoveredHost(_ host: DiscoveredOceanoHost) {
        guard let url = host.baseURL else { return }
        hostStore.baseURL = url
        stateClient.connect(baseURL: url)
        manualHost = host.host
        manualPort = String(host.port)
    }

    private func useManualHost() {
        let host = manualHost.trimmingCharacters(in: .whitespacesAndNewlines)
        let port = Int(manualPort.trimmingCharacters(in: .whitespacesAndNewlines)) ?? 8080
        guard !host.isEmpty, let url = URL(string: "http://\(host):\(port)") else { return }
        hostStore.baseURL = url
        stateClient.connect(baseURL: url)
    }

    private func useKnownLocalHostname() {
        guard let url = URL(string: "http://oceano.local:8080") else { return }
        hostStore.baseURL = url
        stateClient.connect(baseURL: url)
        manualHost = "oceano.local"
        manualPort = "8080"
    }
}

private struct DiscoveryGateView: View {
    let discoveredHost: DiscoveredOceanoHost?
    @Binding var showManualEntry: Bool
    let onConnectDiscovered: (DiscoveredOceanoHost) -> Void
    let onQuickConnectLocalHostname: () -> Void

    @EnvironmentObject private var stateClient: OceanoStateClient

    var body: some View {
        NavigationStack {
            VStack(spacing: 24) {
                Spacer()
                Image(systemName: discoveredHost == nil ? "dot.radiowaves.left.and.right" : "checkmark.circle.fill")
                    .font(.system(size: 72, weight: .semibold))
                    .foregroundStyle(discoveredHost == nil ? Color.secondary : Color.green)

                Text(discoveredHost == nil ? "Searching for Oceano device" : "Oceano device found")
                    .font(.title3.weight(.semibold))

                Text(discoveredHost == nil ? "Make sure your iPhone and Pi are on the same network." : "\(discoveredHost?.host ?? ""):\(discoveredHost?.port ?? 8080)")
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 30)

                if let host = discoveredHost {
                    Button {
                        onConnectDiscovered(host)
                    } label: {
                        Label("Connect", systemImage: "link")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .padding(.horizontal, 24)
                } else {
                    ProgressView()
                        .padding(.top, 4)

                    Button {
                        onQuickConnectLocalHostname()
                    } label: {
                        Label("Try oceano.local", systemImage: "dot.radiowaves.left.and.right")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .padding(.horizontal, 24)
                }

                Button("Enter host manually") {
                    showManualEntry = true
                }
                .buttonStyle(.bordered)

                if let error = stateClient.lastError, !stateClient.isConnected {
                    Text(error)
                        .font(.footnote)
                        .foregroundStyle(Color.red)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 24)
                }
                Spacer()
            }
            .navigationTitle("Oceano")
        }
    }
}

private struct ConnectedShellView: View {
    let baseURL: URL
    @EnvironmentObject private var stateClient: OceanoStateClient
    @EnvironmentObject private var hostStore: HostStore

    var body: some View {
        TabView {
            OceanoWebView(url: baseURL.appending(path: "/nowplaying.html"))
                .tabItem {
                    Label("Now Playing", systemImage: "waveform")
                }

            OceanoWebView(url: baseURL.appending(path: "/index.html"))
                .tabItem {
                    Label("Library", systemImage: "books.vertical")
                }

            OceanoWebView(url: baseURL.appending(path: "/history.html"))
                .tabItem {
                    Label("History", systemImage: "clock.arrow.circlepath")
                }

            OceanoWebView(url: baseURL.appending(path: "/?drawer=1"))
                .tabItem {
                    Label("Config", systemImage: "line.3.horizontal")
                }
        }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    hostStore.baseURL = nil
                    stateClient.disconnect()
                } label: {
                    Image(systemName: "xmark.circle")
                }
                .accessibilityLabel("Disconnect")
            }
        }
    }
}
