package dev.ferry.app.lan

import android.content.Context
import dev.ferry.app.util.hex
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.cert.jcajce.JcaX509CertificateConverter
import org.bouncycastle.cert.jcajce.JcaX509v3CertificateBuilder
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import java.io.File
import java.math.BigInteger
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.Date
import javax.net.ssl.KeyManagerFactory
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLServerSocketFactory

/**
 * This device's LAN identity: a self-signed TLS certificate generated once and kept in app-private
 * storage. Its SHA-256 is the LocalSend "fingerprint" peers use to recognise (and pin) us.
 */
class Certs(ctx: Context) {
    private val file = File(ctx.filesDir, "lan-identity.p12")
    private val pass = "ferry".toCharArray() // file is app-private; the password only satisfies the PKCS12 format
    private val ks: KeyStore = load()
    val cert: X509Certificate = ks.getCertificate("lan") as X509Certificate
    val fingerprint: String = MessageDigest.getInstance("SHA-256").digest(cert.encoded).hex().uppercase()

    private fun load(): KeyStore {
        if (file.exists()) runCatching {
            return KeyStore.getInstance("PKCS12").apply { file.inputStream().use { load(it, pass) } }
        }
        val kp = KeyPairGenerator.getInstance("EC").apply { initialize(256, SecureRandom()) }.generateKeyPair()
        val name = X500Name("CN=Ferry")
        val now = System.currentTimeMillis()
        val holder = JcaX509v3CertificateBuilder(name, BigInteger(64, SecureRandom()), Date(now - 86_400_000L),
            Date(now + 10L * 365 * 86_400_000L), name, kp.public).build(JcaContentSignerBuilder("SHA256withECDSA").build(kp.private))
        val cert = JcaX509CertificateConverter().getCertificate(holder)
        val store = KeyStore.getInstance("PKCS12").apply {
            load(null, null)
            setKeyEntry("lan", kp.private, pass, arrayOf(cert))
        }
        file.outputStream().use { store.store(it, pass) }
        return store
    }

    fun serverSocketFactory(): SSLServerSocketFactory {
        val kmf = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm()).apply { init(ks, pass) }
        return SSLContext.getInstance("TLS").apply { init(kmf.keyManagers, null, null) }.serverSocketFactory
    }

    companion object {
        fun fingerprintOf(c: java.security.cert.Certificate): String = MessageDigest.getInstance("SHA-256").digest(c.encoded).hex().uppercase()
    }
}
