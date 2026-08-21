package com.pocketnas.client.ui.files

import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.HorizontalScrollView
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.activity.addCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AlertDialog
import androidx.fragment.app.Fragment
import androidx.fragment.app.viewModels
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import com.pocketnas.client.App
import com.pocketnas.client.R
import com.pocketnas.client.data.model.FileInfo
import com.pocketnas.client.data.model.MediaItem
import com.pocketnas.client.ui.server.ServerConnectActivity
import com.pocketnas.client.ui.viewer.ViewerActivity
import com.pocketnas.client.ui.viewer.ViewerData
import kotlinx.coroutines.launch
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.RequestBody
import okio.buffer
import okio.source

/** 文件 tab：面包屑 + 目录浏览 + 长按多选（SPEC-M10 §2）。 */
class FilesFragment : Fragment() {

    private val viewModel: FilesViewModel by viewModels()
    private val selection = SelectionSet()
    private lateinit var adapter: FilesAdapter
    private lateinit var breadcrumb: LinearLayout
    private lateinit var breadcrumbScroll: HorizontalScrollView
    private lateinit var selectionBar: LinearLayout
    private lateinit var empty: TextView
    private lateinit var progress: ProgressBar
    private lateinit var swipe: SwipeRefreshLayout

    private val pickFile =
        registerForActivityResult(ActivityResultContracts.GetContent()) { uri ->
            if (uri == null) return@registerForActivityResult
            val ctx = context ?: return@registerForActivityResult
            val api = App.of(ctx).apiClient ?: return@registerForActivityResult
            val name = queryDisplayName(uri) ?: "upload.bin"
            lifecycleScope.launch {
                try {
                    val mime = ctx.contentResolver.getType(uri)
                    val body = object : RequestBody() {
                        override fun contentType() = mime?.toMediaTypeOrNull()
                        override fun writeTo(sink: okio.BufferedSink) {
                            ctx.contentResolver.openInputStream(uri)!!.source().buffer()
                                .use { sink.writeAll(it) }
                        }
                    }
                    api.upload(viewModel.pathStack.path, name, body)
                    Toast.makeText(ctx, getString(R.string.upload_done, name), Toast.LENGTH_SHORT).show()
                    viewModel.reload()
                } catch (e: Exception) {
                    Toast.makeText(ctx, getString(R.string.op_failed, e.message), Toast.LENGTH_LONG).show()
                }
            }
        }

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View = inflater.inflate(R.layout.fragment_files, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        breadcrumb = view.findViewById(R.id.breadcrumb)
        breadcrumbScroll = view.findViewById(R.id.breadcrumb_scroll)
        selectionBar = view.findViewById(R.id.selection_bar)
        empty = view.findViewById(R.id.text_empty)
        progress = view.findViewById(R.id.progress)
        swipe = view.findViewById(R.id.swipe_refresh)
        val recycler: RecyclerView = view.findViewById(R.id.recycler_files)

        adapter = FilesAdapter(selection, onClick = ::onItemClick, onLongClick = ::onItemLongClick)
        recycler.layoutManager = LinearLayoutManager(requireContext())
        recycler.adapter = adapter

        swipe.setOnRefreshListener {
            viewModel.reload()
            swipe.isRefreshing = false
        }
        view.findViewById<Button>(R.id.btn_mkdir).setOnClickListener { showMkdirDialog() }
        view.findViewById<Button>(R.id.btn_upload).setOnClickListener { pickFile.launch("*/*") }
        view.findViewById<Button>(R.id.btn_download).setOnClickListener { downloadSelected() }
        view.findViewById<Button>(R.id.btn_rename).setOnClickListener {
            selection.snapshot().singleOrNull()?.let { showRenameDialog(it) }
        }
        view.findViewById<Button>(R.id.btn_delete).setOnClickListener { confirmDeleteSelected() }
        view.findViewById<Button>(R.id.btn_clear_selection).setOnClickListener { exitSelectionMode() }

        // 返回键：先退多选，再退目录层级（SPEC-M10 §2 面包屑逐级回退）
        requireActivity().onBackPressedDispatcher.addCallback(viewLifecycleOwner) {
            when {
                selection.isNotEmpty -> exitSelectionMode()
                viewModel.navigateUp() -> Unit
                else -> {
                    isEnabled = false
                    requireActivity().onBackPressedDispatcher.onBackPressed()
                }
            }
        }

        renderBreadcrumb()
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.STARTED) {
                viewModel.state.collect { s ->
                    progress.visibility = if (s.loading && s.items.isEmpty()) View.VISIBLE else View.GONE
                    adapter.submitList(s.items)
                    // 刷新后剔除已不存在的选中项
                    selection.retainAll(s.items.map { it.path })
                    updateSelectionBar()
                    if (s.unauthorized) {
                        Toast.makeText(requireContext(), R.string.reauth_required, Toast.LENGTH_LONG).show()
                        startActivity(Intent(requireContext(), ServerConnectActivity::class.java))
                    }
                    when {
                        s.error != null -> {
                            empty.visibility = View.VISIBLE
                            empty.text = getString(R.string.error_with_detail, s.error)
                        }
                        s.items.isEmpty() && !s.loading -> {
                            empty.visibility = View.VISIBLE
                            empty.setText(R.string.empty_files)
                        }
                        else -> empty.visibility = View.GONE
                    }
                    s.opResult?.let { onOpResult(it) }
                }
            }
        }
    }

    private fun onItemClick(item: FileInfo) {
        if (selection.isNotEmpty) {
            selection.toggle(item.path)
            adapter.notifyDataSetChanged()
            updateSelectionBar()
            return
        }
        when {
            item.isDir -> {
                viewModel.enterDir(item.name)
                renderBreadcrumb()
            }
            item.isMedia -> openViewer(item)
            else -> Toast.makeText(requireContext(), R.string.file_no_viewer, Toast.LENGTH_SHORT).show()
        }
    }

    private fun onItemLongClick(item: FileInfo) {
        selection.toggle(item.path)
        adapter.selectionMode = selection.isNotEmpty
        adapter.notifyDataSetChanged()
        updateSelectionBar()
    }

    /** 仅把当前目录的媒体项传给现有查看器（SPEC-M10 §2）。 */
    private fun openViewer(item: FileInfo) {
        val media = viewModel.state.value.items.filter { it.isMedia }.map { f ->
            MediaItem(
                path = f.path,
                name = f.name,
                mimeType = f.mimeType,
                takenTime = f.modified,
            )
        }
        val index = media.indexOfFirst { it.path == item.path }
        if (index < 0) return
        ViewerData.items = media
        startActivity(ViewerActivity.intent(requireContext(), index))
    }

    private fun renderBreadcrumb() {
        breadcrumb.removeAllViews()
        val stack = viewModel.pathStack
        addCrumb(getString(R.string.files_root), 0, stack.isRoot)
        stack.breadcrumbs().forEachIndexed { i, (label, _) ->
            addCrumb(label, i + 1, i == stack.depth - 1)
        }
        breadcrumbScroll.post { breadcrumbScroll.fullScroll(View.FOCUS_RIGHT) }
    }

    private fun addCrumb(label: String, depth: Int, isLast: Boolean) {
        if (breadcrumb.childCount > 0) {
            breadcrumb.addView(TextView(requireContext()).apply {
                text = " › "
                textSize = 16f
            })
        }
        breadcrumb.addView(TextView(requireContext()).apply {
            text = label
            textSize = 16f
            setTypeface(typeface, if (isLast) android.graphics.Typeface.BOLD else android.graphics.Typeface.NORMAL)
            if (!isLast) {
                setTextColor(context.getColor(com.google.android.material.R.color.material_blue_grey_800))
                setOnClickListener {
                    viewModel.navigateTo(depth)
                    renderBreadcrumb()
                }
            }
        })
    }

    private fun updateSelectionBar() {
        selectionBar.visibility = if (selection.isNotEmpty) View.VISIBLE else View.GONE
        selectionBar.findViewById<TextView>(R.id.text_selection_count).text =
            getString(R.string.selection_count, selection.size)
        // 重命名仅支持单选
        selectionBar.findViewById<Button>(R.id.btn_rename).isEnabled = selection.size == 1
        adapter.selectionMode = selection.isNotEmpty
    }

    private fun exitSelectionMode() {
        selection.clear()
        adapter.selectionMode = false
        adapter.notifyDataSetChanged()
        updateSelectionBar()
    }

    private fun showMkdirDialog() {
        inputDialog(R.string.mkdir_title, R.string.hint_folder_name) { name ->
            viewModel.mkdir(name)
        }
    }

    private fun showRenameDialog(path: String) {
        inputDialog(R.string.rename_title, R.string.hint_new_name, PathStack.baseName(path)) { name ->
            viewModel.rename(path, name)
            exitSelectionMode()
        }
    }

    private fun inputDialog(titleRes: Int, hintRes: Int, preset: String = "", onOk: (String) -> Unit) {
        val ctx = requireContext()
        val edit = EditText(ctx).apply {
            inputType = InputType.TYPE_CLASS_TEXT
            setHint(hintRes)
            setText(preset)
            setSelection(preset.length)
        }
        val pad = (16 * resources.displayMetrics.density).toInt()
        val frame = android.widget.FrameLayout(ctx).apply {
            setPadding(pad, pad / 2, pad, 0)
            addView(edit)
        }
        AlertDialog.Builder(ctx)
            .setTitle(titleRes)
            .setView(frame)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val name = edit.text.toString().trim().trim('/')
                when {
                    name.isEmpty() -> toast(R.string.error_name_empty)
                    name == "." || name == ".." || name.contains('/') ->
                        toast(R.string.error_name_invalid)
                    else -> onOk(name)
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun confirmDeleteSelected() {
        val paths = selection.snapshot()
        AlertDialog.Builder(requireContext())
            .setMessage(getString(R.string.confirm_delete_count, paths.size))
            .setPositiveButton(R.string.btn_delete) { _, _ ->
                viewModel.delete(paths)
                exitSelectionMode()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun downloadSelected() {
        val ctx = context ?: return
        val api = App.of(ctx).apiClient ?: return
        val byPath = viewModel.state.value.items.associateBy { it.path }
        val paths = selection.snapshot()
        exitSelectionMode()
        lifecycleScope.launch {
            var ok = 0
            var failed = 0
            for (p in paths) {
                try {
                    DownloadHelper.download(ctx, api, p, isDir = byPath[p]?.isDir == true)
                    ok++
                } catch (e: Exception) {
                    failed++
                }
            }
            Toast.makeText(
                ctx,
                getString(R.string.download_result, ok, failed),
                Toast.LENGTH_LONG
            ).show()
        }
    }

    private fun onOpResult(result: FilesViewModel.OpResult) {
        viewModel.consumeOpResult()
        when (result) {
            is FilesViewModel.OpResult.MkdirDone ->
                toast(R.string.mkdir_done)
            is FilesViewModel.OpResult.Renamed ->
                toast(R.string.rename_done)
            is FilesViewModel.OpResult.Deleted ->
                toast(R.string.delete_done)
            is FilesViewModel.OpResult.Uploaded ->
                toast(R.string.upload_done, result.count.toString())
            is FilesViewModel.OpResult.OpFailed ->
                Toast.makeText(requireContext(), getString(R.string.op_failed, result.message), Toast.LENGTH_LONG).show()
        }
    }

    private fun queryDisplayName(uri: android.net.Uri): String? =
        requireContext().contentResolver.query(uri, null, null, null, null)?.use { c ->
            val i = c.getColumnIndex(android.provider.OpenableColumns.DISPLAY_NAME)
            if (i >= 0 && c.moveToFirst()) c.getString(i) else null
        }

    private fun toast(res: Int, vararg args: Any = emptyArray()) =
        Toast.makeText(requireContext(), getString(res, *args), Toast.LENGTH_SHORT).show()
}
