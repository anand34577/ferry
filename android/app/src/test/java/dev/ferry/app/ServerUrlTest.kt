package dev.ferry.app

import dev.ferry.app.server.ServerApi
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ServerUrlTest {
    @Test
    fun normalizeUrl() {
        assertEquals("https://files.example.com", ServerApi.normalizeUrl(" files.example.com/ "))
        assertEquals("http://192.168.1.10:8080", ServerApi.normalizeUrl("HTTP://192.168.1.10:8080/"))
        assertEquals("https://files.example.com/ferry", ServerApi.normalizeUrl("Https://files.example.com/ferry"))
        assertTrue(ServerApi.hasScheme("http://x"))
        assertFalse(ServerApi.hasScheme("192.168.1.10:8080"))
    }
}
