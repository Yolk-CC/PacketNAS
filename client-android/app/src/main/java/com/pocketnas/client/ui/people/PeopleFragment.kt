package com.pocketnas.client.ui.people

import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.activity.addCallback
import androidx.appcompat.app.AlertDialog
import androidx.fragment.app.Fragment
import androidx.fragment.app.viewModels
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import androidx.recyclerview.widget.GridLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import com.pocketnas.client.App
import com.pocketnas.client.R
import com.pocketnas.client.data.model.Person
import com.pocketnas.client.ui.files.SelectionSet
import com.pocketnas.client.ui.server.ServerConnectActivity
import kotlinx.coroutines.launch

/**
 * 人物 tab（SPEC-M12 §1-§3）：人物网格 + 下拉刷新 + 长按多选合并/命名。
 * faces 不可用时显示服务端原因与引导文案。
 */
class PeopleFragment : Fragment() {

    private val viewModel: PeopleViewModel by viewModels()
    private val selection = SelectionSet()
    private lateinit var adapter: PeopleAdapter
    private lateinit var selectionBar: LinearLayout
    private lateinit var empty: TextView
    private lateinit var progress: ProgressBar
    private lateinit var swipe: SwipeRefreshLayout

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View = inflater.inflate(R.layout.fragment_people, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        selectionBar = view.findViewById(R.id.selection_bar)
        empty = view.findViewById(R.id.text_empty)
        progress = view.findViewById(R.id.progress)
        swipe = view.findViewById(R.id.swipe_refresh)
        val recycler: RecyclerView = view.findViewById(R.id.recycler_people)

        adapter = PeopleAdapter(
            api = { App.of(requireContext()).apiClient },
            selection = selection,
            onClick = ::onPersonClick,
            onLongClick = ::onPersonLongClick,
        )
        recycler.layoutManager = GridLayoutManager(requireContext(), SPAN_COUNT)
        recycler.adapter = adapter

        swipe.setOnRefreshListener {
            viewModel.reload()
            swipe.isRefreshing = false
        }
        view.findViewById<Button>(R.id.btn_merge).setOnClickListener { confirmMergeSelected() }
        view.findViewById<Button>(R.id.btn_name_person).setOnClickListener {
            selectedPersons().singleOrNull()?.let { showNameDialog(it) }
        }
        view.findViewById<Button>(R.id.btn_clear_selection).setOnClickListener { exitSelectionMode() }

        // 返回键先退多选
        requireActivity().onBackPressedDispatcher.addCallback(viewLifecycleOwner) {
            if (selection.isNotEmpty) {
                exitSelectionMode()
            } else {
                isEnabled = false
                requireActivity().onBackPressedDispatcher.onBackPressed()
            }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.STARTED) {
                viewModel.state.collect { s ->
                    progress.visibility = if (s.loading && s.persons.isEmpty()) View.VISIBLE else View.GONE
                    adapter.submitList(s.persons)
                    selection.retainAll(s.persons.map { it.id.toString() })
                    updateSelectionBar()
                    if (s.unauthorized) {
                        Toast.makeText(requireContext(), R.string.reauth_required, Toast.LENGTH_LONG).show()
                        startActivity(Intent(requireContext(), ServerConnectActivity::class.java))
                    }
                    when {
                        s.unavailable -> {
                            empty.visibility = View.VISIBLE
                            empty.text = getString(R.string.faces_unavailable_reason, s.reason) +
                                "\n" + getString(R.string.faces_unavailable_hint)
                        }
                        s.error != null -> {
                            empty.visibility = View.VISIBLE
                            empty.text = getString(R.string.error_with_detail, s.error)
                        }
                        s.persons.isEmpty() && !s.loading -> {
                            empty.visibility = View.VISIBLE
                            empty.setText(R.string.empty_people)
                        }
                        else -> empty.visibility = View.GONE
                    }
                    s.opResult?.let { onOpResult(it) }
                }
            }
        }
    }

    private fun onPersonClick(person: Person) {
        if (selection.isNotEmpty) {
            selection.toggle(person.id.toString())
            adapter.notifyDataSetChanged()
            updateSelectionBar()
            return
        }
        startActivity(PersonPhotosActivity.intent(requireContext(), person))
    }

    private fun onPersonLongClick(person: Person) {
        selection.toggle(person.id.toString())
        adapter.selectionMode = selection.isNotEmpty
        adapter.notifyDataSetChanged()
        updateSelectionBar()
    }

    private fun selectedPersons(): List<Person> {
        val byId = viewModel.state.value.persons.associateBy { it.id.toString() }
        return selection.snapshot().mapNotNull { byId[it] }
    }

    private fun updateSelectionBar() {
        selectionBar.visibility = if (selection.isNotEmpty) View.VISIBLE else View.GONE
        selectionBar.findViewById<TextView>(R.id.text_selection_count).text =
            getString(R.string.selection_count, selection.size)
        // 合并需恰好选中两个；命名仅支持单选
        selectionBar.findViewById<Button>(R.id.btn_merge).isEnabled = selection.size == 2
        selectionBar.findViewById<Button>(R.id.btn_name_person).isEnabled = selection.size == 1
        adapter.selectionMode = selection.isNotEmpty
    }

    private fun exitSelectionMode() {
        selection.clear()
        adapter.selectionMode = false
        adapter.notifyDataSetChanged()
        updateSelectionBar()
    }

    private fun showNameDialog(person: Person) {
        val ctx = requireContext()
        val edit = EditText(ctx).apply {
            inputType = InputType.TYPE_CLASS_TEXT
            setHint(R.string.hint_person_name)
            setText(person.name)
            setSelection(person.name.length)
        }
        val pad = (16 * resources.displayMetrics.density).toInt()
        val frame = android.widget.FrameLayout(ctx).apply {
            setPadding(pad, pad / 2, pad, 0)
            addView(edit)
        }
        AlertDialog.Builder(ctx)
            .setTitle(R.string.name_person_title)
            .setView(frame)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val name = edit.text.toString().trim()
                if (name.isEmpty()) {
                    Toast.makeText(ctx, R.string.error_name_empty, Toast.LENGTH_SHORT).show()
                } else {
                    viewModel.renamePerson(person.id, name)
                    exitSelectionMode()
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    /** 合并方向：后选中的（from）合并进先选中的（to）（SPEC-M12 §2）。 */
    private fun confirmMergeSelected() {
        val selected = selectedPersons()
        if (selected.size != 2) return
        val to = selected[0]
        val from = selected[1]
        AlertDialog.Builder(requireContext())
            .setMessage(getString(R.string.confirm_merge_persons, from.displayName, to.displayName))
            .setPositiveButton(R.string.btn_merge) { _, _ ->
                viewModel.merge(from.id, to.id)
                exitSelectionMode()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun onOpResult(result: PeopleViewModel.OpResult) {
        viewModel.consumeOpResult()
        when (result) {
            is PeopleViewModel.OpResult.Renamed ->
                Toast.makeText(requireContext(), R.string.rename_person_done, Toast.LENGTH_SHORT).show()
            is PeopleViewModel.OpResult.Merged ->
                Toast.makeText(requireContext(), R.string.merge_done, Toast.LENGTH_SHORT).show()
            is PeopleViewModel.OpResult.OpFailed ->
                Toast.makeText(
                    requireContext(),
                    getString(R.string.op_failed, result.message),
                    Toast.LENGTH_LONG,
                ).show()
        }
    }

    companion object {
        private const val SPAN_COUNT = 3
    }
}
