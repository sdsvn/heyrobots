import 'package:web/web.dart' as web;

import 'platform.dart';

/// The web app's pairing, kept in this site's local storage so a reload
/// stays signed in. The app is served by the heyrobots server itself, so
/// only pages from that server can read it.
CredentialStore? localCredentialStore() => _LocalStorageStore();

class _LocalStorageStore implements CredentialStore {
  static const _server = 'heyrobots.server', _token = 'heyrobots.device_token', _device = 'heyrobots.device_id';

  web.Storage get _s => web.window.localStorage;

  @override
  Future<Credentials?> load() async {
    final server = _s.getItem(_server), token = _s.getItem(_token), device = _s.getItem(_device);
    if (server == null || token == null || device == null) return null;
    return Credentials(server: Uri.parse(server), token: token, deviceId: device);
  }

  @override
  Future<void> save(Credentials c) async {
    _s.setItem(_server, c.server.toString());
    _s.setItem(_token, c.token);
    _s.setItem(_device, c.deviceId);
  }

  @override
  Future<void> clear() async {
    for (final k in [_server, _token, _device]) {
      _s.removeItem(k);
    }
  }
}
