package dev.ferry.app.ui

import android.Manifest
import android.content.pm.PackageManager
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.CompositingStrategy
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.navigation.NavHostController
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import dev.ferry.app.FerryApp
import kotlinx.coroutines.launch
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/** Scans Ferry QR codes: nearby devices (ferry://peer), server setup (ferry://server) or pairing codes. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun QrScanScreen(nav: NavHostController) {
    val ctx = LocalContext.current
    var granted by remember { mutableStateOf(ContextCompat.checkSelfPermission(ctx, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) }
    val perm = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted = it }
    val scope = rememberCoroutineScope()
    val handled = remember { AtomicBoolean(false) }
    var status by remember { mutableStateOf("Point the camera at a Ferry QR code") }

    // Android TVs and some tablets have no camera: point people at the pairing code instead.
    if (!ctx.packageManager.hasSystemFeature(PackageManager.FEATURE_CAMERA_ANY)) {
        Column(Modifier.fillMaxSize()) {
            ScreenHeader("Scan QR code", onBack = { nav.popBackStack() })
            EmptyState(Icons.Outlined.QrCodeScanner, "No camera on this device", "Enter the pairing code or IP address shown on the other device instead.")
        }
        return
    }
    LaunchedEffect(Unit) { if (!granted) perm.launch(Manifest.permission.CAMERA) }

    fun onCode(text: String) {
        if (!handled.compareAndSet(false, true)) return
        scope.launch {
            status = "Connecting…"
            val result = handleScanned(text)
            if (result == null) {
                nav.popBackStack()
            } else {
                status = result
                handled.set(false)
            }
        }
    }

    if (!granted) {
        Column(Modifier.fillMaxSize()) {
            ScreenHeader("Scan QR code", onBack = { nav.popBackStack() })
            EmptyState(Icons.Outlined.QrCodeScanner, "Camera permission needed", "Ferry uses the camera only to read QR codes. Nothing is recorded.") {
                BigButton("Allow camera") { perm.launch(Manifest.permission.CAMERA) }
            }
        }
        return
    }
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        CameraPreview(::onCode) { status = it }
        Viewfinder()
        Box(Modifier.statusBarsPadding().padding(16.dp).size(48.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.45f))
            .clickable { nav.popBackStack() }, contentAlignment = Alignment.Center) {
            Icon(Icons.AutoMirrored.Outlined.ArrowBack, "Back", tint = Color.White)
        }
        Column(Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(28.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Text("Scan a Ferry QR code", color = Color.White, style = MaterialTheme.typography.titleLarge)
            Text("From another device's Receive screen, or the web app's Devices page", color = Color.White.copy(alpha = 0.75f),
                style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Center)
            Spacer(Modifier.height(14.dp))
            Row(Modifier.clip(RoundedCornerShape(50)).background(Color.White.copy(alpha = 0.16f)).padding(horizontal = 18.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                if (status.startsWith("Connecting")) {
                    androidx.compose.material3.CircularProgressIndicator(Modifier.size(16.dp), color = Color.White, strokeWidth = 2.dp)
                    Spacer(Modifier.width(10.dp))
                }
                Text(status, color = Color.White, style = MaterialTheme.typography.labelLarge)
            }
        }
    }
}

/** Dark scrim with a rounded cut-out, corner brackets and a moving scan line. */
@Composable
private fun Viewfinder() {
    val t = rememberInfiniteTransition(label = "scan")
    val y by t.animateFloat(0f, 1f, infiniteRepeatable(tween(1800), RepeatMode.Reverse), label = "y")
    val accent = MaterialTheme.colorScheme.primary
    Canvas(Modifier.fillMaxSize().graphicsLayer(compositingStrategy = CompositingStrategy.Offscreen)) {
        val side = size.minDimension * 0.68f
        val left = (size.width - side) / 2
        val top = (size.height - side) / 2.4f
        val r = CornerRadius(36.dp.toPx())
        drawRect(Color.Black.copy(alpha = 0.55f))
        drawRoundRect(Color.Transparent, Offset(left, top), Size(side, side), r, blendMode = BlendMode.Clear)
        val len = side * 0.16f
        val w = 5.dp.toPx()
        val c = Color.White
        fun corner(x: Float, yy: Float, dx: Float, dy: Float) {
            drawLine(c, Offset(x, yy), Offset(x + dx * len, yy), w, StrokeCap.Round)
            drawLine(c, Offset(x, yy), Offset(x, yy + dy * len), w, StrokeCap.Round)
        }
        val inset = 6.dp.toPx()
        corner(left + inset, top + inset, 1f, 1f); corner(left + side - inset, top + inset, -1f, 1f)
        corner(left + inset, top + side - inset, 1f, -1f); corner(left + side - inset, top + side - inset, -1f, -1f)
        val ly = top + side * (0.1f + 0.8f * y)
        drawLine(Brush.horizontalGradient(listOf(Color.Transparent, accent, Color.Transparent), left, left + side), Offset(left + 20f, ly), Offset(left + side - 20f, ly), 3.dp.toPx())
    }
}

/** Returns null on success (navigate back), or a message to show. */
suspend fun handleScanned(text: String): String? {
    val app = FerryApp.app
    val uri = runCatching { Uri.parse(text.trim()) }.getOrNull()
    return try {
        when {
            uri?.scheme == "ferry" && uri.host == "peer" -> {
                val hosts = (uri.getQueryParameter("h") ?: "").split(",").filter { it.isNotBlank() }
                val port = uri.getQueryParameter("p")?.toIntOrNull() ?: 53317
                val fp = uri.getQueryParameter("f") ?: ""
                val https = uri.getQueryParameter("s") != "http"
                var last: Exception? = null
                for (h in hosts) {
                    try {
                        val p = app.discovery.connect(h, port, fp, "qr", https)
                        toast(app, "Connected to ${p.alias}")
                        return null
                    } catch (e: Exception) {
                        last = e
                    }
                }
                "Couldn't reach that device (${last?.message ?: "no address"}). Make sure you're on the same Wi-Fi."
            }
            uri?.scheme == "ferry" && uri.host == "server" -> {
                PendingServer.url.value = uri.getQueryParameter("url")
                Nav.pending.value = "servers"
                null
            }
            else -> {
                connectInput(text); null
            }
        }
    } catch (e: Exception) {
        e.message ?: "Unrecognised QR code"
    }
}

@Composable
private fun CameraPreview(onCode: (String) -> Unit, onError: (String) -> Unit = {}) {
    val ctx = LocalContext.current
    val owner = LocalLifecycleOwner.current
    val executor = remember { Executors.newSingleThreadExecutor() }
    val reader = remember { MultiFormatReader().apply { setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE))) } }
    DisposableEffect(Unit) { onDispose { executor.shutdown() } }
    AndroidView(factory = { c ->
        // TextureView mode so the preview composes correctly under the Compose overlay (SurfaceView would sit behind the window).
        val view = PreviewView(c).apply { implementationMode = PreviewView.ImplementationMode.COMPATIBLE }
        val future = ProcessCameraProvider.getInstance(c)
        future.addListener({
            val provider = future.get()
            val preview = Preview.Builder().build().also { it.surfaceProvider = view.surfaceProvider }
            val analysis = ImageAnalysis.Builder().setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST).build()
            analysis.setAnalyzer(executor) { img ->
                try {
                    val plane = img.planes[0]
                    val buf = plane.buffer
                    val data = ByteArray(buf.remaining()).also { buf.get(it) }
                    val src = PlanarYUVLuminanceSource(data, plane.rowStride, img.height, 0, 0, img.width, img.height, false)
                    val result = runCatching { reader.decodeWithState(BinaryBitmap(HybridBinarizer(src))) }.getOrNull()
                    if (result != null) ContextCompat.getMainExecutor(ctx).execute { onCode(result.text) }
                } finally {
                    reader.reset()
                    img.close()
                }
            }
            runCatching {
                provider.unbindAll()
                val selector = if (provider.hasCamera(CameraSelector.DEFAULT_BACK_CAMERA)) CameraSelector.DEFAULT_BACK_CAMERA else CameraSelector.DEFAULT_FRONT_CAMERA
                provider.bindToLifecycle(owner, selector, preview, analysis)
            }.onFailure {
                android.util.Log.w("Ferry", "camera bind failed", it)
                onError("Camera unavailable — go back and type the pairing code instead.")
            }
        }, ContextCompat.getMainExecutor(c))
        view
    }, modifier = Modifier.fillMaxSize())
}
