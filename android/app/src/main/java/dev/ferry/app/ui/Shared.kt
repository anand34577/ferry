package dev.ferry.app.ui

import android.graphics.Bitmap
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import dev.ferry.app.FerryApp
import dev.ferry.app.data.Peer
import dev.ferry.app.data.TFile
import dev.ferry.app.util.PairCode
import kotlinx.coroutines.flow.MutableStateFlow

// UI state and helpers shared by the phone and TV apps; each flavor has its own screens.

/** Files queued for sending (from the picker or another app's share sheet). */
object SendQueue {
    val files = MutableStateFlow<List<TFile>>(emptyList())
    fun add(list: List<TFile>) {
        files.value = (files.value + list).distinctBy { it.uri }
    }
}

/** Opens a received file in whatever app handles its type; false when nothing can open it or it was deleted. */
fun openFile(ctx: android.content.Context, uri: android.net.Uri, mime: String): Boolean = try {
    ctx.contentResolver.openFileDescriptor(uri, "r")?.close() ?: throw java.io.FileNotFoundException()
    ctx.startActivity(android.content.Intent(android.content.Intent.ACTION_VIEW).setDataAndType(uri, mime.ifEmpty { "*/*" })
        .addFlags(android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION or android.content.Intent.FLAG_ACTIVITY_NEW_TASK))
    true
} catch (_: Exception) {
    false
}

/** Opens the Downloads/Ferry folder in the Files app, or the system Downloads list if that can't be targeted. */
fun openReceivedFolder(ctx: android.content.Context) {
    val folder = android.content.Intent(android.content.Intent.ACTION_VIEW)
        .setDataAndType(android.net.Uri.parse("content://com.android.externalstorage.documents/document/primary%3ADownload%2FFerry"), "vnd.android.document/directory")
        .addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)
    try {
        ctx.startActivity(folder)
    } catch (_: Exception) {
        try {
            ctx.startActivity(android.content.Intent(android.app.DownloadManager.ACTION_VIEW_DOWNLOADS).addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK))
        } catch (_: Exception) {
            android.widget.Toast.makeText(ctx, "No file manager found. Your files are in Downloads/Ferry.", android.widget.Toast.LENGTH_LONG).show()
        }
    }
}

/** Deep links / notifications ask the UI to navigate somewhere. */
object Nav {
    val pending = MutableStateFlow<String?>(null)

    /** Routes of the phone app's navigation graph; extras from other apps may only use these. */
    val routes = setOf("home", "send", "receive", "transfers", "server", "servers", "settings", "trusted", "scan")
}

fun qrBitmap(value: String, px: Int): Bitmap {
    val m = QRCodeWriter().encode(value, BarcodeFormat.QR_CODE, px, px, mapOf(EncodeHintType.MARGIN to 1))
    val pixels = IntArray(px * px) { i -> if (m.get(i % px, i / px)) 0xFF111318.toInt() else 0xFFFFFFFF.toInt() }
    return Bitmap.createBitmap(pixels, px, px, Bitmap.Config.ARGB_8888)
}

/** Accepts a pairing code ("60A-R0BQ"), an IP, or IP:port. */
suspend fun connectInput(input: String): Peer {
    val app = FerryApp.app
    val t = input.trim()
    val (ip, port) = PairCode.decode(t)
        ?: Regex("""^(\d{1,3}(?:\.\d{1,3}){3})(?::(\d{1,5}))?$""").find(t)?.let { it.groupValues[1] to (it.groupValues[2].toIntOrNull() ?: PairCode.DEFAULT_PORT) }
        ?: throw IllegalArgumentException("Enter a pairing code like 60A-R0BQ or an IP address like 192.168.1.20")
    return try {
        app.discovery.connect(ip, port, "", "code")
    } catch (e: Exception) {
        throw IllegalStateException("No Ferry/LocalSend device answered at $ip:$port. Check that it's receiving and on the same network.")
    }
}
