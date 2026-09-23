package com.anivarhq.chameleon

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.ServiceCompat
import android.util.Log
import mobile.Mobile
import org.json.JSONObject

/**
 * Keeps the camera streaming while the screen is off.
 *
 * It must be started while the app is on screen — Android does not allow a
 * camera foreground service to start from the background, and from Android 15
 * it cannot start itself after a reboot either. After a restart the user taps
 * the notification to resume, and Anivar's camera-offline alert is what tells
 * them it stopped.
 */
class CameraService : Service() {
    private var pipeline: CameraPipeline? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (pipeline != null) return START_NOT_STICKY

        // The typed foreground call is API 29 and the camera type itself is
        // API 30, so the compat helper does the version dance; calling the
        // three-argument form directly crashes older phones, which are the
        // ones most likely to be repurposed as cameras.
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA
        } else {
            0
        }
        ServiceCompat.startForeground(
            this, NOTIFICATION_ID, notification(getString(R.string.streaming)), type,
        )

        // No Wi-Fi lock: the only mode that still does anything needs the
        // screen on, which is exactly when it is not needed, and a running
        // foreground service already keeps the app out of App Standby.

        val config = JSONObject()
            .put("address", ":$PORT")
            .put("onvif_address", ":$ONVIF_PORT")
            .put("path", "main")
            .put("user", Credentials.USER)
            .put("pass", Credentials.password(this))
            .put("uuid", Credentials.deviceUuid(this))
            .put("name", Build.MODEL ?: "Android camera")
            .put("model", "${Build.MANUFACTURER} ${Build.MODEL}")
            .put("serial", Credentials.deviceUuid(this).takeLast(12))
            // What the encoder is really doing: an NVR that reads a made-up
            // frame rate makes its own decisions on it.
            .put("width", CameraPipeline.WIDTH)
            .put("height", CameraPipeline.HEIGHT)
            .put("fps", CameraPipeline.FPS)
            .put("bitrate", CameraPipeline.BITRATE)
        try {
            Mobile.start(config.toString())
            pipeline = CameraPipeline(this).also {
                it.previewSurface = Preview.surface
                it.start()
            }
        } catch (t: Throwable) {
            Log.e(TAG, "could not start", t)
            stopSelf()
            return START_NOT_STICKY
        }
        // Not sticky: Android would restart this service with the app in the
        // background, and a camera service started from the background is
        // refused outright from Android 14. The user taps the notification.
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        pipeline?.stop(); pipeline = null
        runCatching { Mobile.stop() }
        super.onDestroy()
    }

    private fun notification(text: String): Notification {
        val manager = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            manager.createNotificationChannel(
                NotificationChannel(CHANNEL, getString(R.string.app_name), NotificationManager.IMPORTANCE_LOW)
            )
        }
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return Notification.Builder(this, CHANNEL)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(text)
            .setSmallIcon(android.R.drawable.presence_video_online)
            .setContentIntent(open)
            .setOngoing(true)
            .build()
    }

    companion object {
        const val PORT = 8554
        const val ONVIF_PORT = 8000
        private const val CHANNEL = "chameleon"
        private const val NOTIFICATION_ID = 1
        private const val TAG = "ChameleonService"

        fun start(context: Context) =
            context.startForegroundService(Intent(context, CameraService::class.java))

        fun stop(context: Context) =
            context.stopService(Intent(context, CameraService::class.java))
    }
}

/**
 * Where the activity parks its preview surface for the service to draw on.
 *
 * Setting it tells a running pipeline to rebuild its capture session, because
 * a surface that has been released cannot stay in the session's target list.
 */
object Preview {
    @Volatile
    var onChange: ((android.view.Surface?) -> Unit)? = null

    @Volatile
    var surface: android.view.Surface? = null
        set(value) {
            field = value
            onChange?.invoke(value)
        }
}
