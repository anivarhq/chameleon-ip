package com.anivarhq.chameleon

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.SurfaceHolder
import android.view.View
import android.view.WindowManager
import android.widget.Button
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import mobile.Mobile
import java.net.Inet4Address
import java.net.NetworkInterface

/**
 * A deliberately plain screen, camera first: turn it on and the picture is
 * the screen. Watching or recording it on another device is an optional
 * button, never something the screen implies you must do to see anything.
 */
class MainActivity : AppCompatActivity() {
    private lateinit var status: TextView
    private lateinit var off: TextView
    private lateinit var address: TextView
    private lateinit var toggle: Button
    private lateinit var share: Button
    private var sharing = false
    private val ui = Handler(Looper.getMainLooper())
    private var streaming = false

    private val askCamera = registerForActivityResult(
        androidx.activity.result.contract.ActivityResultContracts.RequestMultiplePermissions()
    ) { granted ->
        if (granted[Manifest.permission.CAMERA] == true) startStreaming() else
            off.text = getString(R.string.needs_camera)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        // Aiming the camera means looking at the screen; don't let it sleep.
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        status = findViewById(R.id.status)
        off = findViewById(R.id.off)
        address = findViewById(R.id.address)
        toggle = findViewById(R.id.toggle)
        share = findViewById(R.id.share)

        findViewById<PreviewView>(R.id.preview).holder.apply {
            // The camera draws at the encoder's size. Left to the view's size,
            // the surface is one the camera may not support at all, and the
            // picture comes out stretched when it does.
            setFixedSize(CameraPipeline.WIDTH, CameraPipeline.HEIGHT)
            addCallback(object : SurfaceHolder.Callback {
                override fun surfaceCreated(holder: SurfaceHolder) { Preview.surface = holder.surface }
                override fun surfaceChanged(holder: SurfaceHolder, f: Int, w: Int, h: Int) = Unit
                override fun surfaceDestroyed(holder: SurfaceHolder) { Preview.surface = null }
            })
        }

        toggle.setOnClickListener { if (streaming) stopStreaming() else requestAndStart() }
        share.setOnClickListener { showAddress(!sharing) }
        tick()
    }

    private fun requestAndStart() {
        val wanted = buildList {
            add(Manifest.permission.CAMERA)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                add(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
        val missing = wanted.filter {
            ContextCompat.checkSelfPermission(this, it) != PackageManager.PERMISSION_GRANTED
        }
        if (missing.isEmpty()) startStreaming() else askCamera.launch(missing.toTypedArray())
    }

    private fun startStreaming() {
        CameraService.start(this)
        streaming = true
        toggle.setText(R.string.stop)
        off.visibility = View.GONE
        status.visibility = View.VISIBLE
        share.visibility = View.VISIBLE
        address.text = getString(
            R.string.share_text,
            Credentials.USER,
            Credentials.password(this),
            localAddress() ?: "<this device>",
            CameraService.PORT,
        )
    }

    private fun stopStreaming() {
        CameraService.stop(this)
        streaming = false
        toggle.setText(R.string.start)
        off.visibility = View.VISIBLE
        status.visibility = View.GONE
        share.visibility = View.GONE
        showAddress(false)
    }

    private fun showAddress(show: Boolean) {
        sharing = show
        address.visibility = if (show) View.VISIBLE else View.GONE
        share.setText(if (show) R.string.share_hide else R.string.share)
    }

    /** Repeats what the core knows, rather than guessing from this side. */
    private fun tick() {
        // gomobile maps Go's int to a Java long.
        val viewers = runCatching { Mobile.viewers() }.getOrDefault(0L).toInt()
        status.text = if (viewers > 0) {
            resources.getQuantityString(R.plurals.live_watching, viewers, viewers)
        } else {
            getString(R.string.live)
        }
        ui.postDelayed(::tick, 1000)
    }

    private fun localAddress(): String? {
        // Wi-Fi only: never advertise a mobile-data address, which can be
        // reachable from outside the house.
        val wifi = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        if (!wifi.isWifiEnabled) return null
        return NetworkInterface.getNetworkInterfaces().toList()
            .filter { it.isUp && !it.isLoopback }
            .flatMap { it.inetAddresses.toList() }
            .filterIsInstance<Inet4Address>()
            .firstOrNull { it.isSiteLocalAddress }
            ?.hostAddress
    }
}
