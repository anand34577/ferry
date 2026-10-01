package dev.ferry.app

import dev.ferry.app.data.TStatus
import dev.ferry.app.util.PairCode
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.pipelined
import dev.ferry.app.util.safeName
import dev.ferry.app.util.safeRelativePath
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class UtilTest {
    @Test
    fun pairCodeRoundTrip() {
        for (ip in listOf("192.168.1.23", "10.0.0.1", "172.16.254.3", "0.0.0.0", "255.255.255.255")) {
            val code = PairCode.encode(ip)!!
            assertEquals(8, code.length) // XXX-XXXX
            assertEquals(ip to PairCode.DEFAULT_PORT, PairCode.decode(code))
        }
        val withPort = PairCode.encode("192.168.43.1", 53320)!!
        assertEquals("192.168.43.1" to 53320, PairCode.decode(withPort))
        // Forgiving input: lowercase, spaces, confusable letters.
        val c = PairCode.encode("192.168.1.23")!!
        assertEquals("192.168.1.23" to PairCode.DEFAULT_PORT, PairCode.decode(" " + c.lowercase().replace("-", " ") + " "))
        assertNull(PairCode.decode("hello"))
        assertNull(PairCode.encode("300.1.1.1"))
    }

    @Test
    fun namesAreSanitised() {
        assertEquals(".._.._etc_passwd", safeName("../../etc/passwd"))
        assertEquals("file", safeName(".."))
        assertEquals("résumé 📄.pdf", safeName("résumé 📄.pdf"))
        assertTrue(safeName("a".repeat(400) + ".jpeg").let { it.toByteArray().size <= 200 && it.endsWith(".jpeg") })
        val (dirs, name) = safeRelativePath("../photos/./2026/../x.jpg")
        assertEquals(listOf("photos", "2026"), dirs)
        assertEquals("x.jpg", name)
    }

    @Test
    fun statesMatchServer() {
        assertEquals(listOf("created", "waiting", "negotiating", "connecting", "transferring", "verifying", "completed", "failed",
            "cancelled", "expired", "rejected", "interrupted"), TStatus.entries.map { it.wire })
        assertTrue(TStatus.COMPLETED.final && !TStatus.INTERRUPTED.final)
    }

    @Test
    fun bytes() {
        assertEquals("512 B", formatBytes(512))
        assertTrue(formatBytes(1536).startsWith("1") && formatBytes(1536).endsWith("KB"))
    }

    @Test
    fun pipelinedCopiesInOrderAndSurfacesFailures() {
        val src = ByteArray(100_000) { (it % 251).toByte() }
        var pos = 0
        val out = java.io.ByteArrayOutputStream()
        pipelined(size = 1000, depth = 3, produce = { b ->
            val n = minOf(b.size, src.size - pos)
            if (n <= 0) -1 else { System.arraycopy(src, pos, b, 0, n); pos += n; n }
        }, consume = { b, n -> out.write(b, 0, n) })
        assertTrue(src.contentEquals(out.toByteArray()))

        val boom = runCatching { pipelined(produce = { throw java.io.IOException("read failed") }, consume = { _, _ -> }) }.exceptionOrNull()
        assertEquals("read failed", boom?.message)
        val boom2 = runCatching { pipelined(produce = { 1 }, consume = { _, _ -> throw java.io.IOException("write failed") }) }.exceptionOrNull()
        assertEquals("write failed", boom2?.message)
    }
}
