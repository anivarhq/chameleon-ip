package com.anivarhq.chameleon

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.graphics.SurfaceTexture
import android.hardware.camera2.CameraCaptureSession
import android.hardware.camera2.CameraCharacteristics
import android.hardware.camera2.CameraDevice
import android.hardware.camera2.CameraManager
import android.hardware.camera2.CaptureRequest
import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaFormat
import android.os.Bundle
import android.os.Handler
import android.os.HandlerThread
import android.util.Log
import android.view.Surface
import androidx.core.content.ContextCompat
import mobile.Mobile
import java.nio.ByteBuffer

/**
 * Camera frames go straight into the hardware encoder's input surface, so the
 * CPU never touches a pixel; only the encoded bytes are copied, once, on their
 * way to the core. Feeding MediaCodec buffer-by-buffer instead is what made
 * the older Android streaming libraries unmaintainable.
 */
class CameraPipeline(
    private val context: Context,
    private val width: Int = WIDTH,
    private val height: Int = HEIGHT,
    private val fps: Int = FPS,
    private val bitrate: Int = BITRATE,
) {
    private var camera: CameraDevice? = null
    private var session: CameraCaptureSession? = null
    private var encoder: MediaCodec? = null
    private var encoderSurface: Surface? = null
    private var thread: HandlerThread? = null
    private var handler: Handler? = null

    /** Set to the preview surface to also show what the camera sees. */
    var previewSurface: Surface? = null

    /**
     * Swaps the preview in or out while streaming.
     *
     * When the activity goes away its surface is released, and a capture
     * session still pointing at it stops delivering frames — the stream dies
     * quietly with the service still running and the notification still up.
     * So the session is rebuilt around whatever surfaces are currently alive;
     * the encoder's own surface never goes anywhere.
     */
    fun updatePreview(surface: Surface?) {
        handler?.post {
            previewSurface = surface
            val device = camera ?: return@post
            runCatching {
                session?.stopRepeating()
                session?.abortCaptures()
            }
            session?.close()
            session = null
            createSession(device)
        }
    }

    private var epochNanos = 0L

    fun start() {
        require(
            ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA)
                == PackageManager.PERMISSION_GRANTED
        ) { "camera permission not granted" }

        thread = HandlerThread("chameleon-camera").also { it.start() }
        handler = Handler(thread!!.looper)

        startEncoder()
        openCamera()

        // The activity hands its preview over as it comes and goes.
        Preview.onChange = { updatePreview(it) }
    }

    fun stop() {
        Preview.onChange = null
        session?.close(); session = null
        camera?.close(); camera = null
        encoder?.let { runCatching { it.stop() }; it.release() }; encoder = null
        encoderSurface?.release(); encoderSurface = null
        thread?.quitSafely(); thread = null; handler = null
    }

    private fun startEncoder() {
        val format = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, width, height).apply {
            setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface)
            setInteger(MediaFormat.KEY_BIT_RATE, bitrate)
            setInteger(MediaFormat.KEY_FRAME_RATE, fps)
            // A keyframe every 2 s bounds how long a joining viewer waits when
            // the encoder cannot produce one on demand.
            setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 2)
        }

        val codec = MediaCodec.createEncoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
        codec.setCallback(object : MediaCodec.Callback() {
            override fun onInputBufferAvailable(codec: MediaCodec, index: Int) = Unit

            override fun onOutputBufferAvailable(codec: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
                try {
                    val buf: ByteBuffer? = codec.getOutputBuffer(index)
                    if (buf != null && info.size > 0) {
                        buf.position(info.offset)
                        buf.limit(info.offset + info.size)
                        val frame = ByteArray(info.size)
                        buf.get(frame)

                        if (epochNanos == 0L) epochNanos = info.presentationTimeUs * 1000
                        val ptsMicros = info.presentationTimeUs - epochNanos / 1000
                        Mobile.pushFrame(frame, ptsMicros)
                    }
                } catch (t: Throwable) {
                    Log.w(TAG, "push failed", t)
                } finally {
                    runCatching { codec.releaseOutputBuffer(index, false) }
                }

                // A viewer just joined: give them a keyframe now, instead of
                // leaving them with a blank picture until the next one.
                if (Mobile.takeKeyframeWanted()) {
                    runCatching {
                        codec.setParameters(Bundle().apply {
                            putInt(MediaCodec.PARAMETER_KEY_REQUEST_SYNC_FRAME, 0)
                        })
                    }
                }
            }

            override fun onError(codec: MediaCodec, e: MediaCodec.CodecException) {
                Log.e(TAG, "encoder error", e)
            }

            override fun onOutputFormatChanged(codec: MediaCodec, format: MediaFormat) {
                Log.i(TAG, "encoder format $format")
            }
        }, handler)

        codec.configure(format, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE)
        encoderSurface = codec.createInputSurface()
        codec.start()
        encoder = codec
    }

    private fun openCamera() {
        val manager = context.getSystemService(Context.CAMERA_SERVICE) as CameraManager
        val id = manager.cameraIdList.firstOrNull { camId ->
            manager.getCameraCharacteristics(camId)
                .get(CameraCharacteristics.LENS_FACING) == CameraCharacteristics.LENS_FACING_BACK
        } ?: manager.cameraIdList.firstOrNull() ?: error("no camera on this device")

        @Suppress("MissingPermission")
        manager.openCamera(id, object : CameraDevice.StateCallback() {
            override fun onOpened(device: CameraDevice) {
                camera = device
                createSession(device)
            }

            override fun onDisconnected(device: CameraDevice) {
                device.close(); camera = null
            }

            override fun onError(device: CameraDevice, error: Int) {
                Log.e(TAG, "camera error $error")
                device.close(); camera = null
            }
        }, handler)
    }

    private fun createSession(device: CameraDevice) {
        val surfaces = listOfNotNull(encoderSurface, previewSurface)
        @Suppress("DEPRECATION")
        device.createCaptureSession(surfaces, object : CameraCaptureSession.StateCallback() {
            override fun onConfigured(configured: CameraCaptureSession) {
                session = configured
                val request = device.createCaptureRequest(CameraDevice.TEMPLATE_RECORD).apply {
                    surfaces.forEach { addTarget(it) }
                    set(CaptureRequest.CONTROL_AE_TARGET_FPS_RANGE, android.util.Range(fps, fps))
                }.build()
                configured.setRepeatingRequest(request, null, handler)
            }

            override fun onConfigureFailed(configured: CameraCaptureSession) {
                Log.e(TAG, "capture session failed")
            }
        }, handler)
    }

    companion object {
        // What the encoder is set to do. The service reports these to NVRs
        // over ONVIF, so they have to be the same numbers.
        const val WIDTH = 1280
        const val HEIGHT = 720
        const val FPS = 15
        const val BITRATE = 2_000_000

        private const val TAG = "ChameleonCamera"
    }
}
