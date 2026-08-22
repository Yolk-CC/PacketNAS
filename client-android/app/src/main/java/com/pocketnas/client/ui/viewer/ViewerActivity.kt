package com.pocketnas.client.ui.viewer

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.provider.MediaStore
import android.text.format.DateFormat
import android.view.View
import android.widget.ImageButton
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.FileProvider
import androidx.lifecycle.lifecycleScope
import androidx.viewpager2.widget.ViewPager2
import com.google.android.material.bottomsheet.BottomSheetDialog
import com.pocketnas.client.App
import com.pocketnas.client.R
import com.pocketnas.client.data.model.MediaItem
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.Request
import java.io.File
import java.io.OutputStream
import java.util.Date

/** Simple in-memory handoff of the current timeline list to the viewer. */
object ViewerData {
    var items: List<MediaItem> = emptyList()
}

class ViewerActivity : AppCompatActivity() {

    private lateinit var pager: ViewPager2
    private lateinit var adapter: ViewerPagerAdapter
    private lateinit var titleBar: View
    private lateinit var bottomBar: View
    private lateinit var fileName: TextView
    private lateinit var takenTime: TextView
    private var uiVisible = true

    private val items: List<MediaItem> get() = ViewerData.items

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_viewer)

        pager = findViewById(R.id.pager)
        titleBar = findViewById(R.id.viewer_top_bar)
        bottomBar = findViewById(R.id.viewer_bottom_bar)
        fileName = findViewById(R.id.text_file_name)
        takenTime = findViewById(R.id.text_taken_time)

        adapter = ViewerPagerAdapter(api = { App.of(this).apiClient }, onToggleUi = { toggleUi() })
        pager.adapter = adapter
        adapter.submit(items)
        val start = intent.getIntExtra(EXTRA_START, 0).coerceIn(0, (items.size - 1).coerceAtLeast(0))
        pager.setCurrentItem(start, false)
        pager.registerOnPageChangeCallback(object : ViewPager2.OnPageChangeCallback() {
            override fun onPageSelected(position: Int) = updateChrome(items.getOrNull(position))
        })
        updateChrome(items.getOrNull(start))

        findViewById<ImageButton>(R.id.btn_back).setOnClickListener { finish() }
        findViewById<ImageButton>(R.id.btn_info).setOnClickListener { current()?.let(::showDetails) }
        findViewById<ImageButton>(R.id.btn_share).setOnClickListener { current()?.let(::share) }
        findViewById<ImageButton>(R.id.btn_download).setOnClickListener { current()?.let(::download) }
        findViewById<ImageButton>(R.id.btn_delete).setOnClickListener { current()?.let(::confirmDelete) }
    }

    override fun onDestroy() {
        // onViewRecycled is not guaranteed during teardown; release every
        // still-attached page's ExoPlayer here (M15b).
        if (::adapter.isInitialized) adapter.releaseAll()
        super.onDestroy()
    }

    private fun current(): MediaItem? = items.getOrNull(pager.currentItem)

    private fun updateChrome(item: MediaItem?) {
        fileName.text = item?.name ?: ""
        takenTime.text = item?.let {
            DateFormat.format("yyyy-MM-dd HH:mm", Date(it.takenTime * 1000)).toString()
        } ?: ""
    }

    /** Immersive mode: tap toggles system bars + chrome (SPEC-M9 §4). */
    private fun toggleUi() {
        uiVisible = !uiVisible
        titleBar.visibility = if (uiVisible) View.VISIBLE else View.GONE
        bottomBar.visibility = if (uiVisible) View.VISIBLE else View.GONE
        val controller = androidx.core.view.WindowCompat.getInsetsController(window, window.decorView)
        if (uiVisible) {
            controller.show(androidx.core.view.WindowInsetsCompat.Type.systemBars())
        } else {
            controller.hide(androidx.core.view.WindowInsetsCompat.Type.systemBars())
        }
    }

    /** 详情面板（SPEC-M14 §2）：BottomSheet 展示现有元数据，缺字段显示「-」。 */
    private fun showDetails(item: MediaItem) {
        val dialog = BottomSheetDialog(this)
        val sheet = layoutInflater.inflate(R.layout.sheet_media_details, null)
        sheet.findViewById<TextView>(R.id.detail_name).text = MediaDetails.name(item)
        sheet.findViewById<TextView>(R.id.detail_time).text =
            if (item.takenTime > 0) {
                DateFormat.format("yyyy-MM-dd HH:mm:ss", Date(item.takenTime * 1000)).toString()
            } else {
                MediaDetails.PLACEHOLDER
            }
        sheet.findViewById<TextView>(R.id.detail_resolution).text = MediaDetails.resolution(item)
        sheet.findViewById<TextView>(R.id.detail_size).text = MediaDetails.fileSize(item)
        sheet.findViewById<TextView>(R.id.detail_mime).text = MediaDetails.mimeType(item)
        sheet.findViewById<TextView>(R.id.detail_path).text = MediaDetails.path(item)
        dialog.setContentView(sheet)
        dialog.show()
    }

    private fun share(item: MediaItem) = lifecycleScope.launch {
        try {
            val file = cacheMedia(item)
            val uri = FileProvider.getUriForFile(
                this@ViewerActivity, "${packageName}.fileprovider", file,
            )
            val intent = Intent(Intent.ACTION_SEND).apply {
                type = item.mimeType
                putExtra(Intent.EXTRA_STREAM, uri)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
            startActivity(Intent.createChooser(intent, item.name))
        } catch (e: Exception) {
            toast(R.string.error_share)
        }
    }

    /** Downloads to the device gallery via MediaStore (SPEC-M9 §4). */
    private fun download(item: MediaItem) = lifecycleScope.launch {
        try {
            val collection = if (android.os.Build.VERSION.SDK_INT >= 29) {
                if (item.isVideo) {
                    MediaStore.Video.Media.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
                } else {
                    MediaStore.Images.Media.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
                }
            } else if (item.isVideo) {
                MediaStore.Video.Media.EXTERNAL_CONTENT_URI
            } else {
                MediaStore.Images.Media.EXTERNAL_CONTENT_URI
            }
            val values = ContentValues().apply {
                put(MediaStore.MediaColumns.DISPLAY_NAME, item.name)
                put(MediaStore.MediaColumns.MIME_TYPE, item.mimeType)
                if (android.os.Build.VERSION.SDK_INT >= 29) {
                    put(MediaStore.MediaColumns.RELATIVE_PATH, "Pictures/PocketNAS")
                }
            }
            val uri = contentResolver.insert(collection, values)
                ?: error("MediaStore insert failed")
            try {
                contentResolver.openOutputStream(uri)?.use { out -> streamMedia(item, out) }
                    ?: error("MediaStore openOutputStream failed")
            } catch (e: Exception) {
                contentResolver.delete(uri, null, null)
                throw e
            }
            toast(R.string.download_done)
        } catch (e: Exception) {
            toast(R.string.error_download)
        }
    }

    private fun confirmDelete(item: MediaItem) {
        AlertDialog.Builder(this)
            .setMessage(getString(R.string.confirm_delete, item.name))
            .setPositiveButton(android.R.string.ok) { _, _ ->
                lifecycleScope.launch {
                    try {
                        App.of(this@ViewerActivity).apiClient?.deleteFiles(listOf(item.path))
                        toast(R.string.delete_done)
                        finish()
                    } catch (e: Exception) {
                        toast(R.string.error_delete)
                    }
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    /**
     * Streams /api/media/file/<path> into [out] (M15b: no whole-file byte
     * array in memory, same streaming approach as DownloadHelper). The auth
     * header is injected by the shared client's interceptor.
     */
    private suspend fun streamMedia(item: MediaItem, out: OutputStream) = withContext(Dispatchers.IO) {
        val app = App.of(this@ViewerActivity)
        val api = app.apiClient ?: error("no server")
        val url = api.mediaUrl("/api/media/file", item.path)
        val req = Request.Builder().url(url).build()
        app.httpClient.newCall(req).execute().use { resp ->
            if (!resp.isSuccessful) error("HTTP ${resp.code}")
            resp.body?.byteStream()?.use { it.copyTo(out) } ?: error("empty body")
        }
    }

    private suspend fun cacheMedia(item: MediaItem): File = withContext(Dispatchers.IO) {
        val dir = File(cacheDir, "share").apply { mkdirs() }
        val file = File(dir, item.name)
        file.outputStream().use { streamMedia(item, it) }
        file
    }

    private fun toast(res: Int) = Toast.makeText(this, res, Toast.LENGTH_SHORT).show()

    companion object {
        private const val EXTRA_START = "start_index"
        fun intent(context: Context, startIndex: Int): Intent =
            Intent(context, ViewerActivity::class.java).putExtra(EXTRA_START, startIndex)
    }
}
