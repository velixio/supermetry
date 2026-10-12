plugins {
    id("com.android.library")
    kotlin("android")
    `maven-publish`
}

android {
    namespace = "com.supermetry"
    compileSdk = 34

    defaultConfig {
        minSdk = 21
        consumerProguardFiles("consumer-rules.pro")
    }

    sourceSets["main"].java.srcDirs(
        "src/main/kotlin",
        // The platform-independent core is compiled into this same artifact.
        "../supermetry-core/src/main/kotlin",
    )

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_1_8
        targetCompatibility = JavaVersion.VERSION_1_8
    }

    kotlinOptions {
        jvmTarget = "1.8"
    }

    lint {
        abortOnError = true
    }

    publishing {
        singleVariant("release") {
            withSourcesJar()
        }
    }
}

publishing {
    publications {
        register<MavenPublication>("release") {
            groupId = "com.supermetry"
            artifactId = "supermetry-android"
            version = "0.1.0"
            afterEvaluate { from(components["release"]) }
            pom {
                name.set("Supermetry for Android")
                description.set("Event tracking for Supermetry, self-hosted web analytics.")
                licenses {
                    license {
                        name.set("Apache-2.0")
                        url.set("https://www.apache.org/licenses/LICENSE-2.0")
                    }
                }
            }
        }
    }
}
