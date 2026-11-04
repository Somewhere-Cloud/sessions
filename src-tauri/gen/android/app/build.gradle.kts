import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("rust")
}

val tauriProperties = Properties().apply {
    val propFile = file("tauri.properties")
    if (propFile.exists()) {
        propFile.inputStream().use { load(it) }
    }
}

val sessionsTestApp = providers.gradleProperty("sessionsTestApp").orNull == "true"

// Release signing lives outside the repository (docs/ANDROID.md). The file is a
// Java properties file with storeFile, storePassword, keyAlias and keyPassword.
// Without it a release build is produced unsigned, and scripts/release-android.sh
// refuses to continue.
val releaseSigningFile = file(
    providers.environmentVariable("SESSIONS_ANDROID_SIGNING_PROPERTIES").orNull
        ?: "${System.getProperty("user.home")}/Library/Application Support/Sessions/signing/android-release.properties"
)
val releaseSigning = Properties().apply {
    if (releaseSigningFile.isFile) releaseSigningFile.inputStream().use { load(it) }
}

// One monotonic versionCode for every build: 0.2.48 -> 20048. Each component
// below the major version gets room for 100 minors and 10000 patches.
fun sessionsVersionCode(versionName: String): Int {
    val parts = versionName.substringBefore('-').substringBefore('+').split('.')
    require(parts.size == 3) { "Sessions versions are MAJOR.MINOR.PATCH, got $versionName" }
    val (major, minor, patch) = parts.map { it.toInt() }
    require(minor < 100 && patch < 10000) { "versionCode cannot encode $versionName" }
    return major * 1_000_000 + minor * 10_000 + patch
}
val sessionsVersionName = tauriProperties.getProperty("tauri.android.versionName", "0.0.1")

android {
    compileSdk = 36
    namespace = "tech.somewhere.sessions"
    defaultConfig {
        // Sessions hosts are frequently reached over a private LAN or through
        // Tailscale's encrypted network using an http:// daemon endpoint.
        // The app only connects to endpoints the user explicitly pairs or
        // enters; disabling cleartext globally would make those direct
        // connections fail in release builds.
        manifestPlaceholders["usesCleartextTraffic"] = "true"
        manifestPlaceholders["sessionsAppLabel"] = "Sessions"
        applicationId = "tech.somewhere.sessions"
        minSdk = 24
        targetSdk = 36
        versionCode = sessionsVersionCode(sessionsVersionName)
        versionName = sessionsVersionName
    }
    signingConfigs {
        if (releaseSigning.getProperty("storeFile") != null) {
            create("release") {
                storeFile = file(releaseSigning.getProperty("storeFile"))
                storePassword = releaseSigning.getProperty("storePassword")
                keyAlias = releaseSigning.getProperty("keyAlias")
                keyPassword = releaseSigning.getProperty("keyPassword")
            }
        }
    }
    buildTypes {
        getByName("debug") {
            applicationIdSuffix = ".debug"
            if (sessionsTestApp) {
                manifestPlaceholders["sessionsAppLabel"] = "Sessions Test"
            }
            manifestPlaceholders["usesCleartextTraffic"] = "true"
            isDebuggable = true
            isJniDebuggable = true
            isMinifyEnabled = false
            packaging {                jniLibs.keepDebugSymbols.add("*/arm64-v8a/*.so")
                jniLibs.keepDebugSymbols.add("*/armeabi-v7a/*.so")
                jniLibs.keepDebugSymbols.add("*/x86/*.so")
                jniLibs.keepDebugSymbols.add("*/x86_64/*.so")
            }
        }
        getByName("release") {
            signingConfigs.findByName("release")?.let { signingConfig = it }
            isMinifyEnabled = true
            proguardFiles(
                *fileTree(".") { include("**/*.pro") }
                    .plus(getDefaultProguardFile("proguard-android-optimize.txt"))
                    .toList().toTypedArray()
            )
        }
    }
    kotlinOptions {
        jvmTarget = "1.8"
    }
    buildFeatures {
        buildConfig = true
    }
}

rust {
    rootDirRel = "../../../"
}

dependencies {
    implementation("androidx.webkit:webkit:1.14.0")
    implementation("androidx.appcompat:appcompat:1.7.1")
    implementation("androidx.activity:activity-ktx:1.10.1")
    implementation("com.google.android.material:material:1.12.0")
    implementation("androidx.lifecycle:lifecycle-process:2.10.0")
    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test.ext:junit:1.1.4")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.5.0")
}

apply(from = "tauri.build.gradle.kts")
