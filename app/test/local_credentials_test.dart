import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/local_credentials.dart';
import 'package:heyrobots/platform.dart';

void main() {
  test('a computer keeps its pairing in a private file, and forgets it', () async {
    final dir = await Directory.systemTemp.createTemp('pairing');
    addTearDown(() => dir.delete(recursive: true));
    final store = FileCredentialStore(File('${dir.path}/heyrobots/pairing.json'));
    expect(await store.load(), isNull);

    await store.save(Credentials(server: Uri.parse('http://127.0.0.1:7340'), token: 'tok', deviceId: 'dv1'));
    final again = await FileCredentialStore(store.file).load();
    expect((again?.server.toString(), again?.token, again?.deviceId), ('http://127.0.0.1:7340', 'tok', 'dv1'));
    if (!Platform.isWindows) expect((await store.file.stat()).modeString(), 'rw-------');

    await store.clear();
    expect(await store.load(), isNull);
  });
}
