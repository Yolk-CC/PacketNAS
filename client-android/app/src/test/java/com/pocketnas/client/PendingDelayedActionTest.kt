package com.pocketnas.client

import com.pocketnas.client.util.PendingDelayedAction
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** JVM fake of View.postDelayed/removeCallbacks for [PendingDelayedAction]. */
class PendingDelayedActionTest {

    private class FakeView {
        val posted = mutableListOf<Runnable>()
        val removed = mutableListOf<Runnable>()

        fun postDelayed(r: Runnable, delayMs: Long) {
            posted += r
        }

        fun removeCallbacks(r: Runnable) {
            removed += r
            posted.remove(r)
        }

        /** Simulates the handler firing all still-posted runnables. */
        fun fireAll() {
            val pending = posted.toList()
            posted.clear()
            pending.forEach { it.run() }
        }
    }

    @Test
    fun `scheduled action fires and clears pending`() {
        val view = FakeView()
        val runner = PendingDelayedAction(view::postDelayed, view::removeCallbacks)
        var fired = 0
        runner.schedule(400) { fired++ }
        assertTrue(runner.hasPending)
        view.fireAll()
        assertEquals(1, fired)
        assertFalse(runner.hasPending)
    }

    @Test
    fun `cancel before fire removes exact runnable and action never runs`() {
        val view = FakeView()
        val runner = PendingDelayedAction(view::postDelayed, view::removeCallbacks)
        var fired = 0
        runner.schedule(400) { fired++ }
        runner.cancel()
        assertFalse(runner.hasPending)
        assertEquals(1, view.removed.size)
        view.fireAll()
        assertEquals(0, fired)
    }

    @Test
    fun `cancel after fire does not remove anything`() {
        val view = FakeView()
        val runner = PendingDelayedAction(view::postDelayed, view::removeCallbacks)
        var fired = 0
        runner.schedule(400) { fired++ }
        view.fireAll()
        runner.cancel()
        assertTrue(view.removed.isEmpty())
    }

    @Test
    fun `schedule replaces still-pending action`() {
        val view = FakeView()
        val runner = PendingDelayedAction(view::postDelayed, view::removeCallbacks)
        val fired = mutableListOf<String>()
        runner.schedule(400) { fired += "first" }
        runner.schedule(400) { fired += "second" }
        // The superseded runnable was removed precisely.
        assertEquals(1, view.removed.size)
        view.fireAll()
        assertEquals(listOf("second"), fired)
    }

    @Test
    fun `recycle-then-fire race - cancelled runnable never touches released state`() {
        // Regression for the M15b fix: ACTION_DOWN 400ms window + ViewHolder
        // recycle must not let the pending runnable run afterwards.
        val view = FakeView()
        val runner = PendingDelayedAction(view::postDelayed, view::removeCallbacks)
        var playerReleased = false
        var playerTouched = false
        runner.schedule(400) {
            playerTouched = true
            check(!playerReleased) { "runnable touched a released player" }
        }
        // Recycle: release player, then cancel the pending action.
        playerReleased = true
        runner.cancel()
        view.fireAll()
        assertFalse(playerTouched)
    }
}
