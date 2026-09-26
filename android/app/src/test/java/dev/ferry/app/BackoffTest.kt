package dev.ferry.app

import dev.ferry.app.server.CancelledException
import dev.ferry.app.server.backoff
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class BackoffTest {
    @Test
    fun cancelInterruptsTheWait() {
        val start = System.currentTimeMillis()
        try {
            backoff(8) { System.currentTimeMillis() - start > 300 } // would wait 30 s
            fail("expected CancelledException")
        } catch (_: CancelledException) {
        }
        assertTrue("cancel must be noticed quickly", System.currentTimeMillis() - start < 2_000)
    }

    @Test
    fun waitsWhenNotCancelled() {
        val start = System.currentTimeMillis()
        backoff(0) { false }
        assertTrue(System.currentTimeMillis() - start >= 1_000)
    }
}
