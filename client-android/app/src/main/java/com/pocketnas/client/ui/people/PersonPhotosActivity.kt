package com.pocketnas.client.ui.people

import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.lifecycleScope
import androidx.recyclerview.widget.GridLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.pocketnas.client.App
import com.pocketnas.client.R
import com.pocketnas.client.data.api.ApiException
import com.pocketnas.client.data.model.Person
import com.pocketnas.client.ui.timeline.TimelineAdapter
import com.pocketnas.client.ui.timeline.TimelineRow
import com.pocketnas.client.ui.viewer.ViewerActivity
import com.pocketnas.client.ui.viewer.ViewerData
import kotlinx.coroutines.launch

/**
 * 某人物的全部照片（SPEC-M12 §1）：复用时间线 3 列网格，点击进现有 viewer。
 * 标题 = 人物名，右上角"命名"。
 */
class PersonPhotosActivity : AppCompatActivity() {

    private lateinit var adapter: TimelineAdapter
    private lateinit var title: TextView
    private lateinit var empty: TextView
    private lateinit var progress: ProgressBar

    private var personId: Long = -1
    private var personName: String = ""

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_person_photos)

        personId = intent.getLongExtra(EXTRA_PERSON_ID, -1)
        personName = intent.getStringExtra(EXTRA_PERSON_NAME).orEmpty()
        if (personId < 0) {
            finish()
            return
        }

        title = findViewById(R.id.text_person_name)
        empty = findViewById(R.id.text_empty)
        progress = findViewById(R.id.progress)
        val recycler: RecyclerView = findViewById(R.id.recycler_photos)

        title.text = personName
        adapter = TimelineAdapter(api = { App.of(this).apiClient }) { mediaIndex, _ ->
            ViewerData.items = adapter.currentList.mapNotNull {
                (it as? TimelineRow.Media)?.item
            }
            startActivity(ViewerActivity.intent(this, mediaIndex))
        }
        adapter.spanCount = SPAN_COUNT
        recycler.layoutManager = GridLayoutManager(this, SPAN_COUNT)
        recycler.adapter = adapter

        findViewById<Button>(R.id.btn_back).setOnClickListener { finish() }
        findViewById<Button>(R.id.btn_name_person).setOnClickListener { showNameDialog() }

        load()
    }

    private fun load() {
        val api = App.of(this).apiClient ?: return
        progress.visibility = View.VISIBLE
        lifecycleScope.launch {
            try {
                val resp = api.personPhotos(personId)
                progress.visibility = View.GONE
                val rows = resp.items.map { TimelineRow.Media(it) }
                adapter.submitList(rows)
                empty.visibility = if (rows.isEmpty()) View.VISIBLE else View.GONE
            } catch (e: ApiException) {
                progress.visibility = View.GONE
                empty.visibility = View.VISIBLE
                empty.text = getString(R.string.error_with_detail, e.message)
            } catch (e: Exception) {
                progress.visibility = View.GONE
                empty.visibility = View.VISIBLE
                empty.text = getString(R.string.error_with_detail, e.message)
            }
        }
    }

    private fun showNameDialog() {
        val edit = EditText(this).apply {
            inputType = InputType.TYPE_CLASS_TEXT
            setHint(R.string.hint_person_name)
            setText(personName)
            setSelection(personName.length)
        }
        val pad = (16 * resources.displayMetrics.density).toInt()
        val frame = android.widget.FrameLayout(this).apply {
            setPadding(pad, pad / 2, pad, 0)
            addView(edit)
        }
        AlertDialog.Builder(this)
            .setTitle(R.string.name_person_title)
            .setView(frame)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val name = edit.text.toString().trim()
                if (name.isEmpty()) {
                    Toast.makeText(this, R.string.error_name_empty, Toast.LENGTH_SHORT).show()
                } else {
                    rename(name)
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun rename(name: String) {
        val api = App.of(this).apiClient ?: return
        lifecycleScope.launch {
            try {
                api.renamePerson(personId, name)
                personName = name
                title.text = name
                Toast.makeText(this@PersonPhotosActivity, R.string.rename_person_done, Toast.LENGTH_SHORT).show()
            } catch (e: Exception) {
                Toast.makeText(
                    this@PersonPhotosActivity,
                    getString(R.string.op_failed, e.message),
                    Toast.LENGTH_LONG,
                ).show()
            }
        }
    }

    companion object {
        private const val EXTRA_PERSON_ID = "person_id"
        private const val EXTRA_PERSON_NAME = "person_name"
        private const val SPAN_COUNT = 3

        fun intent(context: Context, person: Person): Intent =
            Intent(context, PersonPhotosActivity::class.java)
                .putExtra(EXTRA_PERSON_ID, person.id)
                .putExtra(EXTRA_PERSON_NAME, person.displayName)
    }
}
