package dev.ferry.app.transfer

import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import android.os.PowerManager
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import dev.ferry.app.FerryApp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch

/**
 * Foreground service that keeps transfers and the LAN receiver alive while the app is in the background
 * or the screen is off. Stops itself when nothing is running and receiving is off.
 */
class TransferService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private var wake: PowerManager.WakeLock? = null
    private var holdsDiscovery = false

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        val app = FerryApp.app
        ServiceCompat.startForeground(this, Notifier.ID_SERVICE, Notifier.ongoing(this, app.transfers.active.value, app.prefs.receiving),
            ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        scope.launch {
            combine(app.transfers.active, app.receivingFlow) { a, r -> a to r }.collect { (active, receiving) ->
                if (receiving && !holdsDiscovery) {
                    app.discovery.acquire("receive"); holdsDiscovery = true
                } else if (!receiving && holdsDiscovery) {
                    app.discovery.release("receive"); holdsDiscovery = false
                }
                val busy = active.any { it.status.active }
                if (busy) acquireWake() else releaseWake()
                if (!busy && !receiving) {
                    stopSelf(); return@collect
                }
                NotificationManagerCompat.from(this@TransferService).apply {
                    if (areNotificationsEnabled()) notify(Notifier.ID_SERVICE, Notifier.ongoing(this@TransferService, active, receiving))
                }
            }
        }
    }

    private fun acquireWake() {
        if (wake?.isHeld == true) return
        wake = getSystemService(PowerManager::class.java).newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "ferry:transfer").apply {
            setReferenceCounted(false); acquire(6 * 60 * 60 * 1000L)
        }
    }

    private fun releaseWake() {
        runCatching { wake?.release() }
        wake = null
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int = START_NOT_STICKY

    /** Android 15+: dataSync services get ~6 h per day. Stop receiving cleanly instead of being killed. */
    override fun onTimeout(startId: Int, fgsType: Int) {
        FerryApp.app.setReceiving(false)
        stopSelf()
    }

    override fun onDestroy() {
        if (holdsDiscovery) FerryApp.app.discovery.release("receive")
        releaseWake()
        scope.cancel()
        super.onDestroy()
    }

    companion object {
        fun ensure(ctx: Context) {
            runCatching { ContextCompat.startForegroundService(ctx, Intent(ctx, TransferService::class.java)) }
        }
    }
}

/** Accept/Decline buttons on the incoming-transfer notification. */
class NotificationActions : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        val id = intent.getStringExtra("id") ?: return
        FerryApp.app.transfers.respond(id, intent.getBooleanExtra("accept", false), trustAlways = false)
        Notifier.clearIncoming(ctx)
    }
}
