import 'dart:convert';

import 'package:http/testing.dart';

import 'package:http/http.dart' as http;

import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/api/session.dart';
import 'package:heyrobots/pairing.dart';
import 'package:heyrobots/platform.dart';
import 'package:heyrobots/state.dart';

import 'fake_server.dart';

class FakeVerifier implements UserVerifier {
  FakeVerifier(this.result);

  VerifyResult result;
  int asked = 0;

  @override
  Future<VerifyResult> verify(String reason) async {
    asked++;
    return result;
  }
}

void main() {
  late FakeServer server;
  late MemoryCredentialStore store;
  late FakeVerifier verifier;

  AppState newState() => AppState(
    credentials: store,
    verifier: verifier,
    httpClient: server.client,
    liveUpdates: false,
  );

  setUp(() {
    server = FakeServer();
    store = MemoryCredentialStore();
    verifier = FakeVerifier(VerifyResult.verified);
  });

  final payload = PairingPayload(Uri.parse('http://192.168.1.20:7340'), 'ABCDE-FGHJK');

  test('a code with several addresses pairs with the first that answers', () async {
    final parsed = PairingPayload.parse(jsonEncode({
      'v': 1,
      'server': 'http://100.101.102.103:7340',
      'servers': ['http://100.101.102.103:7340', 'http://192.168.1.20:7340', 'http://8.8.8.8:7340'],
      'code': 'ABCDE-FGHJK',
    }));
    // Plain http on the internet is dropped; private addresses stay, in order.
    expect(parsed.servers.map((u) => u.host), ['100.101.102.103', '192.168.1.20']);
    final s = AppState(
      credentials: store,
      verifier: verifier,
      // The tailnet address doesn't answer from here; the home network does.
      httpClient: () => MockClient((r) async {
        if (r.url.host == '100.101.102.103') throw http.ClientException('no route', r.url);
        final copy = http.Request(r.method, r.url)
          ..headers.addAll(r.headers)
          ..bodyBytes = r.bodyBytes;
        return http.Response.fromStream(await server.client().send(copy));
      }),
      liveUpdates: false,
    );
    await s.start();
    await s.pair(parsed, 'My phone');
    expect((await store.load())!.server.host, '192.168.1.20');
  });

  test('pairing saves the token and loads what waits', () async {
    server.addApproval();
    final s = newState();
    await s.start();
    expect(s.paired, isFalse);
    await s.pair(payload, 'My phone');
    expect(s.paired, isTrue);
    expect((await store.load())!.token, 'tok_owner');
    expect(s.attention.single.approvalId, 'ef_7');
    expect(s.isOwner, isTrue);
    final pair = server.requests.first;
    expect(jsonDecode(pair.body), containsPair('device_name', 'My phone'));

    // A restarted app finds its pairing.
    final again = newState();
    await again.start();
    expect(again.paired, isTrue);
    expect(again.attention, hasLength(1));
  });

  test("a wrong code shows the server's message", () async {
    final s = newState();
    await s.start();
    await expectLater(
      s.pair(PairingPayload(payload.server, 'WRONG'), 'x'),
      throwsA(isA<ApiError>().having((e) => e.message, 'message', 'That pairing code is wrong or expired.')),
    );
    expect(s.paired, isFalse);
  });

  test('a revoked device forgets its token', () async {
    final s = newState();
    await s.start();
    await s.pair(payload, 'My phone');
    server.revoked = true;
    await s.refresh();
    expect(s.paired, isFalse);
    expect(await store.load(), isNull);
    expect(s.error, contains('removed'));
  });

  test('approving an email to someone else asks for biometrics first', () async {
    server.addApproval();
    final s = newState();
    await s.start();
    await s.pair(payload, 'My phone');
    final a = (await s.api.listApprovals()).approvals.single;

    verifier.result = VerifyResult.unavailable;
    expect(await s.decide(a, approve: true), contains("can't check Face ID"));
    verifier.result = VerifyResult.failed;
    expect(await s.decide(a, approve: true), contains('Not approved'));
    expect(server.decisions, isEmpty, reason: 'nothing is sent without the person confirmed');

    verifier.result = VerifyResult.verified;
    expect(await s.decide(a, approve: true), isNull);
    expect(server.decisions['ef_7'], {'decision': 'approve', 'user_verified': true});
    expect(verifier.asked, 3);
    expect(s.attention, isEmpty);
  });

  test('declining needs no biometrics and passes the note', () async {
    server.addApproval();
    final s = newState();
    await s.start();
    await s.pair(payload, 'My phone');
    final a = (await s.api.listApprovals()).approvals.single;
    expect(await s.decide(a, approve: false, note: ' Ask Ann first. '), isNull);
    expect(server.decisions['ef_7'], {'decision': 'reject', 'note': 'Ask Ann first.'});
    expect(verifier.asked, 0);
  });

  test('a message to self needs no biometrics', () async {
    server.addApproval(reach: 'owner');
    final s = newState();
    await s.start();
    await s.pair(payload, 'My phone');
    final a = (await s.api.listApprovals()).approvals.single;
    expect(await s.decide(a, approve: true), isNull);
    expect(verifier.asked, 0);
  });

  test('actionName', () {
    expect(actionName('send_email'), 'email');
    expect(actionName('create_event'), 'create event');
  });
}
