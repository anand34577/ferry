package dev.ferry.app.data

import android.content.Context
import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import org.json.JSONArray
import org.json.JSONObject
import java.security.KeyStore
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

data class ServerProfile(
    val id: String,
    val name: String,
    val url: String,
    val email: String = "",
    val userName: String = "",
    val deviceId: String = "",
    val tokenEnc: String = "",
    val isAdmin: Boolean = false,
) {
    val signedIn get() = tokenEnc.isNotEmpty()
    fun toJson() = JSONObject().put("id", id).put("name", name).put("url", url).put("email", email).put("userName", userName)
        .put("deviceId", deviceId).put("tokenEnc", tokenEnc).put("isAdmin", isAdmin)

    companion object {
        fun from(o: JSONObject) = ServerProfile(o.getString("id"), o.optString("name"), o.getString("url"), o.optString("email"),
            o.optString("userName"), o.optString("deviceId"), o.optString("tokenEnc"), o.optBoolean("isAdmin"))
    }
}

/** A LAN peer the user explicitly paired with (fingerprint = SHA-256 of its TLS certificate). */
data class TrustedDevice(val fingerprint: String, val alias: String, val autoAccept: Boolean, val addedAt: Long) {
    fun toJson() = JSONObject().put("fingerprint", fingerprint).put("alias", alias).put("autoAccept", autoAccept).put("addedAt", addedAt)

    companion object {
        fun from(o: JSONObject) = TrustedDevice(o.getString("fingerprint"), o.optString("alias"), o.optBoolean("autoAccept"), o.optLong("addedAt"))
    }
}

class Prefs(ctx: Context) {
    private val sp = ctx.getSharedPreferences("ferry", Context.MODE_PRIVATE)

    private fun str(k: String, d: String) = sp.getString(k, d) ?: d
    private fun put(k: String, v: Any) = sp.edit().apply {
        when (v) {
            is String -> putString(k, v); is Boolean -> putBoolean(k, v); is Long -> putLong(k, v); is Int -> putInt(k, v)
        }
    }.apply()

    var alias: String
        get() = str("alias", Build.MODEL ?: "Android")
        set(v) = put("alias", v.trim().take(40).ifEmpty { Build.MODEL ?: "Android" })
    var requirePin: Boolean
        get() = sp.getBoolean("requirePin", false)
        set(v) = put("requirePin", v)
    var pin: String
        get() = str("pin", "")
        set(v) = put("pin", v)
    var receiving: Boolean
        get() = sp.getBoolean("receiving", false)
        set(v) = put("receiving", v)
    /** auto | direct | server | ask */
    var method: String
        get() = str("method", "auto")
        set(v) = put("method", v)
    var cellularWarnBytes: Long
        get() = sp.getLong("cellWarn", 50L * 1024 * 1024)
        set(v) = put("cellWarn", v)
    var wifiOnlyLarge: Boolean
        get() = sp.getBoolean("wifiOnlyLarge", false)
        set(v) = put("wifiOnlyLarge", v)
    /** all | important | minimal */
    var notifyLevel: String
        get() = str("notifyLevel", "all")
        set(v) = put("notifyLevel", v)
    /** system | light | dark */
    var theme: String
        get() = str("theme", "system")
        set(v) {
            put("theme", v); themeFlow.value = v
        }
    val themeFlow = MutableStateFlow(str("theme", "system"))
    var autoAcceptOwn: Boolean
        get() = sp.getBoolean("autoAcceptOwn", false)
        set(v) = put("autoAcceptOwn", v)
    var onboarded: Boolean
        get() = sp.getBoolean("onboarded", false)
        set(v) = put("onboarded", v)

    // ---- trusted LAN devices ----
    private val _trusted = MutableStateFlow(loadTrusted())
    val trusted: StateFlow<List<TrustedDevice>> = _trusted
    private fun loadTrusted(): List<TrustedDevice> = runCatching {
        val a = JSONArray(str("trusted", "[]")); (0 until a.length()).map { TrustedDevice.from(a.getJSONObject(it)) }
    }.getOrDefault(emptyList())

    fun saveTrusted(list: List<TrustedDevice>) {
        put("trusted", JSONArray(list.map { it.toJson() }).toString()); _trusted.value = list
    }
    fun trust(fp: String, alias: String) {
        if (_trusted.value.none { it.fingerprint == fp }) saveTrusted(_trusted.value + TrustedDevice(fp, alias, false, System.currentTimeMillis()))
    }
    fun isTrusted(fp: String) = _trusted.value.any { it.fingerprint.equals(fp, true) }
    fun autoAccept(fp: String) = _trusted.value.any { it.fingerprint.equals(fp, true) && it.autoAccept }

    // ---- server profiles ----
    private val _servers = MutableStateFlow(loadServers())
    val servers: StateFlow<List<ServerProfile>> = _servers
    private val _active = MutableStateFlow(str("activeServer", ""))
    val activeServerId: StateFlow<String> = _active

    private fun loadServers(): List<ServerProfile> = runCatching {
        val a = JSONArray(str("servers", "[]")); (0 until a.length()).map { ServerProfile.from(a.getJSONObject(it)) }
    }.getOrDefault(emptyList())

    fun saveServer(p: ServerProfile) {
        val list = _servers.value.filter { it.id != p.id } + p
        put("servers", JSONArray(list.map { it.toJson() }).toString()); _servers.value = list
    }
    fun removeServer(id: String) {
        val list = _servers.value.filter { it.id != id }
        put("servers", JSONArray(list.map { it.toJson() }).toString()); _servers.value = list
        if (_active.value == id) setActive(list.firstOrNull()?.id ?: "")
    }
    fun setActive(id: String) {
        put("activeServer", id); _active.value = id
    }
    fun active(): ServerProfile? = _servers.value.find { it.id == _active.value }

    // ---- misc persisted maps (resumable upload URLs etc.) ----
    fun getKV(k: String): String? = sp.getString("kv:$k", null)
    fun setKV(k: String, v: String?) = sp.edit().apply { if (v == null) remove("kv:$k") else putString("kv:$k", v) }.apply()

    val installId: String
        get() = sp.getString("installId", null) ?: UUID.randomUUID().toString().also { put("installId", it) }
}

/** Encrypts secrets (server tokens) with a non-exportable AES key in the Android Keystore. */
object SecretBox {
    private const val ALIAS = "ferry-secrets"

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getEntry(ALIAS, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        gen.init(KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
        return gen.generateKey()
    }

    fun seal(plain: String): String {
        val c = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key()) }
        val out = c.iv + c.doFinal(plain.toByteArray())
        return Base64.encodeToString(out, Base64.NO_WRAP)
    }

    fun open(sealed: String): String? = runCatching {
        val b = Base64.decode(sealed, Base64.NO_WRAP)
        val c = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, b, 0, 12)) }
        String(c.doFinal(b, 12, b.size - 12))
    }.getOrNull()
}
