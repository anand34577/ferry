package dev.ferry.app.server

import dev.ferry.app.data.Prefs
import dev.ferry.app.data.SecretBox
import dev.ferry.app.data.ServerProfile
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.util.UUID

/** Connection state of the active server, kept separate from Wi-Fi/internet state. */
sealed class ServerState {
    data object None : ServerState()
    data object Checking : ServerState()
    data class Online(val version: String, val siteName: String, val capabilities: Set<String>) : ServerState()
    data class Offline(val reason: String) : ServerState()
    data class SignedOut(val reason: String) : ServerState()
    data class Incompatible(val reason: String) : ServerState()
}

class ServerManager(private val prefs: Prefs, private val scope: CoroutineScope) {
    private val _state = MutableStateFlow<ServerState>(if (prefs.active() == null) ServerState.None else ServerState.Checking)
    val state: StateFlow<ServerState> = _state
    /** Server clock minus local clock; server time is authoritative for expiry display. */
    @Volatile var skewMs = 0L

    val profile: ServerProfile? get() = prefs.active()
    val online get() = _state.value is ServerState.Online

    /** API client for the signed-in active profile, or null. */
    fun api(): ServerApi? {
        val p = profile ?: return null
        val token = SecretBox.open(p.tokenEnc) ?: return null
        return ServerApi(p.url, token)
    }

    fun refresh() {
        val p = profile ?: run { _state.value = ServerState.None; return }
        _state.value = ServerState.Checking
        scope.launch(Dispatchers.IO) { _state.value = check(p) }
    }

    private fun check(p: ServerProfile): ServerState {
        return try {
            val info = ServerApi(p.url, null).info()
            skewMs = info.optLong("serverTime", System.currentTimeMillis()) - System.currentTimeMillis()
            compatibility(info)?.let { return ServerState.Incompatible(it) }
            if (!p.signedIn) return ServerState.SignedOut("Sign in to use this server.")
            try {
                val me = api()?.get("/api/v1/me") ?: return ServerState.SignedOut("Sign in again to use this server.")
                val u = me.getJSONObject("user")
                prefs.saveServer(p.copy(userName = u.optString("name"), isAdmin = u.optString("role") == "admin"))
            } catch (e: ApiException) {
                if (e.status == 401 || e.status == 403) {
                    prefs.saveServer(p.copy(tokenEnc = ""))
                    return ServerState.SignedOut(e.message ?: "Please sign in again.")
                }
                if (e.status == 426) return ServerState.Incompatible(e.message ?: "")
                throw e
            }
            val caps = info.optJSONArray("capabilities")?.let { a -> (0 until a.length()).map { a.getString(it) }.toSet() } ?: emptySet()
            ServerState.Online(info.optString("version"), info.optString("siteName", p.name), caps)
        } catch (e: Exception) {
            ServerState.Offline(friendly(e))
        }
    }

    /** Explicit version check with a human-readable message. */
    fun compatibility(info: JSONObject): String? {
        if (info.optString("name") != "Ferry") return "This address doesn't look like a Ferry server."
        val api = info.optInt("apiVersion", 0)
        val minClient = info.optInt("minClientApiVersion", 1)
        if (minClient > ServerApi.API_VERSION) return "This server (Ferry ${info.optString("version")}) needs a newer app. Please update Ferry on this phone."
        if (api < ServerApi.API_VERSION) return "This server runs an older Ferry (${info.optString("version")}). Ask the administrator to update it."
        return null
    }

    /** Validates a server address before saving it. Returns the /info JSON. */
    suspend fun probe(url: String): JSONObject = withContext(Dispatchers.IO) {
        val info = try {
            ServerApi(url, null).info()
        } catch (e: Exception) {
            throw IllegalStateException(friendly(e))
        }
        compatibility(info)?.let { throw IllegalStateException(it) }
        info
    }

    suspend fun addServer(url: String, name: String): ServerProfile {
        val norm = ServerApi.normalizeUrl(url)
        val info = probe(norm)
        val existing = prefs.servers.value.find { it.url.equals(norm, true) }
        val p = existing ?: ServerProfile(UUID.randomUUID().toString(), name.ifBlank { info.optString("siteName", "Ferry") }, norm)
        prefs.saveServer(p)
        prefs.setActive(p.id)
        refresh()
        return p
    }

    /** Throws [ApiException] with code "totp_required" when the account needs a two-factor [code]. */
    suspend fun login(p: ServerProfile, email: String, password: String, code: String = "") = withContext(Dispatchers.IO) {
        val res = ServerApi(p.url, null).login(email, password, prefs.alias, p.deviceId, code)
        val u = res.getJSONObject("user")
        prefs.saveServer(p.copy(email = u.optString("email"), userName = u.optString("name"), isAdmin = u.optString("role") == "admin",
            deviceId = res.getJSONObject("device").getString("id"), tokenEnc = SecretBox.seal(res.getString("token"))))
        prefs.setActive(p.id)
        refresh()
    }

    fun logout(p: ServerProfile) {
        val token = SecretBox.open(p.tokenEnc)
        scope.launch(Dispatchers.IO) { token?.let { ServerApi(p.url, it).logout() } }
        prefs.saveServer(p.copy(tokenEnc = ""))
        refresh()
    }

    fun switchTo(id: String) {
        prefs.setActive(id); refresh()
    }

    fun remove(p: ServerProfile) {
        if (p.signedIn) logout(p)
        prefs.removeServer(p.id); refresh()
    }

    companion object {
        fun friendly(e: Throwable): String = when {
            e is ApiException -> e.message ?: "Server error"
            e is java.net.UnknownHostException -> "Server address not found. Check the address and your connection."
            e is java.net.ConnectException -> "Can't connect to the server. It may be offline or blocked on this network."
            e is java.net.SocketTimeoutException -> "The server isn't responding. Check your connection."
            e is javax.net.ssl.SSLException -> "Secure connection failed. The server's HTTPS certificate isn't trusted by this phone."
            e is IllegalStateException -> e.message ?: "Error"
            else -> e.message ?: "Can't reach the server."
        }
    }
}
