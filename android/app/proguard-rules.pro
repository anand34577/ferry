# NanoHTTPD and BouncyCastle use reflection for providers.
-keep class org.bouncycastle.** { *; }
-dontwarn org.bouncycastle.**
-keep class fi.iki.elonen.** { *; }
-dontwarn javax.naming.**
-dontwarn okhttp3.internal.platform.**
-dontwarn org.conscrypt.**
-dontwarn org.openjsse.**
