package com.anivarhq.chameleon

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
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
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (pipeline != null) return START_STICKY

        startForeground(
            NOTIFICATION_ID,
            notification(getString(R.string.streaming)),
            ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA,
        )

        // Wi-Fi must not doze off while the screen is dark, or viewers stall.
        val wifi = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        @Suppress("DEPRECATION")
        wifiLock = wifi.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "chameleon").apply {
            setReferenceCounted(false)
            acquire()
        }

        val config = JSONObject()
            .put("address", ":$PORT")
            .put("path", "main")
            .put("user", Credentials.USER)
            .put("pass", Credentials.password(this))
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
        return START_STICKY
    }

    override fun onDestroy() {
        pipeline?.stop(); pipeline = null
        runCatching { Mobile.stop() }
        wifiLock?.let { if (it.isHeld) it.release() }; wifiLock = null
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
        private const val CHANNEL = "chameleon"
        private const val NOTIFICATION_ID = 1
        private const val TAG = "ChameleonService"

        fun start(context: Context) =
            context.startForegroundService(Intent(context, CameraService::class.java))

        fun stop(context: Context) =
            context.stopService(Intent(context, CameraService::class.java))
    }
}

/** Where the activity parks its preview surface for the service to draw on. */
object Preview {
    @Volatile
    var surface: android.view.Surface? = null
}
