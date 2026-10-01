package dev.ferry.app.util

import java.security.MessageDigest
import java.util.Locale
import kotlin.math.abs
import kotlin.math.roundToLong

fun formatBytes(n: Long): String {
    if (n < 0) return "—"
    if (n < 1024) return "$n B"
    val units = arrayOf("KB", "MB", "GB", "TB", "PB")
    var v = n.toDouble()
    var i = -1
    while (v >= 1024 && i < units.size - 1) {
        v /= 1024; i++
    }
    return if (v >= 100) "${v.roundToLong()} ${units[i]}" else String.format(Locale.getDefault(), "%.1f %s", v, units[i]).replace(".0 ", " ")
}

fun formatSpeed(bps: Double): String = if (bps <= 0) "" else formatBytes(bps.toLong()) + "/s"

fun formatEta(seconds: Double): String = when {
    seconds <= 0 || seconds.isNaN() || seconds.isInfinite() -> ""
    seconds < 60 -> "${seconds.toInt() + 1}s left"
    seconds < 3600 -> "${(seconds / 60).toInt() + 1} min left"
    else -> String.format(Locale.getDefault(), "%.1f h left", seconds / 3600)
}

/** Relative time like "5 min ago" / "in 2 h" using a server-corrected clock. */
fun relativeTime(ms: Long, now: Long = System.currentTimeMillis()): String {
    val diff = (ms - now) / 1000
    val a = abs(diff)
    val s = when {
        a < 45 -> return "just now"
        a < 3600 -> "${a / 60} min"
        a < 86400 -> "${a / 3600} h"
        a < 30 * 86400 -> "${a / 86400} d"
        a < 365 * 86400 -> "${a / (30 * 86400)} mo"
        else -> "${a / (365 * 86400)} y"
    }
    return if (diff < 0) "$s ago" else "in $s"
}

fun ByteArray.hex(): String = joinToString("") { "%02x".format(it) }

fun sha256Hex(b: ByteArray): String = MessageDigest.getInstance("SHA-256").digest(b).hex()

/**
 * Makes a peer-supplied file name safe: no separators, control characters or reserved names.
 * LocalSend may send relative paths ("folder/a.jpg"); callers split those into segments first.
 */
fun safeName(name: String): String {
    var n = name.map { c ->
        when {
            c == '/' || c == '\\' || c.code == 0 -> '_'
            c.isISOControl() -> ' '
            c in "<>:\"|?*" -> '_'
            else -> c
        }
    }.joinToString("").trim().trimEnd('.', ' ')
    if (n.isEmpty() || n == "." || n == "..") n = "file"
    if (n.toByteArray().size > 200) {
        val dot = n.lastIndexOf('.')
        val ext = if (dot > 0 && n.length - dot <= 12) n.substring(dot) else ""
        var stem = n.substring(0, n.length - ext.length)
        while ((stem + ext).toByteArray().size > 200) stem = stem.dropLast(1)
        n = stem + ext
    }
    return n
}

/** Splits "a/b/c.txt" into safe directory segments + file name, dropping "..", "." and empty parts. */
fun safeRelativePath(path: String): Pair<List<String>, String> {
    val parts = path.replace('\\', '/').split('/').filter { it.isNotBlank() && it != "." && it != ".." }
    if (parts.isEmpty()) return emptyList<String>() to "file"
    return parts.dropLast(1).map(::safeName).take(8) to safeName(parts.last())
}

/**
 * Short pairing code: an IPv4 address (and optional non-default port) encoded in Crockford base32.
 * Works fully offline — "192.168.1.23" becomes e.g. "60A-R0BQ".
 */
object PairCode {
    private const val ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
    const val DEFAULT_PORT = 53317

    fun encode(ip: String, port: Int = DEFAULT_PORT): String? {
        val o = ip.split('.').mapNotNull { it.toIntOrNull() }
        if (o.size != 4 || o.any { it !in 0..255 }) return null
        var v = o.fold(0L) { acc, b -> (acc shl 8) or b.toLong() }
        var bits = 32
        if (port != DEFAULT_PORT) {
            v = (v shl 16) or port.toLong(); bits = 48
        }
        val chars = (bits + 4) / 5
        val sb = StringBuilder()
        for (i in chars - 1 downTo 0) sb.append(ALPHABET[((v shr (i * 5)) and 31).toInt()])
        val s = sb.toString()
        return if (s.length == 7) s.substring(0, 3) + "-" + s.substring(3) else s.chunked(5).joinToString("-")
    }

    fun decode(code: String): Pair<String, Int>? {
        val clean = code.uppercase().replace("-", "").replace(" ", "")
            .replace('O', '0').replace('I', '1').replace('L', '1')
        if (clean.length != 7 && clean.length != 10) return null
        var v = 0L
        for (c in clean) {
            val d = ALPHABET.indexOf(c)
            if (d < 0) return null
            v = (v shl 5) or d.toLong()
        }
        var port = DEFAULT_PORT
        if (clean.length == 10) {
            port = (v and 0xFFFF).toInt(); v = v shr 16
        }
        if (v > 0xFFFFFFFFL) return null
        val ip = listOf(24, 16, 8, 0).joinToString(".") { ((v shr it) and 255).toString() }
        return ip to port
    }
}

/**
 * Overlaps the two ends of a copy: [produce] fills buffers on a worker thread (returns bytes read, or -1 at the end)
 * while [consume] handles them on the caller's thread — e.g. socket reads and disk writes, or disk reads and socket writes.
 * A failure on either side is rethrown to the caller. Memory is bounded to [depth] buffers of [size] bytes.
 */
fun pipelined(size: Int = 512 * 1024, depth: Int = 4, produce: (ByteArray) -> Int, consume: (ByteArray, Int) -> Unit) {
    class Chunk(val buf: ByteArray, var n: Int = 0)
    val free = java.util.concurrent.ArrayBlockingQueue<Chunk>(depth)
    val full = java.util.concurrent.ArrayBlockingQueue<Chunk>(depth + 1) // +1: room for the failure marker
    repeat(depth) { free.add(Chunk(ByteArray(size))) }
    val failure = java.util.concurrent.atomic.AtomicReference<Throwable?>()
    val worker = Thread {
        try {
            while (true) {
                val c = free.take()
                c.n = produce(c.buf)
                full.put(c)
                if (c.n < 0) return@Thread
            }
        } catch (_: InterruptedException) {
        } catch (t: Throwable) {
            failure.set(t)
            full.offer(Chunk(ByteArray(0), -1))
        }
    }.apply { isDaemon = true; name = "ferry-pipe"; start() }
    try {
        while (true) {
            val c = full.take()
            if (c.n < 0) break
            consume(c.buf, c.n)
            free.put(c)
        }
        failure.get()?.let { throw it }
    } finally {
        worker.interrupt()
    }
}
