package com.pocketnas.client.ui.timeline

import android.annotation.SuppressLint
import android.content.Intent
import android.os.Bundle
import android.view.ScaleGestureDetector
import android.view.View
import android.widget.Button
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.activity.viewModels
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import androidx.recyclerview.widget.GridLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import com.pocketnas.client.App
import com.pocketnas.client.R
import com.pocketnas.client.ui.server.ServerConnectActivity
import com.pocketnas.client.ui.viewer.ViewerActivity
import com.pocketnas.client.ui.viewer.ViewerData
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

class TimelineActivity : AppCompatActivity() {

    private val viewModel: TimelineViewModel by viewModels()
    private lateinit var adapter: TimelineAdapter
    private lateinit var layoutManager: GridLayoutManager
    private var spanCount = 3

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (App.of(this).apiClient == null) {
            startActivity(Intent(this, ServerConnectActivity::class.java))
            finish()
            return
        }
        setContentView(R.layout.activity_timeline)

        val app = App.of(this)
        val title: TextView = findViewById(R.id.text_server_name)
        val switch: Button = findViewById(R.id.btn_switch_server)
        val empty: TextView = findViewById(R.id.text_empty)
        val progress: ProgressBar = findViewById(R.id.progress)
        val swipe: SwipeRefreshLayout = findViewById(R.id.swipe_refresh)
        val recycler: RecyclerView = findViewById(R.id.recycler_timeline)
        val scroller: FastScroller = findViewById(R.id.fast_scroller)

        adapter = TimelineAdapter(api = { App.of(this).apiClient }) { mediaIndex, _ ->
            ViewerData.items = viewModel.state.value.items
            startActivity(ViewerActivity.intent(this, mediaIndex))
        }
        spanCount = savedInstanceState?.getInt(KEY_SPAN) ?: 3
        adapter.spanCount = spanCount
        layoutManager = GridLayoutManager(this, spanCount).apply {
            spanSizeLookup = object : GridLayoutManager.SpanSizeLookup() {
                override fun getSpanSize(position: Int): Int =
                    if (adapter.getItemViewType(position) == TimelineAdapter.TYPE_HEADER) spanCount else 1
            }
        }
        recycler.layoutManager = layoutManager
        recycler.adapter = adapter
        recycler.setHasFixedSize(true)
        recycler.addItemDecoration(StickyDateDecoration { pos ->
            adapter.currentList.getOrNull(pos)
        })
        recycler.addOnScrollListener(object : RecyclerView.OnScrollListener() {
            override fun onScrolled(rv: RecyclerView, dx: Int, dy: Int) {
                if (dy > 0 && layoutManager.findLastVisibleItemPosition() >= adapter.itemCount - 12) {
                    viewModel.loadMore()
                }
            }
        })

        scroller.attach(recycler)
        scroller.labelProvider = { fraction ->
            bubbleLabel(adapter, fraction)
        }

        swipe.setOnRefreshListener {
            viewModel.refresh()
            swipe.isRefreshing = false
        }
        switch.setOnClickListener {
            startActivity(Intent(this, ServerConnectActivity::class.java))
        }

        attachPinchToZoom(recycler)

        title.text = app.server?.name.orEmpty()
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                viewModel.state.collect { s ->
                    progress.visibility = if (s.loading && s.rows.isEmpty()) View.VISIBLE else View.GONE
                    adapter.submitList(s.rows)
                    if (s.serverName.isNotEmpty()) title.text = s.serverName
                    if (s.unauthorized) {
                        Toast.makeText(this@TimelineActivity, R.string.reauth_required, Toast.LENGTH_LONG).show()
                        startActivity(Intent(this@TimelineActivity, ServerConnectActivity::class.java))
                    }
                    when {
                        s.error != null -> {
                            empty.visibility = View.VISIBLE
                            empty.text = getString(R.string.error_with_detail, s.error)
                        }
                        s.rows.isEmpty() && !s.loading -> {
                            empty.visibility = View.VISIBLE
                            empty.setText(R.string.empty_timeline)
                        }
                        else -> empty.visibility = View.GONE
                    }
                }
            }
        }
    }

    private fun bubbleLabel(adapter: TimelineAdapter, fraction: Float): String {
        val list = adapter.currentList
        if (list.isEmpty()) return ""
        val pos = (list.size * fraction).toInt().coerceIn(0, list.size - 1)
        val row = list[pos]
        val epochSec = when (row) {
            is TimelineRow.Header -> row.epochDay * 86400
            is TimelineRow.Media -> row.item.takenTime
        }
        return Instant.ofEpochSecond(epochSec).atZone(ZoneId.systemDefault())
            .format(DateTimeFormatter.ofPattern("yyyy-MM"))
    }

    /** Pinch to switch grid between 3/4/5 columns (SPEC-M9 §3). */
    @SuppressLint("ClickableViewAccessibility")
    private fun attachPinchToZoom(recycler: RecyclerView) {
        val detector = ScaleGestureDetector(this,
            object : ScaleGestureDetector.SimpleOnScaleGestureListener() {
                override fun onScaleEnd(d: ScaleGestureDetector) {
                    val newSpan = when {
                        d.scaleFactor > 1.15f -> (spanCount - 1).coerceAtLeast(3)
                        d.scaleFactor < 0.87f -> (spanCount + 1).coerceAtMost(5)
                        else -> spanCount
                    }
                    if (newSpan != spanCount) {
                        spanCount = newSpan
                        adapter.spanCount = spanCount
                        layoutManager.spanCount = spanCount
                    }
                }
            })
        recycler.addOnItemTouchListener(object : RecyclerView.SimpleOnItemTouchListener() {
            override fun onInterceptTouchEvent(rv: RecyclerView, e: android.view.MotionEvent): Boolean {
                detector.onTouchEvent(e)
                return false
            }
        })
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putInt(KEY_SPAN, spanCount)
    }

    companion object {
        private const val KEY_SPAN = "span_count"
    }
}
