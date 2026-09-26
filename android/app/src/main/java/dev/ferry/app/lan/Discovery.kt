package dev.ferry.app.lan

import android.content.Context
import android.net.wifi.WifiManager
import android.util.Log
import dev.ferry.app.FerryApp
import dev.ferry.app.data.Peer
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import org.json.JSONObject
import java.net.DatagramPacket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.MulticastSocket
import java.net.NetworkInterface

/**
 * LAN presence: runs the LocalSend receiver and multicast discovery while anything needs it
 * (receiving mode or the Send screen). Users of discovery acquire/release by tag.
 */
class Discovery(private val ctx: Context, private val certs: Certs, private val scope: CoroutineScope) {
    private val _peers = MutableStateFlow<Map<String, Peer>>(emptyMap())
    val peers: StateFlow<Map<String, Peer>> = _peers
    private val _running = MutableStateFlow(false)
    val running: StateFlow<Boolean> = _running
    private val _scanning = MutableStateFlow(false)
    val scanning: StateFlow<Boolean> = _scanning

    private val users = mutableSetOf<String>()
    private var socket: MulticastSocket? = null
    private var jobs = mutableListOf<Job>()
    private var lock: WifiManager.MulticastLock? = null
    var server: LanServer? = null
        private set
    val port get() = server?.port ?: LocalSend.PORT

    @Synchronized
    fun acquire(tag: String) {
        users += tag
        if (!_running.value) start()
        else announce()
    }

    @Synchronized
    fun release(tag: String) {
        users -= tag
        if (users.isEmpty()) stop()
    }

    private fun start() {
        // Receiver first: the announcement must carry the port we actually bound.
        for (p in LocalSend.PORT..LocalSend.PORT + 12) {
            try {
                server = LanServer(ctx, certs, p).also { it.startServer() }
                break
            } catch (e: Exception) {
                Log.w("Ferry", "port $p busy: ${e.message}")
            }
        }
        lock = (ctx.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager).createMulticastLock("ferry").apply {
            setReferenceCounted(false); acquire()
        }
        _running.value = true
        jobs += scope.launch(Dispatchers.IO) { listen() }
        jobs += scope.launch(Dispatchers.IO) {
            while (isActive) {
                announce()
                prune()
                delay(if (users.contains("send")) 4000 else 30_000)
            }
        }
    }

    private fun stop() {
        jobs.forEach { it.cancel() }
        jobs.clear()
        runCatching { socket?.close() }
        socket = null
        runCatching { server?.stop() }
        server = null
        runCatching { lock?.release() }
        _running.value = false
        _peers.value = _peers.value.filterValues { it.source == "manual" || it.source == "qr" || it.source == "code" }
    }

    private fun listen() {
        while (scope.isActive && _running.value) {
            try {
                val s = MulticastSocket(null).apply {
                    reuseAddress = true
                    bind(InetSocketAddress(LocalSend.PORT))
                    val group = InetAddress.getByName(LocalSend.MULTICAST_GROUP)
                    lanInterfaces().forEach { ni -> runCatching { joinGroup(InetSocketAddress(group, LocalSend.PORT), ni) } }
                    if (lanInterfaces().isEmpty()) runCatching { joinGroup(group) }
                }
                socket = s
                val buf = ByteArray(8192)
                while (_running.value) {
                    val pkt = DatagramPacket(buf, buf.size)
                    s.receive(pkt)
                    handlePacket(String(pkt.data, 0, pkt.length), pkt.address.hostAddress ?: continue)
                }
            } catch (e: Exception) {
                if (!_running.value) return
                Log.w("Ferry", "multicast listener: ${e.message}")
                Thread.sleep(3000) // network changed; rejoin
            }
        }
    }

    private fun handlePacket(text: String, ip: String) {
        val o = runCatching { JSONObject(text) }.getOrNull() ?: return
        val fp = o.optString("fingerprint")
        if (fp.isEmpty() || fp.equals(certs.fingerprint, true)) return
        val peer = LocalSend.peerFrom(o, ip, "multicast")
        upsert(peer)
        val announce = o.optBoolean("announce", o.optBoolean("announcement", false))
        if (announce && FerryApp.app.prefs.receiving) {
            // Answer via HTTP register (preferred by LocalSend), falling back to a multicast reply.
            scope.launch(Dispatchers.IO) {
                try {
                    LanClient.register(peer, self(null))
                } catch (e: Exception) {
                    sendMulticast(self(false))
                }
            }
        }
    }

    fun upsert(p: Peer) {
        val existing = _peers.value[p.key]
        var merged = if (existing != null && existing.source == "server") p.copy(accountDeviceId = existing.accountDeviceId, source = "server") else p
        if (merged.certPin.isEmpty() && existing != null) merged = merged.copy(certPin = existing.certPin)
        _peers.value = _peers.value + (p.key to merged.copy(lastSeen = System.currentTimeMillis()))
    }

    fun remove(key: String) {
        _peers.value = _peers.value - key
    }

    private fun prune() {
        val now = System.currentTimeMillis()
        _peers.value = _peers.value.filterValues { it.source in setOf("manual", "qr", "code") || now - it.lastSeen < 90_000 }
    }

    private fun self(announce: Boolean?) = LocalSend.selfInfo(FerryApp.app.prefs.alias, certs.fingerprint, port, announce)

    fun announce() {
        if (!_running.value) return
        scope.launch(Dispatchers.IO) { sendMulticast(self(true)) }
    }

    private fun sendMulticast(o: JSONObject) {
        val data = o.toString().toByteArray()
        val group = InetAddress.getByName(LocalSend.MULTICAST_GROUP)
        val nis = lanInterfaces().ifEmpty { listOf(null) }
        for (ni in nis) runCatching {
            MulticastSocket().use { s ->
                if (ni != null) s.networkInterface = ni
                s.timeToLive = 4
                s.send(DatagramPacket(data, data.size, group, LocalSend.PORT))
            }
        }
    }

    private fun lanInterfaces(): List<NetworkInterface> = runCatching {
        NetworkInterface.getNetworkInterfaces().toList().filter { ni ->
            ni.isUp && !ni.isLoopback && ni.supportsMulticast() && !ni.name.startsWith("rmnet") && !ni.name.startsWith("tun") &&
                ni.inetAddresses.toList().any { it is Inet4Address && it.isSiteLocalAddress }
        }
    }.getOrDefault(emptyList())

    /** Fallback when multicast is blocked: probe every address in our /24 subnets (like LocalSend's HTTP scan). */
    fun scanSubnet() {
        if (_scanning.value) return
        _scanning.value = true
        scope.launch(Dispatchers.IO) {
            try {
                val sem = Semaphore(48)
                val jobs = NetworkMonitor.localIPv4().flatMap { mine ->
                    val prefix = mine.substringBeforeLast('.')
                    (1..254).map { "$prefix.$it" }.filter { it != mine }.map { ip ->
                        launch {
                            sem.withPermit {
                                runCatching { LanClient.info(ip, LocalSend.PORT, "", "scan") }.getOrNull()
                                    ?.takeIf { !it.fingerprint.equals(certs.fingerprint, true) }?.let(::upsert)
                            }
                        }
                    }
                }
                jobs.forEach { it.join() }
            } finally {
                _scanning.value = false
            }
        }
    }

    /** Connects to an explicit address (QR, short code or manual entry). */
    /** [fingerprint] from a Ferry QR code is our certificate hash, so it is enforced as the pin. */
    suspend fun connect(ip: String, port: Int, fingerprint: String, source: String, https: Boolean? = null): Peer {
        val p = kotlinx.coroutines.withContext(Dispatchers.IO) { LanClient.info(ip, port, fingerprint, source, https) }
        upsert(p)
        return p
    }

    /** Server-assisted discovery: my own devices that published LAN presence (same account only). */
    fun addFromServer(devices: List<JSONObject>) {
        scope.launch(Dispatchers.IO) {
            for (d in devices) {
                if (!d.optBoolean("online") || d.optBoolean("current")) continue
                val addrs = d.optJSONArray("lanAddrs") ?: continue
                for (i in 0 until addrs.length()) {
                    val ip = addrs.getString(i)
                    val port = d.optInt("lanPort", LocalSend.PORT).takeIf { it > 0 } ?: LocalSend.PORT
                    val p = runCatching { LanClient.info(ip, port, d.optString("fingerprint"), "server") }.getOrNull() ?: continue
                    upsert(p.copy(source = "server", accountDeviceId = d.getString("id"), alias = d.optString("name", p.alias)))
                    break
                }
            }
        }
    }
}
