package com.pocketnas.client.ui.people

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import coil.load
import coil.transform.CircleCropTransformation
import com.pocketnas.client.R
import com.pocketnas.client.data.api.ApiClient
import com.pocketnas.client.data.model.Person
import com.pocketnas.client.ui.files.SelectionSet

/** 人物网格：圆形封面 + 姓名/占位名 + 照片数（SPEC-M12 §1）。 */
class PeopleAdapter(
    private val api: () -> ApiClient?,
    private val selection: SelectionSet,
    private val onClick: (Person) -> Unit,
    private val onLongClick: (Person) -> Unit,
) : ListAdapter<Person, PeopleAdapter.PersonVH>(DIFF) {

    var selectionMode = false

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): PersonVH =
        PersonVH(LayoutInflater.from(parent.context).inflate(R.layout.item_person, parent, false))

    override fun onBindViewHolder(holder: PersonVH, position: Int) = holder.bind(getItem(position))

    inner class PersonVH(view: View) : RecyclerView.ViewHolder(view) {
        private val cover: ImageView = view.findViewById(R.id.image_cover)
        private val overlay: View = view.findViewById(R.id.selection_overlay)
        private val name: TextView = view.findViewById(R.id.text_name)
        private val count: TextView = view.findViewById(R.id.text_count)

        fun bind(person: Person) {
            name.text = person.displayName
            count.text = itemView.context.getString(R.string.person_photo_count, person.faceCount)
            val url = person.coverUrl.takeIf { it.isNotEmpty() }?.let { api()?.absolute(it) }
            cover.load(url) {
                placeholder(R.drawable.bg_thumb_placeholder)
                error(R.drawable.bg_thumb_placeholder)
                transformations(CircleCropTransformation())
                memoryCacheKey(person.coverUrl)
            }
            overlay.visibility =
                if (selectionMode && selection.contains(person.id.toString())) View.VISIBLE else View.GONE
            itemView.setOnClickListener { onClick(person) }
            itemView.setOnLongClickListener {
                onLongClick(person)
                true
            }
        }
    }

    companion object {
        private val DIFF = object : DiffUtil.ItemCallback<Person>() {
            override fun areItemsTheSame(a: Person, b: Person): Boolean = a.id == b.id
            override fun areContentsTheSame(a: Person, b: Person): Boolean = a == b
        }
    }
}
