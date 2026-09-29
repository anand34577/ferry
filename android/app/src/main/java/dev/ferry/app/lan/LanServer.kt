package dev.ferry.app.lan

import android.content.Context
import android.net.Uri
import dev.ferry.app.FerryApp
import dev.ferry.app.data.IncomingRequest
import dev.ferry.app.data.Method as TMethod
import dev.ferry.app.data.TFile
import dev.ferry.app.data.TStatus
import dev.ferry.app.data.Transfer
import dev.ferry.app.server.hashPrefix
import dev.ferry.app.transfer.Storage
import dev.ferry.app.util.hex
import dev.ferry.app.util.safeRelativePath
import fi.iki.elonen.NanoHTTPD
import org.json.JSONObject
import java.io.IOException
import java.security.MessageDigest
import java.util.UUID

/**
 * LocalSend v2 receiver (HTTPS). One incoming session at a time, like LocalSend.
 * Nothing is accepted until the user approves (or the sender is a trusted device with auto-accept on).
 */
class LanServer(private val ctx: Context, private val certs: Certs, val port: Int) : NanoHTTPD(port) {

    private class RFile(
        val id: String, val name: String, val dirs: List<String>, val size: Long, val mime: String, val token: String, val expectedSha: String,
    ) {
        var uri: Uri? = null
        var received = 0L
        var sha = ""
        var done = false
        var verified = false
        @Volatile var busy = false
    }

    private class Session(val id: String, val alias: String, val ferry: Boolean, val files: Map<String, RFile>) {
        @Volatile var lastActivity = System.currentTimeMillis()
        @Volatile var cancelled = false
        val transferId = "lan-" + id
    }

    @Volatile private var session: Session? = null
    @Volatile private var pinFailures = 0
    @Volatile private var pinLockedUntil = 0L
    private val app get() = FerryApp.app

    init {
        makeSecure(certs.serverSocketFactory(), null)
    }

    fun startServer() = start(30_000, false)

    private fun json(status: Response.Status, o: JSONObject) = newFixedLengthResponse(status, "application/json", o.toString())
    private fun err(status: Response.Status, msg: String) = json(status, JSONObject().put("message", msg))

    override fun serve(s: IHTTPSession): Response = try {
        val path = s.uri.removeSuffix("/")
        when {
            path.endsWith("/info") -> json(Response.Status.OK, LocalSend.selfInfo(app.prefs.alias, certs.fingerprint, port))
            path.endsWith("/register") -> register(s)
            path == LocalSend.API + "/prepare-upload" && s.method == NanoHTTPD.Method.POST -> prepare(s)
            path == LocalSend.API + "/upload" && s.method == NanoHTTPD.Method.POST -> upload(s)
            path == LocalSend.API + "/cancel" && s.method == NanoHTTPD.Method.POST -> cancel(s)
            path == LocalSend.API + "/ferry/offset" -> offset(s)
            path == LocalSend.API + "/ferry/verify" && s.method == NanoHTTPD.Method.POST -> verify(s)
            else -> err(Response.Status.NOT_FOUND, "Not found")
        }
    } catch (e: Exception) {
        err(Response.Status.INTERNAL_ERROR, e.message ?: "error")
    }

    private fun body(s: IHTTPSession): String {
        val len = s.headers["content-length"]?.toLongOrNull() ?: 0
        if (len > 4 * 1024 * 1024) throw IOException("Request too large")
        val buf = ByteArray(len.toInt())
        var off = 0
        while (off < buf.size) {
            val n = s.inputStream.read(buf, off, buf.size - off)
            if (n < 0) break
            off += n
        }
        return String(buf, 0, off)
    }

    private fun register(s: IHTTPSession): Response {
        runCatching {
            val o = JSONObject(body(s))
            val peer = LocalSend.peerFrom(o, s.remoteIpAddress, "multicast")
            if (peer.fingerprint != certs.fingerprint) app.discovery.upsert(peer)
        }
        return json(Response.Status.OK, LocalSend.selfInfo(app.prefs.alias, certs.fingerprint, port))
    }

    private fun param(s: IHTTPSession, k: String) = s.parameters[k]?.firstOrNull().orEmpty()

    private fun prepare(s: IHTTPSession): Response {
        if (!app.prefs.receiving) return err(Response.Status.FORBIDDEN, "This device is not receiving right now.")
        val cur = session
        // Ferry senders resume for 10 minutes; a stock LocalSend sender can't, so its session ends sooner.
        val idleLimit = if (cur?.ferry == true) 10 * 60_000 else 2 * 60_000
        if (cur != null && !cur.cancelled && System.currentTimeMillis() - cur.lastActivity < idleLimit && cur.files.values.any { !it.done }) {
            return err(Response.Status.CONFLICT, "Busy with another transfer")
        }
        if (cur != null) expire(cur)
        if (app.prefs.requirePin && app.prefs.pin.isNotEmpty()) {
            // A 4–6 digit PIN falls to brute force in minutes without a limit: 5 misses lock it for a minute.
            val now = System.currentTimeMillis()
            if (now < pinLockedUntil) return err(Response.Status.UNAUTHORIZED, "PIN required")
            if (param(s, "pin") != app.prefs.pin) {
                if (++pinFailures >= 5) {
                    pinFailures = 0; pinLockedUntil = now + 60_000
                }
                return err(Response.Status.UNAUTHORIZED, "PIN required")
            }
            pinFailures = 0
        }
        val o = JSONObject(body(s))
        val info = o.getJSONObject("info")
        val filesJson = o.getJSONObject("files")
        val files = filesJson.keys().asSequence().map { k ->
            val f = filesJson.getJSONObject(k)
            val (dirs, name) = safeRelativePath(f.optString("fileName", "file"))
            RFile(f.optString("id", k), name, dirs, f.optLong("size", 0).coerceAtLeast(0), f.optString("fileType", Storage.mimeFor(name)),
                UUID.randomUUID().toString(), f.optString("sha256", "").lowercase())
        }.toList()
        if (files.isEmpty()) return newFixedLengthResponse(Response.Status.NO_CONTENT, "application/json", "")
        val claimed = info.optString("fingerprint", "")
        // The fingerprint in the request is self-asserted and every device multicasts its own, so anyone on
        // the LAN could claim a trusted one. A trusted claim only counts if the sender's HTTPS server proves it
        // holds that certificate; an unproven one is dropped, so it can't auto-accept or show as trusted.
        val trusted = claimed.isNotEmpty() && app.prefs.isTrusted(claimed)
        val proven = trusted && provesIdentity(s.remoteIpAddress, info.optInt("port", LocalSend.PORT), claimed)
        val fp = if (trusted && !proven) "" else claimed
        val alias = info.optString("alias", "Unknown device").take(60)
        val req = IncomingRequest(UUID.randomUUID().toString(), alias, info.optString("deviceModel", "").take(60), fp,
            trusted = proven, ownAccount = false, files = files.map { (it.dirs + it.name).joinToString("/") to it.size }, source = TMethod.DIRECT)
        val accepted = app.transfers.askIncoming(req)
        if (!accepted) return err(Response.Status.FORBIDDEN, "Declined")
        val sess = Session(UUID.randomUUID().toString(), alias, info.optInt("ferry", 0) >= 1, files.associateBy { it.id })
        session = sess
        app.transfers.lanReceiveStarted(Transfer(sess.transferId, sent = false, method = TMethod.DIRECT, peer = alias,
            files = files.map { TFile(it.id, (it.dirs + it.name).joinToString("/"), it.size, it.mime) }, status = TStatus.WAITING))
        val tokens = JSONObject().apply { files.forEach { put(it.id, it.token) } }
        return json(Response.Status.OK, JSONObject().put("sessionId", sess.id).put("files", tokens))
    }

    /** True when an HTTPS server at the sender's address presents the certificate whose SHA-256 is [fingerprint]. */
    private fun provesIdentity(ip: String, port: Int, fingerprint: String): Boolean =
        runCatching { LanClient.info(ip, port, fingerprint, "verify", httpsHint = true) }.isSuccess

    private fun authorize(s: IHTTPSession): Pair<Session, RFile>? {
        val sess = session ?: return null
        if (sess.id != param(s, "sessionId") || sess.cancelled) return null
        // No IP check: per-file tokens are secrets, and the sender's IP may change after a network switch.
        val f = sess.files[param(s, "fileId")] ?: return null
        if (f.token != param(s, "token")) return null
        return sess to f
    }

    private fun upload(s: IHTTPSession): Response {
        val (sess, f) = authorize(s) ?: return err(Response.Status.FORBIDDEN, "Invalid session or token")
        if (f.done) return json(Response.Status.OK, JSONObject())
        if (f.busy) return err(Response.Status.CONFLICT, "Upload already in progress")
        // Stock LocalSend streams uploads chunked, with no Content-Length; NanoHTTPD doesn't decode that itself.
        val chunked = s.headers["transfer-encoding"]?.contains("chunked", ignoreCase = true) == true
        val offset = param(s, "offset").toLongOrNull() ?: 0L
        val len = if (chunked) f.size - offset
            else s.headers["content-length"]?.toLongOrNull() ?: return err(Response.Status.LENGTH_REQUIRED, "Content-Length required")
        if (offset != 0L && offset != f.received) return json(Response.Status.CONFLICT, JSONObject().put("offset", f.received))
        if (offset + len > f.size) return err(Response.Status.BAD_REQUEST, "More data than declared")
        f.busy = true
        sess.lastActivity = System.currentTimeMillis()
        app.transfers.lanReceiveStatus(sess.transferId, TStatus.TRANSFERRING, "")
        try {
            if (f.uri == null) f.uri = Storage.createPending(ctx, f.name, f.mime, f.dirs)
            val uri = f.uri!!
            val md = MessageDigest.getInstance("SHA-256")
            if (offset > 0) ctx.contentResolver.openInputStream(uri)?.use { hashPrefix(it, md, offset) }
            f.received = offset
            val input = if (chunked) ChunkedInputStream(s.inputStream) else s.inputStream
            (if (offset == 0L) Storage.openTruncate(ctx, uri) else Storage.openAppend(ctx, uri)).use { out ->
                val buf = ByteArray(256 * 1024)
                var left = len
                while (left > 0) {
                    if (sess.cancelled) throw IOException("cancelled")
                    val n = input.read(buf, 0, minOf(buf.size.toLong(), left).toInt())
                    if (n < 0 && chunked) break // body ended; a short upload is caught below
                    if (n < 0) throw IOException("Connection closed")
                    out.write(buf, 0, n)
                    md.update(buf, 0, n)
                    left -= n
                    f.received += n
                    sess.lastActivity = System.currentTimeMillis()
                    app.transfers.lanReceiveProgress(sess.transferId, f.id, f.received)
                }
            }
            // A chunked body's length is only known at its end: reject one that's longer or shorter than declared.
            if (chunked && input.read() >= 0) return err(Response.Status.BAD_REQUEST, "More data than declared")
            if (chunked && f.received != f.size) throw IOException("Upload ended early")
            if (sess.cancelled) throw IOException("cancelled") // cancelled while the last bytes arrived: don't keep the file
            if (f.received == f.size) {
                f.sha = md.digest().hex()
                if (f.expectedSha.isNotEmpty() && f.expectedSha != f.sha) {
                    Storage.delete(ctx, uri); f.uri = null; f.received = 0
                    app.transfers.lanReceiveStatus(sess.transferId, TStatus.FAILED, "\"${f.name}\" was corrupted in transit (checksum mismatch).")
                    return err(Response.Status.INTERNAL_ERROR, "Checksum mismatch")
                }
                Storage.publish(ctx, uri)
                f.done = true
                maybeFinish(sess)
            }
            return json(Response.Status.OK, JSONObject())
        } catch (e: IOException) {
            if (sess.cancelled) {
                cleanup(sess)
                return err(Response.Status.FORBIDDEN, "Cancelled by the receiver").also { it.closeConnection(true) }
            } else {
                // Keep the partial file: a Ferry sender can resume from f.received for 10 minutes.
                app.transfers.lanReceiveStatus(sess.transferId, TStatus.INTERRUPTED, "Connection lost — waiting for the sender to resume…")
            }
            return err(Response.Status.INTERNAL_ERROR, e.message ?: "I/O error")
        } finally {
            f.busy = false
        }
    }

    /** Closes a session whose sender went away, so its transfer doesn't stay "in progress" forever. */
    private fun expire(sess: Session) {
        val all = sess.files.values.all { it.done }
        cleanup(sess)
        app.transfers.lanReceiveFinished(sess.transferId, if (all) TStatus.COMPLETED else TStatus.FAILED, if (all) "" else "The sender stopped sending.", sess.alias)
    }

    private fun maybeFinish(sess: Session) {
        if (!sess.files.values.all { it.done }) return
        // Ferry senders confirm every file's SHA-256 via /ferry/verify; stock LocalSend senders can't.
        if (sess.ferry && !sess.files.values.all { it.verified }) app.transfers.lanReceiveStatus(sess.transferId, TStatus.VERIFYING, "Verifying integrity…")
        else app.transfers.lanReceiveFinished(sess.transferId, TStatus.COMPLETED, "", sess.alias)
    }

    private fun offset(s: IHTTPSession): Response {
        val (_, f) = authorize(s) ?: return err(Response.Status.FORBIDDEN, "Invalid session or token")
        return json(Response.Status.OK, JSONObject().put("offset", f.received).put("done", f.done))
    }

    private fun verify(s: IHTTPSession): Response {
        val (sess, f) = authorize(s) ?: return err(Response.Status.FORBIDDEN, "Invalid session or token")
        val sha = param(s, "sha256").lowercase()
        if (!f.done) return err(Response.Status.CONFLICT, "Not complete")
        if (sha != f.sha) {
            f.uri?.let { Storage.delete(ctx, it) }
            f.done = false; f.received = 0; f.uri = null
            app.transfers.lanReceiveFinished(sess.transferId, TStatus.FAILED, "\"${f.name}\" was corrupted in transit (checksum mismatch).", sess.alias)
            return err(Response.Status.CONFLICT, "Checksum mismatch")
        }
        f.verified = true
        maybeFinish(sess)
        return json(Response.Status.OK, JSONObject().put("ok", true))
    }

    private fun cancel(s: IHTTPSession): Response {
        val sess = session
        if (sess != null && sess.id == param(s, "sessionId")) {
            sess.cancelled = true
            cleanup(sess)
            app.transfers.lanReceiveFinished(sess.transferId, TStatus.CANCELLED, "The sender cancelled the transfer.", sess.alias)
        }
        return json(Response.Status.OK, JSONObject())
    }

    /** Called from the UI when the user cancels an incoming transfer. */
    fun cancelActive(transferId: String) {
        val sess = session ?: return
        if (sess.transferId == transferId) {
            sess.cancelled = true
            cleanup(sess)
        }
    }

    private fun cleanup(sess: Session) {
        sess.files.values.filter { !it.done }.forEach { f -> f.uri?.let { Storage.delete(ctx, it) }; f.uri = null }
        if (session === sess) session = null
    }
}

/** Decodes an HTTP/1.1 chunked request body; returns -1 after the final zero-length chunk. */
class ChunkedInputStream(private val raw: java.io.InputStream) : java.io.InputStream() {
    private var left = 0L
    private var eof = false

    override fun read(): Int {
        val b = ByteArray(1)
        return if (read(b, 0, 1) < 0) -1 else b[0].toInt() and 0xff
    }

    override fun read(b: ByteArray, off: Int, len: Int): Int {
        if (eof) return -1
        if (left == 0L) {
            left = line().substringBefore(';').trim().toLongOrNull(16)?.takeIf { it >= 0 } ?: throw IOException("Bad chunk size")
            if (left == 0L) {
                while (line().isNotEmpty()) Unit // trailers
                eof = true
                return -1
            }
        }
        val n = raw.read(b, off, minOf(len.toLong(), left).toInt())
        if (n < 0) throw IOException("Connection closed")
        left -= n
        if (left == 0L) line() // CRLF after the chunk data
        return n
    }

    private fun line(): String {
        val sb = StringBuilder()
        while (true) {
            val c = raw.read()
            if (c < 0) throw IOException("Connection closed")
            if (c == '\n'.code) return sb.toString().trimEnd('\r')
            if (sb.length > 1024) throw IOException("Chunk header too long")
            sb.append(c.toChar())
        }
    }
}
