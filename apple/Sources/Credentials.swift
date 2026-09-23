import Foundation
import Security

/// The device's own RTSP and ONVIF password, plus the identity NVRs key on.
///
/// Both are generated here and kept in the keychain. The password cannot be
/// hashed: ONVIF's UsernameToken and RTSP Digest each need the original to
/// verify a request. It is stored as "after first unlock, this device only",
/// so the camera keeps serving while the phone sits locked on a shelf, and
/// the secret never travels to another device in a backup.
enum Credentials {
    static let user = "admin"

    static func password() -> String { value(for: "password", generate: newPassword) }

    static func deviceUUID() -> String {
        value(for: "uuid", generate: { "urn:uuid:" + UUID().uuidString.lowercased() })
    }

    /// Replaces the password. Every NVR then has to be pointed at it again.
    @discardableResult
    static func rotate() -> String {
        let fresh = newPassword()
        store(fresh, for: "password")
        return fresh
    }

    // No look-alikes, alphanumeric only: it goes into an rtsp:// URL and into
    // NVR fields that often cap at 16 characters.
    private static func newPassword() -> String {
        let alphabet = Array("abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789")
        return String((0..<16).map { _ in alphabet[Int.random(in: 0..<alphabet.count)] })
    }

    private static func value(for key: String, generate: () -> String) -> String {
        if let existing = read(key) { return existing }
        let fresh = generate()
        store(fresh, for: key)
        return fresh
    }

    private static func query(_ key: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.anivarhq.chameleon",
            kSecAttrAccount as String: key,
        ]
    }

    private static func read(_ key: String) -> String? {
        var query = query(key)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    private static func store(_ value: String, for key: String) {
        SecItemDelete(query(key) as CFDictionary)
        var item = query(key)
        item[kSecValueData as String] = Data(value.utf8)
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(item as CFDictionary, nil)
    }
}
