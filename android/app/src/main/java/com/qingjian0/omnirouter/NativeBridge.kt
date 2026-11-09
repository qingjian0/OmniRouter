package com.qingjian0.omnirouter

internal object NativeBridge {
    init {
        System.loadLibrary("omnirouter")
        System.loadLibrary("omnirouter_jni")
    }
    external fun nativeStart(dataDir: String, gatewayKey: String, encryptionKey: String): Int
    external fun nativeStop(): Int
}
