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
        flatDir { dirs("app/libs") } // chameleon.aar, built from ../core by gomobile
    }
}

rootProject.name = "ChameleonIP"
include(":app")
