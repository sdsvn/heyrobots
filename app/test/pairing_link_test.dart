import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/pairing.dart';

void main() {
  test('a sign-in link works only for a server on this machine', () {
    final ok = localPairingLink(Uri.parse('heyrobots://pair?server=http%3A%2F%2F127.0.0.1%3A7340&code=ABCDE-FGHJK'));
    expect(ok?.server.toString(), 'http://127.0.0.1:7340');
    expect(ok?.code, 'ABCDE-FGHJK');
    expect(localPairingLink(Uri.parse('heyrobots://pair?server=http%3A%2F%2Flocalhost%3A7340&code=X')), isNotNull);
    // A web page can open heyrobots:// links: it must never point the app at its own server.
    expect(localPairingLink(Uri.parse('heyrobots://pair?server=https%3A%2F%2Fevil.example&code=X')), isNull);
    expect(localPairingLink(Uri.parse('heyrobots://pair?server=http%3A%2F%2F192.168.1.5%3A7340&code=X')), isNull);
    expect(localPairingLink(Uri.parse('heyrobots://pair?server=http%3A%2F%2F127.0.0.1%3A7340')), isNull);
    expect(localPairingLink(Uri.parse('heyrobots://thread/1')), isNull);
  });
}
