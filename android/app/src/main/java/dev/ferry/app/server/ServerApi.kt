package dev.ferry.app.server

import android.content.Context
import android.net.Uri
import android.util.Base64
import dev.ferry.app.BuildConfig
import dev.ferry.app.FerryApp
import dev.ferry.app.transfer.Storage
import dev.ferry.app.util.hex
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okio.BufferedSink
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.io.InputStream
import java.security.MessageDigest
import java.util.concurrent.TimeUnit

class ApiException(val status: Int, val code: String, message: String) : IOException(message)

class CancelledException : IOException("Cancelled")

/** Thin client for Ferry's /api/v1. All calls are blocking and must run on Dispatchers.IO (use [call]). */
class ServerApi(baseUrl: String, private val token: String?) {
    val base = baseUrl.trimEnd('/')

    companion object {
        const val API_VERSION = 1
        val CLIENT = "android/${BuildConfig.VERSION_NAME} api=$API_VERSION"
        val http: OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(10, TimeUnit.SECONDS).readTimeout(60, TimeUnit.SECONDS).writeTimeout(60, TimeUnit.SECONDS)
            .retryOnConnectionFailure(true).build()
        val quick: OkHttpClient = http.newBuilder().connectTimeout(4, TimeUnit.SECONDS).readTimeout(8, TimeUnit.SECONDS).build()
        private val JSON = "application/json".toMediaType()

        /** Normalises what a user typed ("myserver:8080") into a URL. */
        fun normalizeUrl(input: String): String {
            var u = input.trim().trimEnd('/')
            if (!u.startsWith("http://") && !u.startsWith("https://")) u = "https://$u"
            return u
        }
    }

    suspend fun <T> call(block: ServerApi.() -> T): T = withContext(Dispatchers.IO) { block() }

    private fun req(path: String) = Request.Builder().url(if (path.startsWith("http")) path else base + path)
        .header("X-Ferry-Client", CLIENT).apply { token?.let { header("Authorization", "Bearer $it") } }

    private fun parse(r: Response): JSONObject {
        r.use {
            val body = it.body?.string().orEmpty()
            val json = runCatching { JSONObject(body) }.getOrNull()
            if (!it.isSuccessful) throw toError(it.code, json)
            return json ?: JSONObject()
        }
    }

    private fun toError(code: Int, json: JSONObject?): ApiException {
        val e = json?.optJSONObject("error")
        if (e != null) return ApiException(code, e.optString("code"), e.optString("message"))
        return when (code) {
            502, 503, 504 -> ApiException(code, "unavailable", "The server is temporarily unavailable. Please try again shortly.")
            426 -> ApiException(code, "client_outdated", "This app is too old for the server. Please update Ferry.")
            else -> ApiException(code, "http_$code", "The server returned an unexpected error ($code).")
        }
    }

    fun get(path: String, client: OkHttpClient = http) = parse(client.newCall(req(path).get().build()).execute())
    fun post(path: String, body: JSONObject = JSONObject()) = parse(http.newCall(req(path).post(body.toString().toRequestBody(JSON)).build()).execute())
    fun patch(path: String, body: JSONObject) = parse(http.newCall(req(path).patch(body.toString().toRequestBody(JSON)).build()).execute())
    fun put(path: String, body: JSONObject) = parse(http.newCall(req(path).put(body.toString().toRequestBody(JSON)).build()).execute())
    fun delete(path: String) = parse(http.newCall(req(path).delete().build()).execute())

    fun info() = get("/api/v1/info", quick)

    fun login(email: String, password: String, deviceName: String, deviceId: String, code: String = "") = post("/api/v1/auth/login", JSONObject()
        .put("email", email).put("password", password).apply { if (code.isNotEmpty()) put("code", code) }
        .put("device", JSONObject().put("name", deviceName).put("platform", "android").put("appVersion", BuildConfig.VERSION_NAME).put("deviceId", deviceId)))

    fun logout() = runCatching { post("/api/v1/auth/logout") }

    fun list(folder: String, q: String = "") = get("/api/v1/files?folder=" + enc(folder) + (if (q.isNotEmpty()) "&q=" + enc(q) else ""))

    fun createShare(fileIds: List<String>, folderIds: List<String>, expiresIn: Long, maxDownloads: Int, password: String?) =
        post("/api/v1/shares", JSONObject().put("kind", "download").put("fileIds", JSONArray(fileIds)).put("folderIds", JSONArray(folderIds))
            .put("expiresIn", expiresIn).put("maxDownloads", maxDownloads).apply { if (!password.isNullOrEmpty()) put("password", password) })

    fun createUploadLink(name: String, expiresIn: Long) = post("/api/v1/shares", JSONObject().put("kind", "upload").put("name", name).put("expiresIn", expiresIn))

    fun ensureFolder(name: String, parent: String = ""): String = try {
        post("/api/v1/folders", JSONObject().put("name", name).put("parentId", parent)).getString("id")
    } catch (e: ApiException) {
        if (e.status != 409) throw e
        val folders = list(parent).getJSONArray("folders")
        (0 until folders.length()).map { folders.getJSONObject(it) }.first { it.getString("name") == name }.getString("id")
    }

    fun createTransfer(targetDeviceId: String, fileCount: Int, totalBytes: Long) = post("/api/v1/transfers", JSONObject()
        .put("targetDeviceId", targetDeviceId).put("fileCount", fileCount).put("totalBytes", totalBytes))

    /** Records a LAN transfer in the account history so web and app agree. */
    fun reportTransfer(sent: Boolean, peer: String, status: String, fileCount: Int, total: Long, error: String) = runCatching {
        post("/api/v1/transfers", JSONObject().put("direction", if (sent) "sent" else "received").put("method", "direct").put("peer", peer)
            .put("status", status).put("fileCount", fileCount).put("totalBytes", total).put("bytesDone", if (status == "completed") total else 0).put("error", error))
    }

    fun patchTransfer(id: String, status: String, bytesDone: Long? = null, error: String? = null) = patch("/api/v1/transfers/$id", JSONObject()
        .put("status", status).apply { bytesDone?.let { put("bytesDone", it) }; error?.let { put("error", it) } })

    private fun enc(s: String) = java.net.URLEncoder.encode(s, "UTF-8")

    // ---------- resumable upload (tus 1.0) ----------

    /**
     * Uploads [uri] with resume support. Progress survives app restarts via [resumeKey].
     * Returns (fileId, sha256). The local SHA-256 is compared with the server's.
     */
    fun tusUpload(
        ctx: Context, uri: Uri, name: String, size: Long, meta: Map<String, String>, resumeKey: String,
        onProgress: (Long) -> Unit, cancelled: () -> Boolean,
    ): Pair<String, String> {
        val prefs = FerryApp.app.prefs
        var attempt = 0
        while (true) {
            if (cancelled()) throw CancelledException()
            try {
                var location = prefs.getKV(resumeKey)
                var offset = 0L
                if (location != null) {
                    http.newCall(req(location).head().header("Tus-Resumable", "1.0.0").build()).execute().use { r ->
                        when {
                            r.code == 200 -> {
                                offset = r.header("Upload-Offset")?.toLongOrNull() ?: 0
                                val done = r.header("Ferry-File-Id")
                                if (done != null && offset == size) {
                                    prefs.setKV(resumeKey, null); onProgress(size)
                                    return done to ""
                                }
                            }
                            r.code in 400..499 -> location = null
                            else -> throw IOException("Server returned ${r.code}")
                        }
                    }
                }
                if (location == null) {
                    val m = (meta + ("filename" to name)).entries.joinToString(",") { (k, v) ->
                        k + " " + Base64.encodeToString(v.toByteArray(), Base64.NO_WRAP)
                    }
                    http.newCall(req("/api/v1/uploads").post(ByteArray(0).toRequestBody(null)).header("Tus-Resumable", "1.0.0")
                        .header("Upload-Length", size.toString()).header("Upload-Metadata", m).build()).execute().use { r ->
                        if (r.code != 201) throw toError(r.code, runCatching { JSONObject(r.body?.string().orEmpty()) }.getOrNull())
                        val loc = r.header("Location") ?: throw IOException("Server did not return an upload location")
                        location = if (loc.startsWith("http")) loc else base + loc
                        prefs.setKV(resumeKey, location)
                        if (size == 0L) {
                            prefs.setKV(resumeKey, null)
                            return (r.header("Ferry-File-Id") ?: "") to (r.header("Ferry-Sha256") ?: "")
                        }
                    }
                }
                val md = MessageDigest.getInstance("SHA-256")
                ctx.contentResolver.openInputStream(uri)?.use { input ->
                    hashPrefix(input, md, offset)
                    onProgress(offset)
                    var off = offset
                    while (off < size) {
                        val len = minOf(16L * 1024 * 1024, size - off)
                        val body = StreamBody(input, len, md, cancelled) { onProgress(off + it) }
                        http.newCall(req(location!!).patch(body).header("Tus-Resumable", "1.0.0").header("Upload-Offset", off.toString()).build()).execute().use { r ->
                            if (r.code != 204) throw toError(r.code, runCatching { JSONObject(r.body?.string().orEmpty()) }.getOrNull())
                            off = r.header("Upload-Offset")?.toLongOrNull() ?: (off + len)
                            attempt = 0
                            if (off >= size) {
                                prefs.setKV(resumeKey, null)
                                val local = md.digest().hex()
                                val server = r.header("Ferry-Sha256") ?: ""
                                if (server.isNotEmpty() && server != local) throw ApiException(460, "checksum_mismatch", "The file was corrupted in transit. Please retry.")
                                return (r.header("Ferry-File-Id") ?: "") to server
                            }
                        }
                    }
                } ?: throw IOException("Can't read the file. It may have been moved or deleted.")
            } catch (e: CancelledException) {
                throw e
            } catch (e: ApiException) {
                if (e.status in 400..499 && e.status !in setOf(409, 423, 429)) {
                    prefs.setKV(resumeKey, null); throw e
                }
                if (++attempt > 8) throw e
                backoff(attempt, cancelled)
            } catch (e: IOException) {
                if (cancelled()) throw CancelledException()
                if (++attempt > 8) throw IOException("Connection lost. The upload can be resumed later.", e)
                backoff(attempt, cancelled)
            }
        }
    }

    fun cancelUpload(resumeKey: String) {
        val loc = FerryApp.app.prefs.getKV(resumeKey) ?: return
        runCatching { http.newCall(req(loc).delete().header("Tus-Resumable", "1.0.0").build()).execute().close() }
        FerryApp.app.prefs.setKV(resumeKey, null)
    }

    /**
     * Downloads a file into a pending Downloads entry with Range-resume, verifying SHA-256.
     * [dest] may already contain a partial download from an earlier attempt.
     */
    fun download(ctx: Context, fileId: String, dest: Uri, size: Long, expectedSha: String, onProgress: (Long) -> Unit, cancelled: () -> Boolean) {
        var attempt = 0
        while (true) {
            if (cancelled()) throw CancelledException()
            try {
                var have = Storage.length(ctx, dest)
                if (have > size) have = 0
                val md = MessageDigest.getInstance("SHA-256")
                if (have > 0) ctx.contentResolver.openInputStream(dest)?.use { hashPrefix(it, md, have) }
                val r = http.newCall(req("/api/v1/files/$fileId/content").apply { if (have > 0) header("Range", "bytes=$have-") }.get().build()).execute()
                r.use {
                    if (r.code == 200 && have > 0) {
                        have = 0; md.reset()
                    } else if (r.code != 206 && r.code != 200) {
                        throw toError(r.code, runCatching { JSONObject(r.body?.string().orEmpty()) }.getOrNull())
                    }
                    val out = if (have == 0L) Storage.openTruncate(ctx, dest) else Storage.openAppend(ctx, dest)
                    out.use { o ->
                        val input = r.body!!.byteStream()
                        val buf = ByteArray(256 * 1024)
                        var got = have
                        onProgress(got)
                        while (true) {
                            if (cancelled()) throw CancelledException()
                            val n = input.read(buf)
                            if (n < 0) break
                            o.write(buf, 0, n); md.update(buf, 0, n); got += n
                            onProgress(got)
                        }
                        if (got != size) throw IOException("Download ended early")
                    }
                    val sha = md.digest().hex()
                    val expect = expectedSha.ifEmpty { r.header("X-Content-SHA256") ?: "" }
                    if (expect.isNotEmpty() && expect != sha) {
                        Storage.openTruncate(ctx, dest).close()
                        throw ApiException(460, "checksum_mismatch", "The downloaded file was corrupted. Please retry.")
                    }
                    return
                }
            } catch (e: CancelledException) {
                throw e
            } catch (e: ApiException) {
                if (e.status in 400..499 && e.status != 429) throw e
                if (++attempt > 8) throw e
                backoff(attempt, cancelled)
            } catch (e: IOException) {
                if (cancelled()) throw CancelledException()
                if (++attempt > 8) throw IOException("Connection lost. The download can be resumed later.", e)
                backoff(attempt, cancelled)
            }
        }
    }
}

/** Reads and hashes the first [n] bytes of [input] (used when resuming). */
fun hashPrefix(input: InputStream, md: MessageDigest, n: Long) {
    val buf = ByteArray(256 * 1024)
    var left = n
    while (left > 0) {
        val r = input.read(buf, 0, minOf(buf.size.toLong(), left).toInt())
        if (r < 0) throw IOException("File is shorter than expected")
        md.update(buf, 0, r); left -= r
    }
}

/** Streams exactly [len] bytes from [input], hashing and reporting progress as they are written. */
class StreamBody(
    private val input: InputStream, private val len: Long, private val md: MessageDigest,
    private val cancelled: () -> Boolean, private val progress: (Long) -> Unit,
) : RequestBody() {
    override fun contentType() = "application/offset+octet-stream".toMediaType()
    override fun contentLength() = len
    override fun isOneShot() = true
    override fun writeTo(sink: BufferedSink) {
        val buf = ByteArray(256 * 1024)
        var left = len
        var sent = 0L
        var lastReport = 0L
        while (left > 0) {
            if (cancelled()) throw CancelledException()
            val r = input.read(buf, 0, minOf(buf.size.toLong(), left).toInt())
            if (r < 0) throw IOException("File changed while uploading")
            sink.write(buf, 0, r)
            md.update(buf, 0, r)
            left -= r; sent += r
            if (sent - lastReport > 128 * 1024 || left == 0L) {
                progress(sent); lastReport = sent
            }
        }
    }
}

/** Waits before retry [attempt] (exponential, max 30 s), but returns early — with [CancelledException] — when cancelled. */
fun backoff(attempt: Int, cancelled: () -> Boolean) {
    val until = System.currentTimeMillis() + minOf(30_000L, 1000L shl minOf(attempt, 5))
    while (System.currentTimeMillis() < until) {
        if (cancelled()) throw CancelledException()
        Thread.sleep(250)
    }
}
