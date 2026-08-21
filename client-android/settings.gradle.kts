pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
        // PhotoView (com.github.chrisbanes:PhotoView) is published via JitPack.
        maven("https://jitpack.io")
    }
}
rootProject.name = "PocketNASClient"
include(":app")
