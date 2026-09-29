package dev.ferry.app

import dev.ferry.app.lan.ChunkedInputStream
import org.junit.Assert.assertEquals
import org.junit.Test

class ChunkedTest {
    @Test
    fun decodesChunkedBody() {
        val raw = "5;ext=1\r\nhello\r\n7\r\n, world\r\n0\r\nX-Trailer: 1\r\n\r\nNEXT".toByteArray()
        val input = raw.inputStream()
        assertEquals("hello, world", ChunkedInputStream(input).readBytes().decodeToString())
        // The connection is left positioned right after the body, ready for the next request.
        assertEquals("NEXT", input.readBytes().decodeToString())
    }
}
