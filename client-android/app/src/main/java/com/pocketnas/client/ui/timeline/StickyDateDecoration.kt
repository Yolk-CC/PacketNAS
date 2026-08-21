package com.pocketnas.client.ui.timeline

import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Rect
import android.view.View
import androidx.recyclerview.widget.RecyclerView

/**
 * Sticky date-header decoration (SPEC-M9 §3), self-implemented.
 * Headers are also rendered as inline list rows by the adapter; this
 * decoration draws a pinned copy at the top once the inline header of the
 * current section has scrolled off screen.
 */
class StickyDateDecoration(
    private val rowAt: (position: Int) -> TimelineRow?,
) : RecyclerView.ItemDecoration() {

    private val bgPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = 0xFFFAFAF7.toInt() }
    private val textPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = 0xFF333333.toInt()
        textSize = 30f
        isFakeBoldText = true
    }
    private val textBounds = Rect()
    private val headerHeight = 72

    /** Position of the header row that opens the section containing [pos]. */
    private fun sectionHeaderPosition(pos: Int, adapterCount: Int): Int {
        var p = pos
        while (p >= 0) {
            if (rowAt(p) is TimelineRow.Header) return p
            p--
        }
        return -1
    }

    override fun onDrawOver(c: Canvas, parent: RecyclerView, state: RecyclerView.State) {
        val adapter = parent.adapter ?: return
        val firstChild = parent.getChildAt(0) ?: return
        val firstPos = parent.getChildAdapterPosition(firstChild)
        if (firstPos == RecyclerView.NO_POSITION) return
        val headerPos = sectionHeaderPosition(firstPos, adapter.itemCount)
        if (headerPos < 0) return
        // Inline header still visible → no pinned copy needed.
        if (headerPos >= firstPos && firstChild.top >= 0 && headerPos == firstPos) return
        val header = rowAt(headerPos) as? TimelineRow.Header ?: return
        // When the next section's header pushes up, offset the pinned header.
        var top = 0f
        for (i in 0 until parent.childCount) {
            val child = parent.getChildAt(i)
            val pos = parent.getChildAdapterPosition(child)
            if (pos != headerPos && rowAt(pos) is TimelineRow.Header) {
                if (child.top in 0 until headerHeight) {
                    top = (child.top - headerHeight).toFloat()
                }
                break
            }
        }
        val left = parent.paddingLeft.toFloat()
        val right = (parent.width - parent.paddingRight).toFloat()
        c.save()
        c.clipRect(left, 0f, right, headerHeight.toFloat())
        c.translate(0f, top)
        c.drawRect(left, 0f, right, headerHeight.toFloat(), bgPaint)
        val text = header.label
        textPaint.getTextBounds(text, 0, text.length, textBounds)
        val y = headerHeight / 2f + textBounds.height() / 2f
        c.drawText(text, left + 24f, y, textPaint)
        c.restore()
    }

    override fun getItemOffsets(
        outRect: Rect, view: View, parent: RecyclerView, state: RecyclerView.State,
    ) {
        // No extra offsets: headers are full-span inline rows.
    }
}
