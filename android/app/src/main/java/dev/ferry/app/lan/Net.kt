package dev.ferry.app.lan

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.os.BatteryManager
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import java.net.Inet4Address
import java.net.NetworkInterface

/**
 * Tracks connectivity on independent axes: being on Wi-Fi is not the same as having internet,
 * and a LAN can exist without either (e.g. phone hotspot).
 */
data class NetState(
    val wifi: Boolean = false,
    val cellular: Boolean = false,
    val internet: Boolean = false,
    val metered: Boolean = false,
    val lanAddrs: List<String> = emptyList(),
) {
    val lan get() = lanAddrs.isNotEmpty()
}

class NetworkMonitor(private val ctx: Context) {
    private val cm = ctx.getSystemService(ConnectivityManager::class.java)
    private val _state = MutableStateFlow(snapshot())
    val state: StateFlow<NetState> = _state

    init {
        cm.registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = update()
            override fun onLost(network: Network) = update()
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) = update()
            override fun onLinkPropertiesChanged(network: Network, lp: android.net.LinkProperties) = update()
        })
    }

    fun update() {
        _state.value = snapshot()
    }

    private fun snapshot(): NetState {
        val caps = cm.activeNetwork?.let { cm.getNetworkCapabilities(it) }
        return NetState(
            wifi = caps?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true || caps?.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) == true,
            cellular = caps?.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) == true,
            internet = caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED) == true,
            metered = cm.isActiveNetworkMetered,
            lanAddrs = localIPv4(),
        )
    }

    fun batteryLow(): Boolean {
        val bm = ctx.getSystemService(BatteryManager::class.java) ?: return false
        return bm.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY) in 1..19 && !bm.isCharging
    }

    companion object {
        /** Private IPv4 addresses of Wi-Fi/Ethernet/hotspot interfaces (cellular excluded). */
        fun localIPv4(): List<String> = runCatching {
            NetworkInterface.getNetworkInterfaces().toList()
                .filter { it.isUp && !it.isLoopback && !it.name.startsWith("rmnet") && !it.name.startsWith("ccmni") && !it.name.startsWith("tun") }
                .flatMap { ni -> ni.inetAddresses.toList().filterIsInstance<Inet4Address>().filter { it.isSiteLocalAddress }.map { it.hostAddress!! } }
                .distinct()
        }.getOrDefault(emptyList())
    }
}
