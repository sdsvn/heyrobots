import 'dart:convert';
import 'dart:io';

import 'platform.dart';

/// Where this device keeps its pairing, when not in the system keychain.
///
/// On a Mac or Linux computer it is a file only this user can read, in the
/// app's own folder, like the server's own keys. The Mac app is signed
/// ad hoc, so each update looks like a new app to the keychain, which then
/// asks for the login password on every read. Phones keep the keychain or
/// keystore.
CredentialStore? localCredentialStore() {
  final home = Platform.environment['HOME'];
  if (home == null || !(Platform.isMacOS || Platform.isLinux)) return null;
  final dir = Platform.isMacOS ? '$home/Library/Application Support/heyrobots' : '${Platform.environment['XDG_CONFIG_HOME'] ?? '$home/.config'}/heyrobots';
  return FileCredentialStore(File('$dir/pairing.json'));
}

/// Keeps the pairing in a file readable only by this user.
class FileCredentialStore implements CredentialStore {
  FileCredentialStore(this.file);

  final File file;

  @override
  Future<Credentials?> load() async {
    try {
      final m = jsonDecode(await file.readAsString()) as Map<String, Object?>;
      final (server, token, device) = (m['server'], m['token'], m['device_id']);
      if (server is! String || token is! String || device is! String) return null;
      return Credentials(server: Uri.parse(server), token: token, deviceId: device);
    } on Object {
      return null;
    }
  }

  @override
  Future<void> save(Credentials c) async {
    await file.parent.create(recursive: true);
    final tmp = File('${file.path}.tmp');
    await tmp.writeAsString(jsonEncode({'server': c.server.toString(), 'token': c.token, 'device_id': c.deviceId}), flush: true);
    // Only this user may read it. The Mac app's folder is private already,
    // and a sandboxed app may not run chmod, so a failure here is fine.
    try {
      await Process.run('chmod', ['600', tmp.path]);
    } on Object {
      // Kept as the folder allows.
    }
    await tmp.rename(file.path);
  }

  @override
  Future<void> clear() async {
    if (await file.exists()) await file.delete();
  }
}
