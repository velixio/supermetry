pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "supermetry-android-sdk"

// Pure Kotlin/JVM core: event building, validation, queue, retry, HTTP. Builds and tests with
// plain Gradle (no Android SDK needed).
include(":supermetry-core")

// Android library (com.supermetry:supermetry-android). It compiles the core sources into the
// same artifact and adds SharedPreferences, lifecycle flushing, UA and screen width. Only
// included when an Android SDK is available.
val hasAndroidSdk = System.getenv("ANDROID_HOME") != null ||
    System.getenv("ANDROID_SDK_ROOT") != null ||
    file("local.properties").let { it.exists() && it.readText().contains("sdk.dir") }
if (hasAndroidSdk) {
    include(":supermetry-android")
}
