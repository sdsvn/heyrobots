import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/pairing.dart';

void main() {
  test('reads the QR code the setup page shows', () {
    final p = PairingPayload.parse('{"v":1,"server":"http://127.0.0.1:7340","code":"ABCDE-FGHJK"}');
    expect(p.server.toString(), 'http://127.0.0.1:7340');
    expect(p.code, 'ABCDE-FGHJK');
    expect(PairingPayload.parse(p.encode()).code, 'ABCDE-FGHJK');
  });

  test('refuses other QR codes', () {
    for (final raw in ['https://example.com', '{"v":2,"server":"https://a","code":"x"}', '{"server":"https://a"}', '[]']) {
      expect(() => PairingPayload.parse(raw), throwsFormatException, reason: raw);
    }
  });

  test('plain http only on this machine or a private network', () {
    for (final ok in [
      'http://localhost:7340',
      'http://127.0.0.1:7340',
      'http://192.168.1.20:7340',
      'http://10.0.0.5',
      'http://172.20.1.1',
      'http://100.101.102.103:7340',
      'http://mac-mini.local:7340',
      'http://[fd7a:115c:a1e0::1]:7340',
      'https://heyrobots.example.com',
      'mac-mini.tailnet.ts.net:7340',
    ]) {
      expect(() => parseServer(ok), returnsNormally, reason: ok);
    }
    for (final bad in [
      'http://heyrobots.example.com',
      'http://8.8.8.8',
      'http://172.32.0.1',
      'http://100.128.0.1',
      'ftp://192.168.1.2',
      'https://user:pw@example.com',
      'https://example.com/?x=1',
      '',
    ]) {
      expect(() => parseServer(bad), throwsFormatException, reason: bad);
    }
  });

  test('an address without a scheme means https', () {
    expect(parseServer('mac-mini.tailnet.ts.net:7340/').toString(), 'https://mac-mini.tailnet.ts.net:7340');
  });
}
