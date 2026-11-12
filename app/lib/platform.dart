import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:local_auth/local_auth.dart';

/// The pairing this device holds.
class Credentials {
  const Credentials({required this.server, required this.token, required this.deviceId});

  final Uri server;
  final String token;
  final String deviceId;
}

/// Keeps the device token.
abstract class CredentialStore {
  Future<Credentials?> load();
  Future<void> save(Credentials c);
  Future<void> clear();
}

/// Keeps the token in the Keychain on Apple devices and the Keystore on
/// Android.
class SecureCredentialStore implements CredentialStore {
  final _storage = const FlutterSecureStorage();

  static const _server = 'server';
  static const _token = 'device_token';
  static const _device = 'device_id';

  @override
  Future<Credentials?> load() async {
    final server = await _storage.read(key: _server);
    final token = await _storage.read(key: _token);
    final device = await _storage.read(key: _device);
    if (server == null || token == null || device == null) return null;
    return Credentials(server: Uri.parse(server), token: token, deviceId: device);
  }

  @override
  Future<void> save(Credentials c) async {
    await _storage.write(key: _server, value: c.server.toString());
    await _storage.write(key: _token, value: c.token);
    await _storage.write(key: _device, value: c.deviceId);
  }

  @override
  Future<void> clear() async {
    await _storage.delete(key: _token);
    await _storage.delete(key: _device);
    await _storage.delete(key: _server);
  }
}

/// Keeps the token in memory only, for tests and previews.
class MemoryCredentialStore implements CredentialStore {
  MemoryCredentialStore([this._saved]);

  Credentials? _saved;

  @override
  Future<Credentials?> load() async => _saved;

  @override
  Future<void> save(Credentials c) async => _saved = c;

  @override
  Future<void> clear() async => _saved = null;
}

enum VerifyResult { verified, failed, unavailable }

/// Confirms the person holding the device, before approving an action.
abstract class UserVerifier {
  Future<VerifyResult> verify(String reason);
}

/// Asks for Face ID, Touch ID or a fingerprint. A passcode isn't enough:
/// someone who saw it typed could approve actions on the owner's accounts.
class BiometricVerifier implements UserVerifier {
  final _auth = LocalAuthentication();

  @override
  Future<VerifyResult> verify(String reason) async {
    if (kIsWeb) return VerifyResult.unavailable;
    try {
      if (!await _auth.canCheckBiometrics) return VerifyResult.unavailable;
      final ok = await _auth.authenticate(localizedReason: reason, biometricOnly: true);
      return ok ? VerifyResult.verified : VerifyResult.failed;
    } on MissingPluginException {
      // No fingerprint support on this platform, such as Linux.
      return VerifyResult.unavailable;
    } on LocalAuthException catch (e) {
      switch (e.code) {
        case LocalAuthExceptionCode.noBiometricHardware:
        case LocalAuthExceptionCode.noBiometricsEnrolled:
        case LocalAuthExceptionCode.biometricHardwareTemporarilyUnavailable:
          return VerifyResult.unavailable;
        default:
          return VerifyResult.failed;
      }
    }
  }
}

/// The platform name sent when pairing.
/// Whether this device is a phone or tablet app. Those never run on the
/// server's own machine, so they can't finish a sign-in that returns to it.
bool get isPhoneApp => !kIsWeb && (defaultTargetPlatform == TargetPlatform.android || defaultTargetPlatform == TargetPlatform.iOS);

String platformName() {
  if (kIsWeb) return 'web';
  return defaultTargetPlatform.name;
}
