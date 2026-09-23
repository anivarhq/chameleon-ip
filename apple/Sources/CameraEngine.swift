import AVFoundation
import Foundation
import Mobile
import VideoToolbox
#if os(iOS)
import UIKit
#endif

/// Captures the camera and hardware-encodes it, then hands the encoded frames
/// to the shared core, which serves them over RTSP and ONVIF.
///
/// Camera buffers go straight into VideoToolbox without ever being drawn or
/// copied through the CPU; only the encoded frames are copied, once.
final class CameraEngine: NSObject, ObservableObject {
    @Published private(set) var isRunning = false
    @Published private(set) var viewers = 0
    @Published private(set) var problem: String?

    let session = AVCaptureSession()

    private let queue = DispatchQueue(label: "chameleon.capture")
    private var compressor: VTCompressionSession?
    private var started: CFTimeInterval = 0
    private var poll: Timer?

    private let width: Int32 = 1280
    private let height: Int32 = 720
    private let fps: Int32 = 15
    private let bitrate: Int32 = 2_000_000

    func start() {
        guard !isRunning else { return }
        AVCaptureDevice.requestAccess(for: .video) { [weak self] granted in
            guard let self else { return }
            guard granted else {
                DispatchQueue.main.async { self.problem = "Chameleon needs the camera to work." }
                return
            }
            self.queue.async { self.configureAndRun() }
        }
    }

    func stop() {
        queue.async {
            self.session.stopRunning()
            if let compressor = self.compressor {
                VTCompressionSessionInvalidate(compressor)
                self.compressor = nil
            }
            MobileStop()
            DispatchQueue.main.async {
                self.poll?.invalidate()
                self.poll = nil
                self.isRunning = false
                self.viewers = 0
            }
        }
    }

    private func configureAndRun() {
        do {
            try startCore()
            try startCapture()
            startEncoder()
        } catch {
            DispatchQueue.main.async { self.problem = error.localizedDescription }
            return
        }
        session.startRunning()
        DispatchQueue.main.async {
            self.isRunning = true
            // The core knows who is connected; the screen just repeats it.
            self.poll = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { _ in
                self.viewers = MobileViewers()
            }
        }
    }

    private func startCore() throws {
        let config: [String: Any] = [
            "address": ":8554",
            "onvif_address": ":8000",
            "path": "main",
            "user": Credentials.user,
            "pass": Credentials.password(),
            "uuid": Credentials.deviceUUID(),
            "name": deviceName,
            "model": deviceModel,
            "serial": String(Credentials.deviceUUID().suffix(12)),
            "width": Int(width), "height": Int(height),
            "fps": Int(fps), "bitrate": Int(bitrate),
        ]
        let json = String(data: try JSONSerialization.data(withJSONObject: config), encoding: .utf8)!
        try MobileStart(json)
    }

    private func startCapture() throws {
        session.beginConfiguration()
        session.sessionPreset = .hd1280x720

        guard let device = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .back)
            ?? AVCaptureDevice.default(for: .video) else {
            throw EngineError.noCamera
        }
        let input = try AVCaptureDeviceInput(device: device)
        guard session.canAddInput(input) else { throw EngineError.noCamera }
        session.addInput(input)

        let output = AVCaptureVideoDataOutput()
        output.videoSettings = [kCVPixelBufferPixelFormatTypeKey as String:
                                    kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange]
        // Dropping late frames is right for a live camera: a stale frame is
        // worth less than a current one, and a backlog stalls the encoder.
        output.alwaysDiscardsLateVideoFrames = true
        output.setSampleBufferDelegate(self, queue: queue)
        guard session.canAddOutput(output) else { throw EngineError.noCamera }
        session.addOutput(output)

        session.commitConfiguration()
    }

    private func startEncoder() {
        var session: VTCompressionSession?
        VTCompressionSessionCreate(
            allocator: kCFAllocatorDefault,
            width: width, height: height,
            codecType: kCMVideoCodecType_H264,
            encoderSpecification: nil,
            imageBufferAttributes: nil,
            compressedDataAllocator: nil,
            outputCallback: nil, refcon: nil,
            compressionSessionOut: &session)
        guard let session else { return }

        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_RealTime, value: kCFBooleanTrue)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_ProfileLevel,
                             value: kVTProfileLevel_H264_High_AutoLevel)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_AverageBitRate,
                             value: NSNumber(value: bitrate))
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_ExpectedFrameRate,
                             value: NSNumber(value: fps))
        // A keyframe every two seconds bounds how long a joining viewer waits
        // when the encoder cannot be interrupted.
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration,
                             value: NSNumber(value: 2))
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_AllowFrameReordering, value: kCFBooleanFalse)
        VTCompressionSessionPrepareToEncodeFrames(session)
        compressor = session
    }

    private enum EngineError: LocalizedError {
        case noCamera
        var errorDescription: String? { "No usable camera on this device." }
    }

    private var deviceName: String {
        #if os(iOS)
        return UIDevice.current.name
        #else
        return Host.current().localizedName ?? "Mac"
        #endif
    }

    private var deviceModel: String {
        #if os(iOS)
        return UIDevice.current.model
        #else
        return "Mac"
        #endif
    }
}

extension CameraEngine: AVCaptureVideoDataOutputSampleBufferDelegate {
    func captureOutput(_ output: AVCaptureOutput,
                       didOutput sampleBuffer: CMSampleBuffer,
                       from connection: AVCaptureConnection) {
        guard let compressor,
              let pixels = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }

        let time = CMSampleBufferGetPresentationTimeStamp(sampleBuffer)
        var properties: [CFString: Any] = [:]
        // A viewer just joined: give them a picture now rather than at the
        // next keyframe.
        if MobileTakeKeyframeWanted() {
            properties[kVTEncodeFrameOptionKey_ForceKeyFrame] = true
        }

        VTCompressionSessionEncodeFrame(
            compressor, imageBuffer: pixels, presentationTimeStamp: time,
            duration: .invalid, frameProperties: properties as CFDictionary,
            infoFlagsOut: nil
        ) { [weak self] status, _, buffer in
            guard status == noErr, let buffer, let self else { return }
            self.push(buffer, at: time)
        }
    }

    /// VideoToolbox hands back length-prefixed NAL units; RTSP wants them
    /// start-code delimited, with the parameter sets in front of every
    /// keyframe so a viewer joining between them can decode.
    private func push(_ sample: CMSampleBuffer, at time: CMTime) {
        guard let block = CMSampleBufferGetDataBuffer(sample) else { return }

        // The clock starts at the first frame that actually comes out of the
        // encoder, not when the session was told to run. Taking it from the
        // session start left the first frames stamped with the time since the
        // device booted, and then jumping backwards.
        if started == 0 { started = CMTimeGetSeconds(time) }

        var annexB = Data()
        let isKeyframe = Self.isKeyframe(sample)

        if isKeyframe, let format = CMSampleBufferGetFormatDescription(sample) {
            for index in 0..<parameterSetCount(format) {
                if let set = parameterSet(format, index) {
                    annexB.append(contentsOf: [0, 0, 0, 1])
                    annexB.append(set)
                }
            }
        }

        var lengthAtOffset = 0, totalLength = 0
        var pointer: UnsafeMutablePointer<Int8>?
        guard CMBlockBufferGetDataPointer(block, atOffset: 0, lengthAtOffsetOut: &lengthAtOffset,
                                          totalLengthOut: &totalLength, dataPointerOut: &pointer) == noErr,
              let pointer else { return }

        var offset = 0
        while offset < totalLength - 4 {
            var length: UInt32 = 0
            memcpy(&length, pointer.advanced(by: offset), 4)
            length = CFSwapInt32BigToHost(length)
            annexB.append(contentsOf: [0, 0, 0, 1])
            annexB.append(Data(bytes: pointer.advanced(by: offset + 4), count: Int(length)))
            offset += 4 + Int(length)
        }

        let micros = Int64((CMTimeGetSeconds(time) - started) * 1_000_000)
        try? MobilePushFrame(annexB, max(0, micros))
    }

    /// VideoToolbox marks a frame as "not sync" when it depends on others;
    /// no such mark means it is a keyframe.
    private static func isKeyframe(_ sample: CMSampleBuffer) -> Bool {
        guard let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: false),
              CFArrayGetCount(attachments) > 0 else { return true }
        let first = unsafeBitCast(CFArrayGetValueAtIndex(attachments, 0), to: CFDictionary.self)
        let notSync = CFDictionaryGetValue(first, Unmanaged.passUnretained(kCMSampleAttachmentKey_NotSync).toOpaque())
        return notSync == nil
    }

    private func parameterSetCount(_ format: CMFormatDescription) -> Int {
        var count = 0
        CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, parameterSetIndex: 0,
                                                           parameterSetPointerOut: nil,
                                                           parameterSetSizeOut: nil,
                                                           parameterSetCountOut: &count,
                                                           nalUnitHeaderLengthOut: nil)
        return count
    }

    private func parameterSet(_ format: CMFormatDescription, _ index: Int) -> Data? {
        var pointer: UnsafePointer<UInt8>?
        var size = 0
        guard CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
            format, parameterSetIndex: index,
            parameterSetPointerOut: &pointer, parameterSetSizeOut: &size,
            parameterSetCountOut: nil, nalUnitHeaderLengthOut: nil) == noErr,
            let pointer else { return nil }
        return Data(bytes: pointer, count: size)
    }
}
