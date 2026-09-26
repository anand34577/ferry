package dev.ferry.app.lan

import android.os.Build
import dev.ferry.app.data.Peer
import org.json.JSONObject

/** LocalSend protocol v2 constants and message helpers (https://github.com/localsend/protocol). */
object LocalSend {
    const val MULTICAST_GROUP = "224.0.0.167"
    const val PORT = 53317
    const val VERSION = "2.1"
    const val API = "/api/localsend/v2"

    /** Discovery/registration payload. Contains only what's needed to connect — no account data. */
    fun selfInfo(alias: String, fingerprint: String, port: Int, announce: Boolean? = null): JSONObject = JSONObject()
        .put("alias", alias)
        .put("version", VERSION)
        .put("deviceModel", listOfNotNull(Build.MANUFACTURER?.replaceFirstChar { it.uppercase() }, Build.MODEL).distinct().joinToString(" ").take(60))
        .put("deviceType", "mobile")
        .put("fingerprint", fingerprint)
        .put("port", port)
        .put("protocol", "https")
        .put("download", false)
        .put("ferry", 1) // extension marker: resume + verify supported; ignored by stock LocalSend
        .apply {
            if (announce != null) {
                put("announce", announce); put("announcement", announce)
            }
        }

    fun peerFrom(o: JSONObject, ip: String, source: String, fallbackPort: Int = PORT, fallbackHttps: Boolean = true) = Peer(
        ip = ip,
        port = o.optInt("port", fallbackPort),
        https = o.optString("protocol", if (fallbackHttps) "https" else "http") != "http",
        alias = o.optString("alias", "Unknown device").take(60),
        model = o.optString("deviceModel", "").take(60),
        type = o.optString("deviceType", "mobile"),
        fingerprint = o.optString("fingerprint", ""),
        source = source,
        ferry = o.optInt("ferry", 0) >= 1,
    )
}
