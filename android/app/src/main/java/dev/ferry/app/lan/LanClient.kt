package dev.ferry.app.lan

import dev.ferry.app.data.Peer
import okhttp3.Call
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.io.IOException
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

class PeerException(val status: Int, message: String) : IOException(message)

/**
 * HTTP(S) client for LocalSend peers. Peers use self-signed certificates, so trust is established by
 * pinning: the certificate seen on first contact (or the fingerprint in a scanned QR code) must match
 * on every later connection. Advertised fingerprints are identifiers, not always certificate hashes.
 */
object LanClient {
    private val JSON = "application/json".toMediaType()

    // One client per peer identity and timeout, so files and calls reuse TLS connections instead of re-handshaking.
    private val clients = java.util.concurrent.ConcurrentHashMap<Pair<String, Long>, OkHttpClient>()

    fun client(expectedFingerprint: String, readTimeoutSec: Long = 30): OkHttpClient =
        clients.getOrPut(expectedFingerprint to readTimeoutSec) { build(expectedFingerprint, readTimeoutSec, null) }

    private fun build(expectedFingerprint: String, readTimeoutSec: Long, seen: ((String) -> Unit)?): OkHttpClient {
        val tm = object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {}
            override fun checkServerTrusted(chain: Array<out X509Certificate>, authType: String?) {
                val actual = Certs.fingerprintOf(chain[0])
                seen?.invoke(actual)
                if (expectedFingerprint.isNotEmpty() && !actual.equals(expectedFingerprint, true))
                    throw CertificateException("Device identity changed — the fingerprint doesn't match. Refusing to connect.")
            }
            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        }
        // Current LocalSend requires a client certificate (mTLS) and uses it as the sender's identity.
        val ssl = SSLContext.getInstance("TLS").apply { init(identityKeyManagers(), arrayOf(tm), SecureRandom()) }
        return OkHttpClient.Builder()
            .sslSocketFactory(ssl.socketFactory, tm)
            .protocols(listOf(okhttp3.Protocol.HTTP_1_1)) // LocalSend peers speak HTTP/1.1
            // Idle connections go before NanoHTTPD's 5 s keep-alive timeout would close them under us.
            .connectionPool(okhttp3.ConnectionPool(8, 3, TimeUnit.SECONDS))
            .hostnameVerifier { _, _ -> true } // identity is the pinned fingerprint, not a hostname
            .connectTimeout(4, TimeUnit.SECONDS)
            .readTimeout(readTimeoutSec, TimeUnit.SECONDS)
            .writeTimeout(60, TimeUnit.SECONDS)
            .build()
    }

    /** Always offers our own certificate: the default key manager would drop it because its issuer isn't one the server lists. */
    private fun identityKeyManagers(): Array<javax.net.ssl.KeyManager>? {
        val certs = runCatching { dev.ferry.app.FerryApp.app.certs }.getOrNull() ?: return null
        return arrayOf(object : javax.net.ssl.X509ExtendedKeyManager() {
            override fun getClientAliases(t: String?, i: Array<out java.security.Principal>?) = arrayOf("lan")
            override fun chooseClientAlias(t: Array<out String>?, i: Array<out java.security.Principal>?, s: java.net.Socket?) = "lan"
            override fun chooseEngineClientAlias(t: Array<out String>?, i: Array<out java.security.Principal>?, e: javax.net.ssl.SSLEngine?) = "lan"
            override fun getServerAliases(t: String?, i: Array<out java.security.Principal>?): Array<String>? = null
            override fun chooseServerAlias(t: String?, i: Array<out java.security.Principal>?, s: java.net.Socket?): String? = null
            override fun getCertificateChain(alias: String?) = arrayOf(certs.cert)
            override fun getPrivateKey(alias: String?) = certs.privateKey
        })
    }

    private fun check(r: okhttp3.Response): String {
        val body = r.body?.string().orEmpty()
        if (!r.isSuccessful) throw PeerException(r.code, runCatching { JSONObject(body).optString("message") }.getOrNull().orEmpty().ifEmpty { "HTTP ${r.code}" })
        return body
    }

    /**
     * Fetches a peer's identity. Tries HTTPS first, then HTTP (LocalSend with encryption off).
     * [pin] = expected certificate hash (from an earlier contact or a QR code); empty = trust on first use.
     */
    fun info(ip: String, port: Int, pin: String = "", source: String, httpsHint: Boolean? = null): Peer {
        val order = when (httpsHint) {
            true -> listOf(true); false -> listOf(false); null -> listOf(true, false)
        }
        var last: Exception? = null
        for (https in order) {
            try {
                val host = if (ip.contains(':')) "[$ip]" else ip
                val url = (if (https) "https" else "http") + "://$host:$port${LocalSend.API}/info"
                var seenCert = ""
                build(pin, 5) { seenCert = it }.newCall(Request.Builder().url(url).get().build()).execute().use { r ->
                    val o = JSONObject(check(r))
                    return LocalSend.peerFrom(o, ip, source, port, https).copy(port = port, https = https, certPin = seenCert)
                }
            } catch (e: Exception) {
                last = e
            }
        }
        throw last ?: IOException("unreachable")
    }

    fun register(peer: Peer, self: JSONObject) {
        client(peer.certPin, 5).newCall(Request.Builder().url(peer.baseUrl + LocalSend.API + "/register").post(self.toString().toRequestBody(JSON)).build())
            .execute().use { check(it) }
    }

    data class Prepared(val sessionId: String, val tokens: Map<String, String>)

    /**
     * Asks the receiver to accept files. Blocks while the receiver's user decides (up to ~2 minutes).
     * 204 → receiver needs nothing (returns null). 401 → PIN required, 403 → declined, 409 → busy.
     */
    fun prepare(peer: Peer, self: JSONObject, files: JSONObject, pin: String?, onCall: (Call) -> Unit): Prepared? {
        val url = peer.baseUrl + LocalSend.API + "/prepare-upload" + (if (!pin.isNullOrEmpty()) "?pin=" + java.net.URLEncoder.encode(pin, "UTF-8") else "")
        val call = client(peer.certPin, 130).newCall(Request.Builder().url(url)
            .post(JSONObject().put("info", self).put("files", files).toString().toRequestBody(JSON)).build())
        onCall(call)
        call.execute().use { r ->
            if (r.code == 204) return null
            val o = JSONObject(check(r))
            val t = o.getJSONObject("files")
            return Prepared(o.getString("sessionId"), t.keys().asSequence().associateWith { t.getString(it) })
        }
    }

    fun cancel(peer: Peer, sessionId: String) = runCatching {
        client(peer.certPin, 5).newCall(Request.Builder().url(peer.baseUrl + LocalSend.API + "/cancel?sessionId=$sessionId")
            .post(ByteArray(0).toRequestBody(null)).build()).execute().close()
    }

    /** Ferry extension: how many bytes of a file the receiver already has. */
    fun offset(peer: Peer, sessionId: String, fileId: String, token: String): Long {
        client(peer.certPin, 10).newCall(Request.Builder().url(peer.baseUrl + LocalSend.API + "/ferry/offset?sessionId=$sessionId&fileId=${enc(fileId)}&token=$token").get().build())
            .execute().use { return JSONObject(check(it)).optLong("offset", 0) }
    }

    /** Ferry extension: receiver compares our SHA-256 with what it stored. */
    fun verify(peer: Peer, sessionId: String, fileId: String, token: String, sha: String) {
        client(peer.certPin, 30).newCall(Request.Builder().url(peer.baseUrl + LocalSend.API + "/ferry/verify?sessionId=$sessionId&fileId=${enc(fileId)}&token=$token&sha256=$sha")
            .post(ByteArray(0).toRequestBody(null)).build()).execute().use { check(it) }
    }

    fun uploadRequest(peer: Peer, sessionId: String, fileId: String, token: String, offset: Long, body: okhttp3.RequestBody): Request =
        Request.Builder().url(peer.baseUrl + LocalSend.API + "/upload?sessionId=$sessionId&fileId=${enc(fileId)}&token=$token" + (if (offset > 0) "&offset=$offset" else ""))
            .post(body).build()

    private fun enc(s: String) = java.net.URLEncoder.encode(s, "UTF-8")
}
