package com.pocketnas.client.ui.files

/**
 * 纯逻辑：长按多选的选择集（SPEC-M10 §3 单测对象）。
 * 保持插入顺序，便于稳定展示与批量操作。
 */
class SelectionSet {

    private val selected = LinkedHashSet<String>()

    val size: Int get() = selected.size
    val isEmpty: Boolean get() = selected.isEmpty()
    val isNotEmpty: Boolean get() = !isEmpty

    /** 返回切换后该项是否被选中。 */
    fun toggle(path: String): Boolean =
        if (!selected.add(path)) {
            selected.remove(path)
            false
        } else {
            true
        }

    fun select(path: String) = selected.add(path)

    fun contains(path: String): Boolean = path in selected

    fun clear() = selected.clear()

    /** 快照，避免遍历期间被修改。 */
    fun snapshot(): List<String> = selected.toList()

    /** 剔除已不在 [existing] 中的项（列表刷新后调用）。 */
    fun retainAll(existing: Collection<String>) {
        selected.retainAll(existing.toSet())
    }
}
