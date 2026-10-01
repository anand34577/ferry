package dev.ferry.app.transfer

import android.content.Context
import android.net.Uri
import dev.ferry.app.FerryApp
import dev.ferry.app.data.IncomingRequest
import dev.ferry.app.data.Method
import dev.ferry.app.data.Peer
import dev.ferry.app.data.TFile
import dev.ferry.app.data.TStatus
import dev.ferry.app.data.Transfer
import dev.ferry.app.lan.LanClient
import dev.ferry.app.lan.LocalSend
import dev.ferry.app.lan.PeerException
import dev.ferry.app.server.ApiException
import dev.ferry.app.server.CancelledException
import dev.ferry.app.server.ServerManager
import dev.ferry.app.server.StreamBody
import dev.ferry.app.server.hashPrefix
import dev.ferry.app.server.linkUrl
import dev.ferry.app.util.hex
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.Call
import org.json.JSONObject
import java.io.IOException
import java.net.ConnectException
import java.net.NoRouteToHostException
import java.net.SocketTimeoutException
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Runs every transfer (direct LAN, server relay, links, inbox downloads) and exposes one list of
 * active transfers with the same state machine as the server and web app.
 */
class TransferManager(private val ctx: Context, private val scope: CoroutineScope) {
    private val app get() = FerryApp.app

    private val _active = MutableStateFlow<List<Transfer>>(emptyList())
    val active: StateFlow<List<Transfer>> = _active
    private val _incoming = MutableStateFlow<List<IncomingRequest>>(emptyList())
    val incoming: StateFlow<List<IncomingRequest>> = _incoming

    data class PinRequest(val transferId: String, val peer: String, val wrong: Boolean)
    private val _pin = MutableStateFlow<PinRequest?>(null)
    val pinRequest: StateFlow<PinRequest?> = _pin
    private var pinAnswer: CompletableDeferred<String?>? = null

    private val decisions = ConcurrentHashMap<String, CompletableDeferred<Boolean>>()
    private val seenInbox = ConcurrentHashMap.newKeySet<String>()

    /** Actions the UI can offer on a failed transfer (kept in memory for this app session). */
    val retryable = ConcurrentHashMap<String, () -> Unit>()
    val linkable = ConcurrentHashMap<String, List<TFile>>()

    private companion object {
        const val PARALLEL_FILES = 4
    }

    private class DirectState(val sessionId: String, val tokens: Map<String, String>) {
        val sent = ConcurrentHashMap<String, String>() // fileId → sha256
    }

    private class Control {
        val cancelled = AtomicBoolean(false)
        val paused = AtomicBoolean(false)
        val calls: MutableSet<Call> = ConcurrentHashMap.newKeySet() // in flight: parallel files each have one
        val done = ConcurrentHashMap<String, Long>() // fileId → bytes done, updated on every progress tick
        fun cancelCalls() = calls.forEach { it.cancel() }
        var job: Job? = null
        var direct: DirectState? = null
        var restart: (() -> Unit)? = null
        var peer: Peer? = null
        var lastBytes = 0L
        var lastTime = 0L
        var lastEmit = 0L
    }
    private val controls = ConcurrentHashMap<String, Control>()

    // Snapshot of active transfers (written on state changes, not progress) so a process death is visible.
    private val activeFile = java.io.File(ctx.filesDir, "active-transfers.json")

    init {
        // Transfers that were running when the process died can't continue (their state was in memory):
        // record them in history as interrupted instead of letting them silently disappear.
        runCatching {
            if (activeFile.exists()) {
                val a = org.json.JSONArray(activeFile.readText())
                for (i in 0 until a.length()) runCatching { Transfer.from(a.getJSONObject(i)) }.getOrNull()?.let {
                    app.history.add(it.copy(status = TStatus.INTERRUPTED, error = "Ferry was closed during this transfer. Send the files again to retry."))
                }
            }
        }
        activeFile.delete()
    }

    @Synchronized
    private fun persist() {
        runCatching {
            val tmp = java.io.File(activeFile.path + ".tmp")
            tmp.writeText(org.json.JSONArray(_active.value.map { it.toJson() }).toString())
            tmp.renameTo(activeFile)
        }
    }

    // ---------- state helpers ----------

    private fun get(id: String) = _active.value.find { it.id == id }

    private fun upsert(t: Transfer) {
        _active.update { list -> if (list.any { it.id == t.id }) list.map { if (it.id == t.id) t else it } else list + t }
        persist()
        if (t.status.active) TransferService.ensure(ctx)
    }

    private fun update(id: String, f: (Transfer) -> Transfer) {
        _active.update { list -> list.map { if (it.id == id) f(it).copy(updatedAt = System.currentTimeMillis()) else it } }
    }

    private fun status(id: String, s: TStatus, note: String = "") {
        update(id) { it.copy(status = s, note = note, speed = if (s == TStatus.TRANSFERRING) it.speed else 0.0) }
        persist()
    }

    private fun progress(id: String, fileId: String, done: Long) {
        val c = controls.getOrPut(id) { Control() }
        c.done[fileId] = done // every tick is recorded; only the UI update is throttled, so parallel files add up correctly
        val now = System.currentTimeMillis()
        val t = get(id) ?: return
        val finished = t.files.any { it.id == fileId && done >= it.size }
        synchronized(c) {
            if (now - c.lastEmit < 250 && !finished) return
            val files = t.files.map { f -> c.done[f.id]?.let { f.copy(done = it) } ?: f }
            val total = files.sumOf { it.done }
            var speed = t.speed
            if (c.lastTime > 0 && now > c.lastTime) {
                val inst = (total - c.lastBytes) * 1000.0 / (now - c.lastTime)
                if (inst >= 0) speed = if (speed > 0) speed * 0.7 + inst * 0.3 else inst
            }
            c.lastTime = now; c.lastBytes = total; c.lastEmit = now
            update(id) { it.copy(files = files, speed = speed) }
        }
    }

    private fun finish(id: String, s: TStatus, error: String = "", shareUrl: String = "") {
        val t = (get(id) ?: app.history.items.value.find { it.id == id } ?: return)
            .copy(status = s, error = error, speed = 0.0, paused = false, shareUrl = shareUrl.ifEmpty { get(id)?.shareUrl ?: "" }, updatedAt = System.currentTimeMillis())
        _active.update { list -> list.filter { it.id != id } }
        persist()
        controls.remove(id)
        app.history.add(t)
        if (s == TStatus.COMPLETED) {
            retryable.remove(id); linkable.remove(id)
        }
        Notifier.finished(ctx, t)
        if (t.method == Method.DIRECT) scope.launch(Dispatchers.IO) {
            app.server.api()?.reportTransfer(t.sent, t.peer, s.wire, t.files.size, t.total, error)
        }
    }

    private fun markUploaded(id: String, fileId: String, serverFileId: String) =
        update(id) { t -> t.copy(files = t.files.map { if (it.id == fileId) it.copy(serverFileId = serverFileId, done = it.size) else it }) }

    fun dismiss(id: String) {
        _active.update { list -> list.filter { it.id != id } }
        persist()
    }

    private fun newTransfer(sent: Boolean, method: Method, peer: String, files: List<TFile>, status: TStatus, canPause: Boolean = false) =
        Transfer(UUID.randomUUID().toString(), sent, method, peer, files.map { it.copy(done = 0) }, status, canPause = canPause).also { upsert(it) }

    // ---------- controls ----------

    fun cancel(id: String) {
        // An incoming LAN transfer must be stopped on the receiver too. Receiving progress creates a Control
        // as well, so this can't depend on whether one exists.
        app.discovery.server?.cancelActive(id)
        val c = controls[id]
        if (c == null) {
            get(id)?.let { finish(id, TStatus.CANCELLED, "Cancelled") }
            return
        }
        c.paused.set(false)
        c.cancelled.set(true)
        c.cancelCalls()
        if (c.job?.isActive != true) {
            // A paused direct send still holds a session on the receiver: tell it we're done.
            val peer = c.peer
            val st = c.direct
            if (peer != null && st != null) scope.launch(Dispatchers.IO) { LanClient.cancel(peer, st.sessionId) }
            finish(id, TStatus.CANCELLED, "Cancelled")
        }
    }

    fun pause(id: String) {
        val c = controls[id] ?: return
        c.paused.set(true)
        c.cancelled.set(true)
        c.cancelCalls()
    }

    fun resume(id: String) {
        val c = controls[id] ?: return
        val t = get(id) ?: return
        c.cancelled.set(false)
        c.paused.set(false)
        update(id) { it.copy(paused = false) }
        if (t.method == Method.DIRECT && c.peer != null) runDirect(id, c.peer!!, null) else c.restart?.invoke()
    }

    private fun cancelled(id: String) = controls[id]?.cancelled?.get() == true

    // ---------- direct (LAN) sending ----------

    fun sendDirect(peer: Peer, files: List<TFile>, fallbackDeviceId: String? = null): String {
        val t = newTransfer(true, Method.DIRECT, peer.alias, files, TStatus.CONNECTING, canPause = peer.ferry)
        retryable[t.id] = { sendDirect(peer, files, fallbackDeviceId) }
        linkable[t.id] = files
        runDirect(t.id, peer, fallbackDeviceId)
        return t.id
    }

    private fun runDirect(id: String, peerIn: Peer, fallbackDeviceId: String?) {
        val c = controls.getOrPut(id) { Control() }
        c.peer = peerIn
        c.job = scope.launch(Dispatchers.IO) {
            var peer = peerIn
            try {
                status(id, TStatus.CONNECTING, "Connecting to ${peer.alias}…")
                // Pinned to the certificate seen when the peer was found; a different certificate is refused.
                val info = LanClient.info(peer.ip, peer.port, peer.certPin, peer.source, peer.https)
                peer = peer.copy(ferry = info.ferry, https = info.https, certPin = info.certPin, fingerprint = peer.fingerprint.ifEmpty { info.fingerprint })
                c.peer = peer
                update(id) { it.copy(canPause = peer.ferry) }
                val files = get(id)?.files ?: return@launch
                var st = c.direct
                if (st == null) {
                    status(id, TStatus.WAITING, "Waiting for ${peer.alias} to accept…")
                    val self = LocalSend.selfInfo(app.prefs.alias, app.certs.fingerprint, app.discovery.port)
                    val filesJson = JSONObject().apply {
                        files.forEach { f ->
                            put(f.id, JSONObject().put("id", f.id).put("fileName", f.name).put("size", f.size).put("fileType", f.mime))
                        }
                    }
                    var pin: String? = null
                    var prepared: LanClient.Prepared?
                    while (true) {
                        try {
                            prepared = LanClient.prepare(peer, self, filesJson, pin) { c.calls += it }
                            break
                        } catch (e: PeerException) {
                            if (e.status != 401) throw e
                            pin = askPin(id, peer.alias, pin != null) ?: throw CancelledException()
                        }
                    }
                    if (prepared == null) {
                        finish(id, TStatus.COMPLETED); return@launch
                    }
                    st = DirectState(prepared.sessionId, prepared.tokens)
                    c.direct = st
                }
                status(id, TStatus.TRANSFERRING)
                // Several files at once keep the link busy through per-file handshakes and slow storage reads.
                val gate = Semaphore(if (peer.ferry) PARALLEL_FILES else 2)
                coroutineScope {
                    for (f in files) {
                        if (st.sent.containsKey(f.id)) continue
                        val token = st.tokens[f.id] ?: continue // receiver skipped this file
                        launch {
                            gate.withPermit {
                                try {
                                    st.sent[f.id] = sendFile(id, peer, st, f, token, c)
                                } catch (e: Exception) {
                                    c.cancelCalls() // unblock the sibling uploads so the whole send stops promptly
                                    throw e
                                }
                            }
                        }
                    }
                }
                if (peer.ferry) {
                    status(id, TStatus.VERIFYING, "Verifying integrity…")
                    for ((fid, sha) in st.sent) LanClient.verify(peer, st.sessionId, fid, st.tokens.getValue(fid), sha)
                }
                finish(id, TStatus.COMPLETED)
            } catch (e: Exception) {
                val paused = c.paused.get()
                when {
                    paused -> update(id) { it.copy(status = TStatus.INTERRUPTED, paused = true, speed = 0.0, note = "Paused") }
                    c.cancelled.get() || e is CancelledException -> {
                        c.direct?.let { LanClient.cancel(peer, it.sessionId) }
                        finish(id, TStatus.CANCELLED, "Cancelled")
                    }
                    // The receiver dropped the session (its user cancelled): nothing more to send.
                    e is PeerException && e.status == 403 && c.direct != null -> finish(id, TStatus.CANCELLED, "${peer.alias} cancelled the transfer.")
                    e is PeerException && e.status == 403 -> finish(id, TStatus.REJECTED, "${peer.alias} declined the files.")
                    e is PeerException && e.status == 409 -> finish(id, TStatus.FAILED, "${peer.alias} is busy with another transfer. Try again in a moment.")
                    e is PeerException && e.message?.contains("checksum", true) == true ->
                        finish(id, TStatus.FAILED, "The file arrived corrupted (checksum mismatch). Please retry.")
                    else -> {
                        android.util.Log.w("Ferry", "direct send to ${peer.alias} failed", e)
                        c.direct?.let { LanClient.cancel(peer, it.sessionId) } // free the receiver: LocalSend accepts one session at a time
                        val why = unreachableReason(e, peer)
                        val fallback = fallbackDeviceId?.takeIf { app.server.online && app.prefs.method != "direct" && c.direct == null }
                        if (fallback != null) {
                            update(id) { it.copy(method = Method.SERVER, note = "$why Sending via your server instead.") }
                            controls.remove(id)
                            runServerRelay(id, fallback, peer.alias)
                        } else finish(id, TStatus.FAILED, why)
                    }
                }
            }
        }
    }

    private fun unreachableReason(e: Exception, peer: Peer): String = when {
        e is javax.net.ssl.SSLPeerUnverifiedException || e.cause is java.security.cert.CertificateException || e is javax.net.ssl.SSLHandshakeException ->
            "${peer.alias}'s identity changed or couldn't be verified. For your safety the connection was refused."
        e is ConnectException || e is NoRouteToHostException || e is SocketTimeoutException ->
            "Couldn't connect directly to ${peer.alias} (${e.message ?: e.javaClass.simpleName}). You may be on different networks, or this Wi-Fi blocks device-to-device traffic (client isolation, guest or corporate network)."
        else -> "Connection to ${peer.alias} was lost (${e.message ?: "network error"})."
    }

    /** Streams one file; for Ferry peers, reconnects and resumes from the receiver's offset after drops. */
    private suspend fun sendFile(id: String, peer: Peer, st: DirectState, f: TFile, token: String, c: Control): String {
        var attempt = 0
        var resuming = false
        while (true) {
            if (c.cancelled.get()) throw CancelledException()
            try {
                val offset = if (resuming && peer.ferry) LanClient.offset(peer, st.sessionId, f.id, token) else 0L // 403 = session gone
                val md = MessageDigest.getInstance("SHA-256")
                ctx.contentResolver.openInputStream(f.uri!!)?.use { input ->
                    hashPrefix(input, md, offset)
                    progress(id, f.id, offset)
                    val body = StreamBody(input, f.size - offset, md, { c.cancelled.get() }) { progress(id, f.id, offset + it) }
                    val call = LanClient.client(peer.certPin, 120).newCall(LanClient.uploadRequest(peer, st.sessionId, f.id, token, offset, body))
                    c.calls += call
                    try {
                        call.execute().use { r ->
                            if (!r.isSuccessful) throw PeerException(r.code, r.body?.string()?.let { runCatching { JSONObject(it).optString("message") }.getOrNull() } ?: "HTTP ${r.code}")
                        }
                    } finally {
                        c.calls -= call
                    }
                } ?: throw IOException("Can't read \"${f.name}\". It may have been moved or deleted.")
                if (resuming) status(id, TStatus.TRANSFERRING)
                return md.digest().hex()
            } catch (e: IOException) {
                if (c.cancelled.get()) throw CancelledException()
                if (e is PeerException && e.status in setOf(400, 403, 404)) throw e
                if (!peer.ferry || ++attempt > 12) throw e
                resuming = true
                status(id, TStatus.INTERRUPTED, "Connection lost — reconnecting (attempt $attempt)…")
                // Wait for a LAN, then back off; the receiver keeps partial data for 10 minutes.
                withTimeoutOrNull(120_000) { app.net.state.first { it.lan } }
                delay(minOf(20_000L, 1000L shl minOf(attempt, 5)))
            }
        }
    }

    private suspend fun askPin(id: String, peer: String, wrong: Boolean): String? {
        val d = CompletableDeferred<String?>()
        pinAnswer = d
        _pin.value = PinRequest(id, peer, wrong)
        status(id, TStatus.WAITING, "PIN required by $peer")
        val r = withTimeoutOrNull(120_000) { d.await() }
        _pin.value = null
        return r
    }

    fun answerPin(pin: String?) {
        pinAnswer?.complete(pin)
    }

    // ---------- server relay ----------

    /** Sends to one of my devices through my server (store-and-forward). */
    fun sendViaServer(deviceId: String, deviceName: String, files: List<TFile>, waitForWifi: Boolean = false): String {
        val t = newTransfer(true, Method.SERVER, deviceName, files, TStatus.CREATED)
        runServerRelay(t.id, deviceId, deviceName, waitForWifi)
        return t.id
    }

    private fun runServerRelay(id: String, deviceId: String, deviceName: String, waitForWifi: Boolean = false) {
        val c = controls.getOrPut(id) { Control() }
        val files0 = get(id)?.files.orEmpty()
        retryable[id] = { sendViaServer(deviceId, deviceName, files0) }
        c.restart = { runServerRelay(id, deviceId, deviceName) }
        c.job = scope.launch(Dispatchers.IO) {
            var serverId = get(id)?.serverTransferId.orEmpty()
            try {
                if (waitForWifi && !app.net.state.value.wifi) {
                    status(id, TStatus.WAITING, "Waiting for Wi-Fi…")
                    app.net.state.first { it.wifi }
                }
                val api = app.server.api() ?: throw IllegalStateException("Sign in to your server to send via server.")
                val files = get(id)!!.files
                if (serverId.isEmpty()) {
                    serverId = api.createTransfer(deviceId, files.size, files.sumOf { it.size }).getString("id")
                    update(id) { it.copy(serverTransferId = serverId) }
                }
                status(id, TStatus.TRANSFERRING, "Uploading to your server…")
                for (f in get(id)!!.files) {
                    if (f.serverFileId.isNotEmpty()) continue // finished before a pause
                    val fid = api.tusUpload(ctx, f.uri!!, f.name, f.size, mapOf("transferId" to serverId), "tr:$serverId:${f.id}",
                        { progress(id, f.id, it) }, { c.cancelled.get() }).first
                    markUploaded(id, f.id, fid)
                }
                api.patchTransfer(serverId, "waiting")
                status(id, TStatus.WAITING, "On your server — $deviceName will get it when it's online.")
                watchServerTransfer(id, serverId)
            } catch (e: Exception) {
                when {
                    c.paused.get() -> update(id) { it.copy(status = TStatus.INTERRUPTED, paused = true, speed = 0.0, note = "Paused — resume any time") }
                    c.cancelled.get() || e is CancelledException -> {
                        if (serverId.isNotEmpty()) runCatching { app.server.api()?.patchTransfer(serverId, "cancelled") }
                        finish(id, TStatus.CANCELLED, "Cancelled")
                    }
                    else -> finish(id, TStatus.FAILED, ServerManager.friendly(e) + " Your file is safe — you can retry.")
                }
            }
        }
    }

    /** Follows a relayed transfer until the target device finishes it (polls the server). */
    private suspend fun watchServerTransfer(id: String, serverId: String) {
        val start = System.currentTimeMillis()
        while (System.currentTimeMillis() - start < 60 * 60_000) {
            delay(if (System.currentTimeMillis() - start < 5 * 60_000) 4000 else 30_000)
            val s = runCatching { app.server.api()?.get("/api/v1/transfers/$serverId") }.getOrNull() ?: continue
            val st = TStatus.of(s.optString("status"))
            if (st.final) {
                finish(id, st, s.optString("error")); return
            }
            if (st == TStatus.TRANSFERRING) status(id, TStatus.WAITING, "${get(id)?.peer} is downloading…")
        }
        // Still undelivered after an hour: keep it in history; the server delivers it later.
        val t = get(id) ?: return
        _active.update { l -> l.filter { it.id != id } }
        persist()
        app.history.add(t.copy(note = "Waiting on the server for delivery"))
    }

    /** Uploads files to my server and creates a share link (works for people without the app). */
    fun shareAsLink(files: List<TFile>, expiresIn: Long, maxDownloads: Int, password: String?, waitForWifi: Boolean = false): String {
        val t = newTransfer(true, Method.LINK, "Link", files, TStatus.CREATED)
        retryable[t.id] = { shareAsLink(files, expiresIn, maxDownloads, password, waitForWifi) }
        runUpload(t.id, waitForWifi, "link:${t.id}") { api, ids ->
            status(t.id, TStatus.TRANSFERRING, "Creating link…")
            finish(t.id, TStatus.COMPLETED, shareUrl = api.createShare(ids, emptyList(), expiresIn, maxDownloads, password).linkUrl())
        }
        return t.id
    }

    /** Uploads into a folder of my server storage. */
    fun uploadToFolder(files: List<TFile>, folderId: String, folderName: String, onDone: () -> Unit = {}): String {
        val t = newTransfer(true, Method.SERVER, folderName, files, TStatus.CREATED)
        retryable[t.id] = { uploadToFolder(files, folderId, folderName, onDone) }
        runUpload(t.id, false, "up:${t.id}", folderId) { _, _ ->
            finish(t.id, TStatus.COMPLETED); onDone()
        }
        return t.id
    }

    /** Shared tus upload runner with pause/resume (same id) and friendly failures. */
    private fun runUpload(id: String, waitForWifi: Boolean, keyPrefix: String, folderId: String? = null,
                          done: (dev.ferry.app.server.ServerApi, List<String>) -> Unit) {
        val c = controls.getOrPut(id) { Control() }
        c.restart = { runUpload(id, false, keyPrefix, folderId, done) }
        c.job = scope.launch(Dispatchers.IO) {
            try {
                if (waitForWifi && !app.net.state.value.wifi) {
                    status(id, TStatus.WAITING, "Waiting for Wi-Fi…"); app.net.state.first { it.wifi }
                }
                val api = app.server.api() ?: throw IllegalStateException("Sign in to a Ferry server first.")
                val folder = folderId ?: api.ensureFolder("Sent")
                status(id, TStatus.TRANSFERRING, "Uploading to your server…")
                val ids = get(id)!!.files.map { f ->
                    f.serverFileId.ifEmpty {
                        api.tusUpload(ctx, f.uri!!, f.name, f.size, mapOf("folderId" to folder), "$keyPrefix:${f.id}", { progress(id, f.id, it) }, { c.cancelled.get() })
                            .first.also { markUploaded(id, f.id, it) }
                    }
                }
                done(api, ids)
            } catch (e: Exception) {
                when {
                    c.paused.get() -> update(id) { it.copy(status = TStatus.INTERRUPTED, paused = true, speed = 0.0, note = "Paused — resume any time") }
                    c.cancelled.get() || e is CancelledException -> {
                        get(id)?.files?.forEach { f -> app.server.api()?.let { api -> runCatching { api.cancelUpload("$keyPrefix:${f.id}") } } }
                        finish(id, TStatus.CANCELLED, "Cancelled")
                    }
                    else -> finish(id, TStatus.FAILED, ServerManager.friendly(e) + " Your file is safe — you can retry.")
                }
            }
        }
    }

    /** Downloads files from my server into Downloads/Ferry (with resume and verification). */
    fun downloadFromServer(files: List<JSONObject>, peer: String, serverTransferId: String = "", existingId: String? = null): String {
        val tfiles = files.map { TFile(it.getString("id"), it.getString("name"), it.getLong("size"), it.optString("mime"), sha256 = it.optString("sha256"), serverFileId = it.getString("id")) }
        val id = existingId ?: UUID.randomUUID().toString()
        upsert(Transfer(id, false, Method.SERVER, peer, tfiles, TStatus.CONNECTING, canPause = true, serverTransferId = serverTransferId))
        val c = controls.getOrPut(id) { Control() }
        retryable[id] = { downloadFromServer(files, peer, serverTransferId) }
        c.restart = { downloadFromServer(files, peer, serverTransferId, id) }
        c.job = scope.launch(Dispatchers.IO) {
            try {
                val api = app.server.api() ?: throw IllegalStateException("Sign in to your server first.")
                if (serverTransferId.isNotEmpty()) api.patchTransfer(serverTransferId, "transferring")
                status(id, TStatus.TRANSFERRING)
                var bytes = 0L
                for (f in tfiles) {
                    if (app.prefs.getKV("dlok:${f.serverFileId}") != null) { // saved before a pause/restart
                        progress(id, f.id, f.size); bytes += f.size; continue
                    }
                    val key = "dl:${f.serverFileId}"
                    val uri = app.prefs.getKV(key)?.let(Uri::parse)?.takeIf { runCatching { Storage.length(ctx, it); true }.getOrDefault(false) }
                        ?: Storage.createPending(ctx, f.name, f.mime).also { app.prefs.setKV(key, it.toString()) }
                    api.download(ctx, f.serverFileId, uri, f.size, f.sha256, { progress(id, f.id, it) }, { c.cancelled.get() })
                    Storage.publish(ctx, uri)
                    app.prefs.setKV(key, null)
                    app.prefs.setKV("dlok:${f.serverFileId}", "1")
                    bytes += f.size
                }
                tfiles.forEach { app.prefs.setKV("dlok:${it.serverFileId}", null) }
                if (serverTransferId.isNotEmpty()) api.patchTransfer(serverTransferId, "completed", bytes)
                finish(id, TStatus.COMPLETED)
            } catch (e: Exception) {
                when {
                    c.paused.get() -> update(id) { it.copy(status = TStatus.INTERRUPTED, paused = true, note = "Paused") }
                    c.cancelled.get() || e is CancelledException -> {
                        tfiles.forEach { f -> app.prefs.getKV("dl:${f.serverFileId}")?.let { Storage.delete(ctx, Uri.parse(it)) }; app.prefs.setKV("dl:${f.serverFileId}", null) }
                        if (serverTransferId.isNotEmpty()) runCatching { app.server.api()?.patchTransfer(serverTransferId, "cancelled") }
                        finish(id, TStatus.CANCELLED, "Cancelled")
                    }
                    else -> {
                        if (serverTransferId.isNotEmpty()) runCatching { app.server.api()?.patchTransfer(serverTransferId, "interrupted", error = e.message) }
                        finish(id, TStatus.FAILED, ServerManager.friendly(e) + " Partial data is kept — retry to resume.")
                    }
                }
            }
        }
        return id
    }

    // ---------- incoming ----------

    /** Blocking variant for the LAN receiver thread. */
    fun askIncoming(req: IncomingRequest): Boolean = runBlocking { askIncomingSuspend(req) }

    suspend fun askIncomingSuspend(req: IncomingRequest): Boolean {
        if (req.source == Method.DIRECT && req.fingerprint.isNotEmpty() && app.prefs.autoAccept(req.fingerprint)) return true
        if (req.ownAccount && app.prefs.autoAcceptOwn) return true
        val d = CompletableDeferred<Boolean>()
        decisions[req.id] = d
        _incoming.update { it + req }
        Notifier.incoming(ctx, req)
        val r = withTimeoutOrNull(90_000) { d.await() } ?: false
        decisions.remove(req.id)
        _incoming.update { l -> l.filter { it.id != req.id } }
        Notifier.clearIncoming(ctx)
        return r
    }

    fun respond(id: String, accept: Boolean, trustAlways: Boolean) {
        val req = _incoming.value.find { it.id == id }
        if (accept && trustAlways && req != null && req.fingerprint.isNotEmpty()) app.prefs.trust(req.fingerprint, req.alias)
        decisions[id]?.complete(accept)
    }

    // LAN receiver callbacks
    fun lanReceiveStarted(t: Transfer) = upsert(t)
    fun lanReceiveProgress(id: String, fileId: String, bytes: Long) = progress(id, fileId, bytes)
    fun lanReceiveStatus(id: String, s: TStatus, note: String) {
        if (s == TStatus.FAILED) finish(id, s, note) else status(id, s, note)
    }
    fun lanReceiveFinished(id: String, s: TStatus, error: String, @Suppress("UNUSED_PARAMETER") alias: String) {
        if (get(id) != null) finish(id, s, error) // already finished (e.g. cancelled here) → leave history alone
    }

    /** Checks my server inbox for files sent to this device (from the web or my other devices). */
    fun pollInbox() {
        val api = app.server.api() ?: return
        scope.launch(Dispatchers.IO) {
            val list = runCatching { api.get("/api/v1/inbox").getJSONArray("transfers") }.getOrNull() ?: return@launch
            for (i in 0 until list.length()) {
                val tr = list.getJSONObject(i)
                val id = tr.getString("id")
                if (!seenInbox.add(id) || _active.value.any { it.serverTransferId == id }) continue
                val filesJ = tr.optJSONArray("files") ?: continue
                val files = (0 until filesJ.length()).map { filesJ.getJSONObject(it) }
                val from = tr.optString("sourceDeviceName").ifEmpty { "Web browser" }
                val status = tr.optString("status")
                scope.launch(Dispatchers.IO) {
                    // A transfer this device had already accepted (e.g. before a restart) resumes without asking again.
                    val accepted = status != "waiting" || askIncomingSuspend(IncomingRequest(id, from, "", "", trusted = true, ownAccount = true,
                        files = files.map { it.getString("name") to it.getLong("size") }, source = Method.SERVER))
                    if (accepted) downloadFromServer(files, from, id)
                    else runCatching { api.patchTransfer(id, "rejected") }
                }
            }
        }
    }
}
