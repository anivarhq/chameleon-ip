package com.anivarhq.chameleon

import android.content.Context
import android.util.AttributeSet
import android.view.SurfaceView

/**
 * The camera's picture at its own shape.
 *
 * A SurfaceView filling the screen stretched the 16:9 picture to the screen's
 * shape. This one measures itself to 16:9 along the long side, whichever way
 * the phone is turned (the camera framework rotates a SurfaceView's picture to
 * match the display), and the layout centres it.
 */
class PreviewView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
) : SurfaceView(context, attrs) {

    override fun onMeasure(widthSpec: Int, heightSpec: Int) {
        val width = MeasureSpec.getSize(widthSpec)
        val height = MeasureSpec.getSize(heightSpec)
        val long = CameraPipeline.WIDTH.toFloat()
        val short = CameraPipeline.HEIGHT.toFloat()
        // Landscape screen: the picture is wider than tall; portrait: taller.
        val aspect = if (width >= height) long / short else short / long

        var w = width
        var h = (width / aspect).toInt()
        if (h > height) {
            h = height
            w = (height * aspect).toInt()
        }
        setMeasuredDimension(w, h)
    }
}
