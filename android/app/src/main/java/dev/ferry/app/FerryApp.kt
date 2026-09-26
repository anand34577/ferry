package dev.ferry.app

import android.app.Application
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import dev.ferry.app.data.Prefs
import dev.ferry.app.lan.Certs
import dev.ferry.app.lan.Discovery
import dev.ferry.app.lan.NetworkMonitor
import dev.ferry.app.server.ServerManager
import dev.ferry.app.server.ServerState
import dev.ferry.app.transfer.History
import dev.ferry.app.transfer.Notifier
import dev.ferry.app.transfer.TransferManager
import dev.ferry.app.transfer.TransferService
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.distinctUntilChangedBy
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject

class FerryApp : Application() {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    lateinit var prefs: Prefs
    lateinit var net: NetworkMonitor
    lateinit var certs: Certs
    lateinit var discovery: Discovery
    lateinit var server: ServerManager
    lateinit var transfers: TransferManager
    lateinit var history: History
    private val _receiving = MutableStateFlow(false)
    val receivingFlow: StateFlow<Boolean> = _receiving
    private val _foreground = MutableStateFlow(false)
    val foreground: StateFlow<Boolean> = _foreground

    override fun onCreate() {
        super.onCreate()
        app = this
        prefs = Prefs(this)
        net = NetworkMonitor(this)
        certs = Certs(this)
        history = History(this)
        discovery = Discovery(this, certs, scope)
        server = ServerManager(prefs, scope)
        transfers = TransferManager(this, scope)
        Notifier.createChannels(this)
        _receiving.value = prefs.receiving

        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) {
                _foreground.value = true
                server.refresh()
                if (prefs.receiving) TransferService.ensure(this@FerryApp)
            }
            override fun onStop(owner: LifecycleOwner) {
                _foreground.value = false
            }
        })

        // Re-check the server whenever connectivity changes (Wi-Fi ≠ internet ≠ server reachable).
        scope.launch { net.state.distinctUntilChangedBy { Triple(it.wifi, it.internet, it.lanAddrs) }.collect { if (prefs.active() != null) server.refresh() } }

        // Inbox polling + LAN presence for server-assisted discovery.
        scope.launch {
            var tick = 0
            while (true) {
                val online = server.state.value is ServerState.Online
                if (online && (_foreground.value || _receiving.value)) transfers.pollInbox()
                if (online && _receiving.value && tick % 4 == 0) publishPresence()
                tick++
                delay(15_000)
            }
        }
    }

    fun setReceiving(on: Boolean) {
        prefs.receiving = on
        _receiving.value = on
        if (on) TransferService.ensure(this)
        scope.launch(Dispatchers.IO) {
            if (on) publishPresence() else runCatching { server.api()?.delete("/api/v1/devices/current/presence") }
        }
    }

    private fun publishPresence() {
        val api = server.api() ?: return
        val addrs = net.state.value.lanAddrs
        if (addrs.isEmpty()) return
        scope.launch(Dispatchers.IO) {
            runCatching {
                api.put("/api/v1/devices/current/presence", JSONObject().put("addrs", JSONArray(addrs)).put("port", discovery.port)
                    .put("protocol", "https").put("fingerprint", certs.fingerprint))
            }
        }
    }

    companion object {
        lateinit var app: FerryApp
            private set
    }
}
