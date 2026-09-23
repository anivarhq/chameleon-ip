package com.anivarhq.chameleon

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

/**
 * The device's own RTSP/ONVIF password.
 *
 * It is generated here, never chosen by a person, and never reused anywhere
 * else, so it behaves like an API key. It has to be stored recoverable rather
 * than hashed, because ONVIF's UsernameToken and RTSP Digest both need the
 * original password to verify a request. What protects it is the Android
 * keystore: the key that encrypts it never leaves the secure hardware.
 */
object Credentials {
    const val USER = "admin"

    // No look-alikes (0/O, 1/l/I), and alphanumeric only: it goes into an
    // rtsp:// URL and into NVR fields that often cap at 16 characters.
    private const val ALPHABET = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
    private const val LENGTH = 16

    private const val PREFS = "chameleon"
    private const val KEY_PASS = "pass"
    private const val KEY_UUID = "uuid"
    private const val KEYSTORE_ALIAS = "chameleon-credentials"
    private const val TRANSFORM = "AES/GCM/NoPadding"
    private const val IV_BYTES = 12
    private const val TAG_BITS = 128

    /** The stored password, generated on first call. */
    fun password(context: Context): String {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        prefs.getString(KEY_PASS, null)?.let { stored ->
            decrypt(stored)?.let { return it }
            // Undecryptable, e.g. restored onto another device without the
            // key: start over rather than serve with a password nobody knows.
        }
        val fresh = generate()
        prefs.edit().putString(KEY_PASS, encrypt(fresh)).apply()
        return fresh
    }

    /**
     * The identity NVRs key this camera on. UniFi Protect and Home Assistant
     * match cameras by it, so a fresh one on every launch would look like a
     * fresh camera on every launch.
     */
    fun deviceUuid(context: Context): String {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        prefs.getString(KEY_UUID, null)?.let { return it }
        val fresh = "urn:uuid:" + java.util.UUID.randomUUID()
        prefs.edit().putString(KEY_UUID, fresh).apply()
        return fresh
    }

    /** Replaces the password; every NVR must then be re-pointed at this camera. */
    fun rotate(context: Context): String {
        val fresh = generate()
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit().putString(KEY_PASS, encrypt(fresh)).apply()
        return fresh
    }

    private fun generate(): String {
        val rng = SecureRandom()
        return (1..LENGTH).map { ALPHABET[rng.nextInt(ALPHABET.length)] }.joinToString("")
    }

    private fun secretKey(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getEntry(KEYSTORE_ALIAS, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }

        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        generator.init(
            KeyGenParameterSpec.Builder(
                KEYSTORE_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                // Readable after the first unlock following a reboot, so the
                // camera can serve while the phone sits locked on a shelf.
                .setUserAuthenticationRequired(false)
                .build()
        )
        return generator.generateKey()
    }

    private fun encrypt(value: String): String {
        val cipher = Cipher.getInstance(TRANSFORM).apply { init(Cipher.ENCRYPT_MODE, secretKey()) }
        val out = cipher.iv + cipher.doFinal(value.toByteArray())
        return Base64.encodeToString(out, Base64.NO_WRAP)
    }

    private fun decrypt(stored: String): String? = runCatching {
        val raw = Base64.decode(stored, Base64.NO_WRAP)
        val cipher = Cipher.getInstance(TRANSFORM).apply {
            init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(TAG_BITS, raw, 0, IV_BYTES))
        }
        String(cipher.doFinal(raw, IV_BYTES, raw.size - IV_BYTES))
    }.getOrNull()
}
