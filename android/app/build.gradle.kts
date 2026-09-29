plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "dev.ferry.app"
    compileSdk = 36

    defaultConfig {
        applicationId = "dev.ferry.app"
        minSdk = 29
        targetSdk = 36
        versionCode = 6
        versionName = "1.5.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    signingConfigs {
        // Release signing comes from environment variables in CI; local release builds fall back to debug keys.
        create("release") {
            val ks = System.getenv("FERRY_KEYSTORE")
            if (ks != null) {
                storeFile = file(ks)
                storePassword = System.getenv("FERRY_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("FERRY_KEY_ALIAS")
                keyPassword = System.getenv("FERRY_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = if (System.getenv("FERRY_KEYSTORE") != null) signingConfigs.getByName("release") else signingConfigs.getByName("debug")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    packaging {
        resources.excludes += setOf("META-INF/versions/9/OSGI-INF/MANIFEST.MF", "META-INF/DEPENDENCIES", "META-INF/LICENSE*", "META-INF/NOTICE*")
    }
    testOptions { unitTests.isReturnDefaultValues = true }
}

dependencies {
    val composeBom = platform("androidx.compose:compose-bom:2024.10.01")
    implementation(composeBom)
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.compose.ui:ui-tooling-preview")
    debugImplementation("androidx.compose.ui:ui-tooling")
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.activity:activity-compose:1.9.2")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.4")
    implementation("androidx.lifecycle:lifecycle-process:2.8.4")
    implementation("androidx.navigation:navigation-compose:2.8.1")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("org.nanohttpd:nanohttpd:2.3.1")
    implementation("org.bouncycastle:bcpkix-jdk18on:1.79")
    implementation("com.google.zxing:core:3.5.3")
    implementation("androidx.camera:camera-camera2:1.6.2")
    implementation("androidx.camera:camera-lifecycle:1.6.2")
    implementation("androidx.camera:camera-view:1.6.2")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
}
