package dev.ferry.app.data

import android.net.Uri
import org.json.JSONArray
import org.json.JSONObject

/** Transfer states — the same strings as the server and web app. */
enum class TStatus(val wire: String, val label: String) {
    CREATED("created", "Preparing"),
    WAITING("waiting", "Waiting"),
    NEGOTIATING("negotiating", "Negotiating"),
    CONNECTING("connecting", "Connecting"),
    TRANSFERRING("transferring", "Transferring"),
    VERIFYING("verifying", "Verifying"),
    COMPLETED("completed", "Completed"),
    FAILED("failed", "Failed"),
    CANCELLED("cancelled", "Cancelled"),
    EXPIRED("expired", "Expired"),
    REJECTED("rejected", "Declined"),
    INTERRUPTED("interrupted", "Interrupted");

    val final get() = this in setOf(COMPLETED, FAILED, CANCELLED, EXPIRED, REJECTED)
    val active get() = this in setOf(CREATED, WAITING, NEGOTIATING, CONNECTING, TRANSFERRING, VERIFYING)

    companion object {
        fun of(s: String) = entries.find { it.wire == s } ?: FAILED
    }
}

enum class Method(val wire: String, val label: String) {
    DIRECT("direct", "Direct (Wi-Fi)"), SERVER("server", "Via server"), LINK("link", "Link");

    companion object {
        fun of(s: String) = entries.find { it.wire == s } ?: SERVER
    }
}

data class TFile(
    val id: String,
    val name: String,
    val size: Long,
    val mime: String = "application/octet-stream",
    val uri: Uri? = null,
    val done: Long = 0,
    val sha256: String = "",
    val serverFileId: String = "",
) {
    fun toJson() = JSONObject().put("id", id).put("name", name).put("size", size).put("mime", mime).put("uri", uri?.toString() ?: "")
        .put("done", done)

    companion object {
        fun from(o: JSONObject) = TFile(o.getString("id"), o.getString("name"), o.getLong("size"), o.optString("mime"),
            o.optString("uri").takeIf { it.isNotEmpty() }?.let(Uri::parse), o.optLong("done"))
    }
}

data class Transfer(
    val id: String,
    val sent: Boolean,
    val method: Method,
    val peer: String,
    val files: List<TFile>,
    val status: TStatus,
    val error: String = "",
    val speed: Double = 0.0,
    val canPause: Boolean = false,
    val paused: Boolean = false,
    val shareUrl: String = "",
    val serverTransferId: String = "",
    val note: String = "",
    val createdAt: Long = System.currentTimeMillis(),
    val updatedAt: Long = System.currentTimeMillis(),
) {
    val total get() = files.sumOf { it.size }
    val done get() = files.sumOf { it.done }
    val progress get() = if (total == 0L) (if (status == TStatus.COMPLETED) 1f else 0f) else (done.toFloat() / total)

    fun toJson() = JSONObject().put("id", id).put("sent", sent).put("method", method.wire).put("peer", peer)
        .put("files", JSONArray(files.map { it.toJson() })).put("status", status.wire).put("error", error)
        .put("shareUrl", shareUrl).put("createdAt", createdAt).put("updatedAt", updatedAt)

    companion object {
        fun from(o: JSONObject): Transfer {
            val fa = o.getJSONArray("files")
            return Transfer(o.getString("id"), o.getBoolean("sent"), Method.of(o.getString("method")), o.optString("peer"),
                (0 until fa.length()).map { TFile.from(fa.getJSONObject(it)) }, TStatus.of(o.getString("status")), o.optString("error"),
                shareUrl = o.optString("shareUrl"), createdAt = o.optLong("createdAt"), updatedAt = o.optLong("updatedAt"))
        }
    }
}

/** A device reachable on the LAN (via multicast, server presence, QR, code or manual entry). */
data class Peer(
    val ip: String,
    val port: Int,
    val https: Boolean,
    val alias: String,
    val model: String,
    val type: String,
    val fingerprint: String,
    val source: String, // multicast | server | qr | code | manual | scan
    val ferry: Boolean = false, // supports Ferry resume/verify extensions
    val accountDeviceId: String = "", // set when this is one of my own server devices
    val certPin: String = "", // SHA-256 of the TLS certificate seen on first contact (trust on first use)
    val lastSeen: Long = System.currentTimeMillis(),
) {
    val key get() = fingerprint.ifEmpty { "$ip:$port" }
    val baseUrl get() = (if (https) "https" else "http") + "://" + (if (ip.contains(':')) "[$ip]" else ip) + ":" + port
}

/** An incoming transfer waiting for the user's decision. */
data class IncomingRequest(
    val id: String,
    val alias: String,
    val model: String,
    val fingerprint: String,
    val trusted: Boolean,
    val ownAccount: Boolean,
    val files: List<Pair<String, Long>>,
    val source: Method,
) {
    val total get() = files.sumOf { it.second }
}
