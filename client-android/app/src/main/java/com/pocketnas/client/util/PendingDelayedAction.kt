package com.pocketnas.client.util

/**
 * Holds at most one pending delayed [Runnable] and cancels it precisely via
 * `removeCallbacks(runnable)` instead of `removeCallbacksAndMessages(null)`
 * (which would wipe unrelated callbacks posted on the same handler/view).
 *
 * Pure logic: the post/remove primitives are injected so the behaviour is
 * unit-testable without Android.
 */
class PendingDelayedAction(
    private val postDelayed: (Runnable, Long) -> Unit,
    private val removeCallbacks: (Runnable) -> Unit,
) {
    private var pending: Runnable? = null

    val hasPending: Boolean get() = pending != null

    /** Schedules [action] after [delayMs], replacing any still-pending action. */
    fun schedule(delayMs: Long, action: () -> Unit) {
        cancel()
        val runnable = Runnable {
            pending = null
            action()
        }
        pending = runnable
        postDelayed(runnable, delayMs)
    }

    /** Cancels the pending action if it has not fired yet. */
    fun cancel() {
        pending?.let(removeCallbacks)
        pending = null
    }
}
