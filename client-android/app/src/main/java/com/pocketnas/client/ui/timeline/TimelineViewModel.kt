package com.pocketnas.client.ui.timeline

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.pocketnas.client.App
import com.pocketnas.client.data.api.ApiException
import com.pocketnas.client.data.model.MediaItem
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

/** Timeline state survives rotation via ViewModel (SPEC-M9 §5). */
class TimelineViewModel(app: Application) : AndroidViewModel(app) {

    data class UiState(
        val loading: Boolean = false,
        val refreshing: Boolean = false,
        val rows: List<TimelineRow> = emptyList(),
        val items: List<MediaItem> = emptyList(),
        val total: Int = 0,
        val error: String? = null,
        val unauthorized: Boolean = false,
        val serverName: String = "",
    )

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state

    private var loadJob: Job? = null

    val application: App get() = getApplication()

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
                val page = api.gallery(offset = 0, limit = PAGE_SIZE)
                _state.value = UiState(
                    items = page.items,
                    total = page.total,
                    rows = DateGrouper.group(page.items),
                    serverName = application.server?.name.orEmpty(),
                )
            } catch (e: ApiException) {
                _state.value = UiState(
                    error = if (e.isUnauthorized) "需要重新输入密码" else "加载失败: ${e.message}",
                    unauthorized = e.isUnauthorized,
                )
            } catch (e: Exception) {
                _state.value = UiState(error = "服务器不可达: ${e.message}")
            }
        }
    }

    fun loadMore() {
        val s = _state.value
        if (s.loading || s.items.size >= s.total) return
        loadJob = viewModelScope.launch {
            val api = application.apiClient ?: return@launch
            _state.value = s.copy(loading = true)
            try {
                val page = api.gallery(offset = s.items.size, limit = PAGE_SIZE)
                val items = s.items + page.items
                _state.value = _state.value.copy(
                    loading = false,
                    items = items,
                    total = page.total,
                    rows = DateGrouper.group(items),
                )
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, error = "加载失败: ${e.message}")
            }
        }
    }

    /** Pull-to-refresh: trigger a rescan, then reload (SPEC-M9 §3). */
    fun refresh() {
        loadJob?.cancel()
        loadJob = viewModelScope.launch {
            val api = application.apiClient ?: return@launch
            _state.value = _state.value.copy(refreshing = true)
            try {
                api.galleryScan()
            } catch (_: Exception) {
                // scan status is advisory; continue with reload
            }
            _state.value = _state.value.copy(refreshing = false)
            reload()
        }
    }

    companion object {
        const val PAGE_SIZE = 200
    }
}
