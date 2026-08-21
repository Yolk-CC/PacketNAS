package com.pocketnas.client.ui.files

/**
 * 纯逻辑：文件浏览的路径栈（SPEC-M10 §3 单测对象）。
 * 路径为 root-relative，"/" 表示共享伪目录根。片段不含 "/"，
 * 中文/空格等字符原样保存（URL 编码由 ApiClient 负责）。
 */
class PathStack(initial: String = "/") {

    private val segments: MutableList<String> = split(initial)

    /** 当前路径，root 为 "/"。 */
    val path: String get() = join(segments)

    val isRoot: Boolean get() = segments.isEmpty()

    val depth: Int get() = segments.size

    /** 进入子目录。 */
    fun push(name: String) {
        val clean = name.trim().trim('/')
        require(clean.isNotBlank() && clean != "." && clean != "..") { "invalid dir name: $name" }
        segments.add(clean)
    }

    /** 回退一级，root 时返回 false。 */
    fun pop(): Boolean {
        if (segments.isEmpty()) return false
        segments.removeAt(segments.size - 1)
        return true
    }

    /** 回退到指定深度（0 = root）。越界时 clamp。 */
    fun popTo(depth: Int) {
        val target = depth.coerceIn(0, segments.size)
        while (segments.size > target) segments.removeAt(segments.size - 1)
    }

    /** 面包屑：每级 (显示名, 该级路径)，root 不在列表中（由 UI 加"共享"）。 */
    fun breadcrumbs(): List<Pair<String, String>> {
        val out = ArrayList<Pair<String, String>>(segments.size)
        for (i in segments.indices) {
            out.add(segments[i] to join(segments.subList(0, i + 1)))
        }
        return out
    }

    /** 当前路径下的子项完整路径。 */
    fun child(name: String): String = join(segments + name.trim('/'))

    companion object {
        /** 拆分路径为片段，过滤空段（处理多余 "/"）。 */
        fun split(path: String): MutableList<String> =
            path.split('/').filter { it.isNotEmpty() }.toMutableList()

        fun join(segments: List<String>): String =
            if (segments.isEmpty()) "/" else segments.joinToString("/", prefix = "/")

        /** 父目录路径："/a/b" → "/a"，"/a" → "/"。 */
        fun parent(path: String): String = join(split(path).dropLast(1))

        /** 末段名称："/a/b" → "b"，"/" → ""。 */
        fun baseName(path: String): String = split(path).lastOrNull().orEmpty()
    }
}
