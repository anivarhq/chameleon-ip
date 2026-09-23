import AVFoundation
import SwiftUI

@main
struct ChameleonApp: App {
    @StateObject private var engine = CameraEngine()

    var body: some Scene {
        WindowGroup {
            CameraScreen(engine: engine)
            #if os(iOS)
                // Aiming the camera means looking at the screen, and on iOS
                // the app has to stay in front to keep the camera at all.
                .onAppear { UIApplication.shared.isIdleTimerDisabled = true }
                .onDisappear { UIApplication.shared.isIdleTimerDisabled = false }
            #endif
        }
    }
}

struct CameraScreen: View {
    @ObservedObject var engine: CameraEngine
    @State private var guarding = false

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            if guarding {
                // Guard mode: an almost-black screen, because iOS stops the
                // camera the moment the app leaves the foreground. Tap to wake.
                GuardScreen(viewers: engine.viewers)
                    .onTapGesture { guarding = false }
            } else {
                CameraPreview(session: engine.session).ignoresSafeArea()
                overlay
            }
        }
        .onChange(of: engine.isRunning) { running in
            guarding = false
            if !running { return }
        }
    }

    private var overlay: some View {
        VStack {
            HStack {
                Label(status, systemImage: engine.isRunning ? "dot.radiowaves.left.and.right" : "video.slash")
                    .foregroundStyle(.white)
                    .padding(10)
                    .background(.black.opacity(0.45), in: Capsule())
                Spacer()
            }
            .padding()

            Spacer()

            if engine.isRunning {
                Text(streamURL)
                    .font(.footnote.monospaced())
                    .foregroundStyle(.white)
                    .textSelection(.enabled)
                    .padding(8)
                    .background(.black.opacity(0.45), in: RoundedRectangle(cornerRadius: 8))
                    .padding(.horizontal)
            }

            if let problem = engine.problem {
                Text(problem).foregroundStyle(.red).padding(8)
            }

            HStack(spacing: 16) {
                Button(engine.isRunning ? "Stop" : "Start camera") {
                    engine.isRunning ? engine.stop() : engine.start()
                }
                .buttonStyle(.borderedProminent)

                if engine.isRunning {
                    Button("Guard") { guarding = true }.buttonStyle(.bordered)
                }
            }
            .padding(.bottom, 24)
        }
    }

    private var status: String {
        if !engine.isRunning { return "Not streaming" }
        return engine.viewers > 0 ? "\(engine.viewers) watching" : "Waiting for a viewer"
    }

    private var streamURL: String {
        "rtsp://\(Credentials.user):\(Credentials.password())@\(LocalAddress.current()):8554/main"
    }
}

/// Black, with one dim line that drifts so it cannot burn into the screen.
struct GuardScreen: View {
    let viewers: Int
    @State private var offset: CGFloat = -40

    var body: some View {
        VStack {
            Spacer()
            Text(viewers > 0 ? "\(viewers) watching" : "Streaming")
                .font(.caption2)
                .foregroundStyle(.white.opacity(0.25))
                .offset(y: offset)
                .onAppear {
                    withAnimation(.easeInOut(duration: 90).repeatForever(autoreverses: true)) {
                        offset = 40
                    }
                }
            Spacer()
            Text("Tap to wake").font(.caption2).foregroundStyle(.white.opacity(0.15)).padding(.bottom, 40)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
    }
}

#if os(iOS)
struct CameraPreview: UIViewRepresentable {
    let session: AVCaptureSession

    func makeUIView(context: Context) -> PreviewView { PreviewView(session: session) }
    func updateUIView(_ view: PreviewView, context: Context) {}

    final class PreviewView: UIView {
        override class var layerClass: AnyClass { AVCaptureVideoPreviewLayer.self }
        init(session: AVCaptureSession) {
            super.init(frame: .zero)
            let layer = self.layer as! AVCaptureVideoPreviewLayer
            layer.session = session
            layer.videoGravity = .resizeAspectFill
        }
        required init?(coder: NSCoder) { fatalError() }
    }
}
#else
struct CameraPreview: NSViewRepresentable {
    let session: AVCaptureSession

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        let layer = AVCaptureVideoPreviewLayer(session: session)
        layer.videoGravity = .resizeAspectFill
        view.layer = layer
        view.wantsLayer = true
        return view
    }
    func updateNSView(_ view: NSView, context: Context) {}
}
#endif

/// The address an NVR on the same network can reach.
enum LocalAddress {
    static func current() -> String {
        var address = "this device"
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return address }
        defer { freeifaddrs(head) }

        for pointer in sequence(first: first, next: { $0.pointee.ifa_next }) {
            let flags = Int32(pointer.pointee.ifa_flags)
            guard flags & IFF_UP != 0, flags & IFF_LOOPBACK == 0,
                  pointer.pointee.ifa_addr.pointee.sa_family == UInt8(AF_INET) else { continue }

            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            guard getnameinfo(pointer.pointee.ifa_addr, socklen_t(pointer.pointee.ifa_addr.pointee.sa_len),
                              &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 else { continue }
            let candidate = String(cString: host)
            // Wi-Fi or Ethernet only: a cellular address can be reachable
            // from outside the house.
            if candidate.hasPrefix("192.168.") || candidate.hasPrefix("10.") || candidate.hasPrefix("172.") {
                address = candidate
                break
            }
        }
        return address
    }
}
