plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.anivarhq.chameleon"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.anivarhq.chameleon"
        // 24 is what gomobile builds the core against.
        minSdk = 24
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
    }

    // Release signing comes from the environment: CI decodes the key from the
    // repo's secrets. Without it a release build is simply unsigned, so anyone
    // can still run assembleRelease; only the key holder can publish one.
    val keystore = System.getenv("CHAMELEON_KEYSTORE")
    signingConfigs {
        if (keystore != null) {
            create("release") {
                storeFile = file(keystore)
                storeType = "pkcs12"
                storePassword = System.getenv("CHAMELEON_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("CHAMELEON_KEY_ALIAS")
                keyPassword = System.getenv("CHAMELEON_KEYSTORE_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (keystore != null) signingConfig = signingConfigs.getByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlin {
        compilerOptions { jvmToolchain(17) }
    }
}

dependencies {
    // The shared RTSP/ONVIF core: ../core, bound by gomobile. CI builds it
    // before assembling; scripts/build-core.sh does the same locally.
    implementation(files("libs/chameleon.aar"))
    implementation("androidx.core:core-ktx:1.17.0")
    implementation("androidx.appcompat:appcompat:1.7.1")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.10.2")
}
