package com.pocketnas.client.ui.files

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.pocketnas.client.App
import com.pocketnas.client.data.api.ApiException
import com.pocketnas.client.data.model.FileInfo
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

/** 文件 tab 状态（SPEC-M10 §2）。路径栈保存在 ViewModel，旋转不丢。 */
class FilesViewModel(app: Application) : AndroidViewModel(app) {

    data class UiState(
        val loading: Boolean = false,
        val items: List<FileInfo> = emptyList(),
        val error: String? = null,
        val unauthorized: Boolean = false,
        /** 每次成功操作自增，驱动 UI toast/刷新。 */
        val opResult: OpResult? = null,
    )

    sealed interface OpResult {
        data class Uploaded(val count: Int) : OpResult
        data class Renamed(val name: String) : OpResult
        data class Deleted(val count: Int) : OpResult
        data class MkdirDone(val name: String) : OpResult
        data class OpFailed(val message: String) : OpResult
    }

    val pathStack = PathStack()

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state

    private var loadJob: Job? = null
    private val application: App get() = getApplication()

    init {
        reload()
    }

    fun enterDir(name: String) {
        pathStack.push(name)
        reload()
    }

    /** 返回 true 表示消费了返回键（回退了一级）。 */
    fun navigateUp(): Boolean {
        if (!pathStack.pop()) return false
        reload()
        return true
    }

    fun navigateTo(depth: Int) {
        pathStack.popTo(depth)
        reload()
    }

    fun reload() {
        loadJob?.cancel()
        loadJob = viewModelScope.launch {
            val api = application.apiClient
            if (api == null) {
                _state.value = UiState(error = "未连接服务器")
                return@launch
            }
            _state.value = _state.value.copy(loading = true, error = null)
            try {
                // 目录在前、再按名称排序，与 Web UI 一致
                val items = api.listFiles(pathStack.path)
                    .sortedWith(compareBy({ !it.isDir }, { it.name.lowercase() }))
                _state.value = UiState(items = items)
            } catch (e: ApiException) {
                _state.value = if (e.isUnauthorized) {
                    _state.value.copy(loading = false, unauthorized = true)
                } else {
                    _state.value.copy(loading = false, error = e.message)
                }
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, error = e.message)
            }
        }
    }

    fun mkdir(name: String) = runOp(OpResult.MkdirDone(name)) {
        mkdir(pathStack.path, name)
    }

    fun rename(path: String, newName: String) = runOp(OpResult.Renamed(newName)) {
        rename(path, newName)
    }

    fun delete(paths: List<String>) = runOp(OpResult.Deleted(paths.size)) {
        deleteFiles(paths)
    }

    /** Fragment 处理完 opResult 后调用，避免重复 toast。 */
    fun consumeOpResult() {
        _state.value = _state.value.copy(opResult = null)
    }

    private fun runOp(result: OpResult, block: suspend com.pocketnas.client.data.api.PocketNasApi.() -> Unit) {
        viewModelScope.launch {
            val api = application.apiClient ?: return@launch
            try {
                api.block()
                reload()
                _state.value = _state.value.copy(opResult = result)
            } catch (e: ApiException) {
                if (e.isUnauthorized) {
                    _state.value = _state.value.copy(unauthorized = true)
                } else {
                    _state.value = _state.value.copy(opResult = OpResult.OpFailed(e.message ?: "操作失败"))
                }
            } catch (e: Exception) {
                _state.value = _state.value.copy(opResult = OpResult.OpFailed(e.message ?: "操作失败"))
            }
        }
    }
}
