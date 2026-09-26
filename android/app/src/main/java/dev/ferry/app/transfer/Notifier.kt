package dev.ferry.app.transfer

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import dev.ferry.app.FerryApp
import dev.ferry.app.MainActivity
import dev.ferry.app.R
import dev.ferry.app.data.IncomingRequest
import dev.ferry.app.data.TStatus
import dev.ferry.app.data.Transfer
import dev.ferry.app.util.formatBytes

object Notifier {
    const val CH_ONGOING = "ongoing"
    const val CH_INCOMING = "incoming"
    const val CH_EVENTS = "events"
    const val ID_SERVICE = 1
    private const val ID_INCOMING = 2

    fun createChannels(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(CH_ONGOING, "Active transfers", NotificationManager.IMPORTANCE_LOW).apply {
            description = "Progress of files being sent or received"
        })
        nm.createNotificationChannel(NotificationChannel(CH_INCOMING, "Incoming files", NotificationManager.IMPORTANCE_HIGH).apply {
            description = "Asks before accepting files from another device"
        })
        nm.createNotificationChannel(NotificationChannel(CH_EVENTS, "Transfer results", NotificationManager.IMPORTANCE_DEFAULT).apply {
            description = "Completed and failed transfers"
        })
    }

    private fun canPost(ctx: Context) =
        ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    fun openApp(ctx: Context, route: String = "transfers"): PendingIntent = PendingIntent.getActivity(ctx, route.hashCode(),
        Intent(ctx, MainActivity::class.java).putExtra("route", route).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

    fun ongoing(ctx: Context, active: List<Transfer>, receiving: Boolean) = NotificationCompat.Builder(ctx, CH_ONGOING)
        .setSmallIcon(R.drawable.ic_stat_ferry)
        .setOngoing(true)
        .setOnlyAlertOnce(true)
        .setContentIntent(openApp(ctx))
        .apply {
            val running = active.filter { it.status.active }
            if (running.isNotEmpty() && FerryApp.app.prefs.notifyLevel == "all") {
                val total = running.sumOf { it.total }.coerceAtLeast(1)
                val done = running.sumOf { it.done }
                val sending = running.count { it.sent }
                setContentTitle(if (sending == running.size) "Sending ${running.sumOf { it.files.size }} file(s)" else if (sending == 0) "Receiving files" else "Transferring files")
                setContentText("${formatBytes(done)} of ${formatBytes(total)} · ${running.first().method.label}")
                setProgress(1000, (done * 1000 / total).toInt(), false)
            } else if (running.isNotEmpty()) {
                setContentTitle("Transfers in progress")
            } else {
                setContentTitle(if (receiving) "Ready to receive" else "Ferry")
                setContentText(if (receiving) "Visible to nearby devices as “${FerryApp.app.prefs.alias}”" else "")
            }
        }
        .build()

    fun incoming(ctx: Context, req: IncomingRequest) {
        if (!canPost(ctx)) return
        fun action(accept: Boolean) = PendingIntent.getBroadcast(ctx, if (accept) 11 else 12,
            Intent(ctx, NotificationActions::class.java).putExtra("id", req.id).putExtra("accept", accept),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val who = req.alias + if (req.ownAccount) " (your account)" else if (req.trusted) " (trusted)" else " (unknown device)"
        val n = NotificationCompat.Builder(ctx, CH_INCOMING)
            .setSmallIcon(R.drawable.ic_stat_ferry)
            .setContentTitle("$who wants to send ${req.files.size} file(s)")
            .setContentText("${req.files.firstOrNull()?.first ?: ""}${if (req.files.size > 1) " and ${req.files.size - 1} more" else ""} · ${formatBytes(req.total)}")
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setContentIntent(openApp(ctx, "home"))
            .setAutoCancel(true)
            .setTimeoutAfter(90_000)
            .addAction(0, "Decline", action(false))
            .addAction(0, "Accept", action(true))
            .build()
        NotificationManagerCompat.from(ctx).notify(ID_INCOMING, n)
    }

    fun clearIncoming(ctx: Context) = NotificationManagerCompat.from(ctx).cancel(ID_INCOMING)

    fun finished(ctx: Context, t: Transfer) {
        if (!canPost(ctx) || FerryApp.app.prefs.notifyLevel == "minimal" && t.status == TStatus.COMPLETED) return
        val title = when (t.status) {
            TStatus.COMPLETED -> if (t.sent) "Sent to ${t.peer}" else "Received from ${t.peer}"
            TStatus.REJECTED -> "${t.peer} declined the files"
            TStatus.CANCELLED -> "Transfer cancelled"
            else -> "Transfer failed"
        }
        val text = if (t.status == TStatus.COMPLETED) "${t.files.size} file(s) · ${formatBytes(t.total)}" + (if (!t.sent) " · saved to Downloads/Ferry" else "")
        else t.error.ifEmpty { "Tap to retry" }
        val n = NotificationCompat.Builder(ctx, CH_EVENTS)
            .setSmallIcon(R.drawable.ic_stat_ferry)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setContentIntent(openApp(ctx))
            .setAutoCancel(true)
            .build()
        NotificationManagerCompat.from(ctx).notify(t.id.hashCode(), n)
    }
}
