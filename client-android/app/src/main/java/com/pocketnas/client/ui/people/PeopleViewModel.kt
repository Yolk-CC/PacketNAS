package com.pocketnas.client.ui.people

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.pocketnas.client.App
import com.pocketnas.client.data.api.ApiException
import com.pocketnas.client.data.model.Person
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

/** 人物 tab 状态（SPEC-M12 §2）。先查 /api/faces/status，可用才拉 persons。 */
class PeopleViewModel(app: Application) : AndroidViewModel(app) {

    data class UiState(
        val loading: Boolean = false,
        val persons: List<Person> = emptyList(),
        /** faces 功能不可用（模型未下载等），reason 为服务端给出的原因。 */
        val unavailable: Boolean = false,
        val reason: String = "",
        val error: String? = null,
        val unauthorized: Boolean = false,
        val opResult: OpResult? = null,
    )

    sealed interface OpResult {
        data class Renamed(val name: String) : OpResult
        data object Merged : OpResult
        data class OpFailed(val message: String) : OpResult
    }

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state

    private var loadJob: Job? = null
    private val application: App get() = getApplication()

    init {
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
                val status = api.facesStatus()
                if (!status.available) {
                    _state.value = UiState(unavailable = true, reason = status.reason)
                    return@launch
                }
                _state.value = UiState(persons = api.persons())
            } catch (e: ApiException) {
                when {
                    e.isUnauthorized ->
                        _state.value = _state.value.copy(loading = false, unauthorized = true)
                    e.httpCode == 503 -> {
                        // persons 端点 503 = faces_unavailable，取 status 里的原因
                        val reason = try {
                            api.facesStatus().reason
                        } catch (_: Exception) {
                            ""
                        }
                        _state.value = UiState(unavailable = true, reason = reason)
                    }
                    else -> _state.value = _state.value.copy(loading = false, error = e.message)
                }
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, error = e.message)
            }
        }
    }

    fun renamePerson(id: Long, name: String) = runOp(OpResult.Renamed(name)) {
        renamePerson(id, name)
    }

    /** from 合并进 to（SPEC-M12 §2：合并到先选中的）。 */
    fun merge(from: Long, to: Long) = runOp(OpResult.Merged) {
        mergePersons(from, to)
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
