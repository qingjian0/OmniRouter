package com.qingjian0.omnirouter

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

internal data class GatewaySecrets(val gatewayKey: String, val credentialEncryptionKey: String)

internal class AndroidSecretStore(context: Context) {
    private val prefs = context.getSharedPreferences("omnirouter_secure_state", Context.MODE_PRIVATE)
    private val alias = "omnirouter.gateway.secrets.v1"

    fun loadOrCreate(): GatewaySecrets {
        val ivText = prefs.getString("iv", null)
        val ciphertextText = prefs.getString("ciphertext", null)
        if (ivText != null && ciphertextText != null) {
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.DECRYPT_MODE, getOrCreateKey(), GCMParameterSpec(128, Base64.decode(ivText, Base64.NO_WRAP)))
            val plaintext = cipher.doFinal(Base64.decode(ciphertextText, Base64.NO_WRAP)).toString(Charsets.UTF_8)
            val fields = plaintext.split("\n")
            require(fields.size == 2) { "Stored gateway secret payload is invalid" }
            return GatewaySecrets(fields[0], fields[1])
        }

        val random = SecureRandom()
        val gatewayBytes = ByteArray(32).also(random::nextBytes)
        val encryptionBytes = ByteArray(32).also(random::nextBytes)
        val gatewayKey = gatewayBytes.joinToString("") { "%02x".format(it.toInt() and 0xff) }
        val encryptionKey = Base64.encodeToString(encryptionBytes, Base64.NO_WRAP)
        gatewayBytes.fill(0)
        encryptionBytes.fill(0)

        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey())
        val encrypted = cipher.doFinal("$gatewayKey\n$encryptionKey".toByteArray(Charsets.UTF_8))
        val saved = prefs.edit()
            .putString("iv", Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .putString("ciphertext", Base64.encodeToString(encrypted, Base64.NO_WRAP))
            .commit()
        check(saved) { "Unable to persist encrypted gateway secrets" }
        return GatewaySecrets(gatewayKey, encryptionKey)
    }

    private fun getOrCreateKey(): SecretKey {
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (keyStore.getKey(alias, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        generator.init(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .setRandomizedEncryptionRequired(true)
                .build()
        )
        return generator.generateKey()
    }
}
