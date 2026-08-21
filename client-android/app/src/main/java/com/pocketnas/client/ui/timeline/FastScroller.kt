package com.pocketnas.client.ui.timeline

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.RectF
import android.util.AttributeSet
import android.view.MotionEvent
import android.view.View
import androidx.recyclerview.widget.RecyclerView

/**
 * Minimal fast scroller (SPEC-M9 §3): a draggable track on the right edge;
 * while dragging it shows a bubble with the year-month of the target row.
 */
class FastScroller @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
) : View(context, attrs) {

    private val handlePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = 0x66000000 }
    private val bubblePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = 0xDD333333.toInt() }
    private val bubbleTextPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = 0xFFFFFFFF.toInt()
        textSize = 34f
        textAlign = Paint.Align.CENTER
        isFakeBoldText = true
    }
    private val bubbleRect = RectF()

    private var recyclerView: RecyclerView? = null
    private var handleFraction = 0f
    private var dragging = false
    private var bubbleText: String? = null

    /** Supplies the bubble label (e.g. "2024-08") for a scroll fraction. */
    var labelProvider: ((fraction: Float) -> String)? = null

    fun attach(recyclerView: RecyclerView) {
        this.recyclerView = recyclerView
        recyclerView.addOnScrollListener(object : RecyclerView.OnScrollListener() {
            override fun onScrolled(rv: RecyclerView, dx: Int, dy: Int) {
                if (dragging) return
                val offset = rv.computeVerticalScrollOffset()
                val range = rv.computeVerticalScrollRange() - rv.computeVerticalScrollExtent()
                handleFraction = if (range > 0) offset.toFloat() / range else 0f
                invalidate()
            }
        })
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val handleH = height * 0.12f
        val top = (height - handleH) * handleFraction
        canvas.drawRoundRect(
            RectF(width * 0.35f, top, width * 0.85f, top + handleH),
            16f, 16f, handlePaint,
        )
        val label = bubbleText
        if (dragging && !label.isNullOrEmpty()) {
            val bw = 170f
            val bh = 88f
            bubbleRect.set(width - bw - 24f, height / 2f - bh / 2f, width - 24f, height / 2f + bh / 2f)
            canvas.drawRoundRect(bubbleRect, 20f, 20f, bubblePaint)
            canvas.drawText(label, bubbleRect.centerX(), bubbleRect.centerY() + 12f, bubbleTextPaint)
        }
    }

    @SuppressLint("ClickableViewAccessibility")
    override fun onTouchEvent(event: MotionEvent): Boolean {
        val rv = recyclerView ?: return false
        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                dragging = true
                parent?.requestDisallowInterceptTouchEvent(true)
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                dragging = false
                bubbleText = null
                parent?.requestDisallowInterceptTouchEvent(false)
                invalidate()
                return true
            }
        }
        if (dragging) {
            val fraction = (event.y / height).coerceIn(0f, 1f)
            handleFraction = fraction
            val range = rv.computeVerticalScrollRange() - rv.computeVerticalScrollExtent()
            if (range > 0) {
                val target = (range * fraction).toInt()
                rv.scrollBy(0, target - rv.computeVerticalScrollOffset())
            }
            bubbleText = labelProvider?.invoke(fraction)
            invalidate()
            return true
        }
        return super.onTouchEvent(event)
    }
}
