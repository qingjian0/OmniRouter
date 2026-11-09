package com.qingjian0.omnirouter

internal object NativeBridge {
    @Volatile private var coreLibraryLoaded = false
    @Volatile private var jniLibraryLoaded = false

    // Load libraries explicitly from the foreground service. Keeping this out of
    // object initialization lets the caller report the exact failing library
    // instead of poisoning the Kotlin object and later surfacing NoClassDefFoundError.
    fun loadCoreLibrary() {
        if (!coreLibraryLoaded) {
            System.loadLibrary("omnirouter")
            coreLibraryLoaded = true
        }
    }

    fun loadJniLibrary() {
        if (!jniLibraryLoaded) {
            System.loadLibrary("omnirouter_jni")
            jniLibraryLoaded = true
        }
    }

    external fun nativeStart(dataDir: String, gatewayKey: String, encryptionKey: String): Int
    external fun nativePair(token: ByteArray, sessionCookie: ByteArray): Int
    external fun nativeStop(): Int
    external fun nativeGetStartupStage(): Int
    external fun nativeGetStartupFailedAt(): Int
}
