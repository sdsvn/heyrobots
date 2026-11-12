import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;

import 'api/session.dart';
import 'pairing.dart';
import 'platform.dart';

/// Everything the app shows, kept fresh from the server.
class AppState extends ChangeNotifier {
  AppState({
    required this.credentials,
    required this.verifier,
    this.httpClient,
    bool? streamEvents,
    this.pollEvery = const Duration(seconds: 15),
    this.liveUpdates = true,
  }) : streamEvents = streamEvents ?? !kIsWeb;

  /// Off in tests, which refresh by hand.
  final bool liveUpdates;

  final CredentialStore credentials;
  final UserVerifier verifier;

  /// Makes the HTTP client; tests pass a fake.
  final http.Client Function()? httpClient;

  /// Live updates come from server-sent events where the platform can
  /// stream a response. The browser build polls instead.
  final bool streamEvents;
  final Duration pollEvery;

  Credentials? _creds;
  ApiClient? _api;
  Device? me;
  List<AttentionItem> attention = const [];
  List<ThreadSummary> threads = const [];
  List<Digest> digests = const [];
  bool loading = true;
  bool live = false;
  String? error;

  final _threadChanges = StreamController<String>.broadcast();
  int _generation = 0;
  Timer? _refreshSoon;
  Timer? _poll;
  bool _disposed = false;

  bool get paired => _creds != null;
  bool get isOwner => me?.role == Role.owner;
  Uri? get server => _creds?.server;

  /// The API, as this device. Only valid while paired.
  ApiClient get api => _api!;

  /// Thread ids that changed on the server.
  Stream<String> get threadChanges => _threadChanges.stream;

  Future<void> start() async {
    _creds = await credentials.load();
    if (_creds != null) {
      _open();
      await refresh();
    }
    loading = false;
    _notify();
  }

  /// Pairs this device with a server using a one-time code.
  Future<void> pair(PairingPayload p, String deviceName) async {
    // Try each address the code names, best first; the first that answers
    // is the one this device keeps. A server that answers with an error,
    // such as a wrong code, ends the search.
    late final PairResponse res;
    late Uri server;
    Object? unreachable;
    var paired = false;
    for (final s in p.servers) {
      try {
        res = await openApi(s, inner: httpClient?.call())
            .pairDevice(body: PairRequest(code: p.code.trim(), deviceName: deviceName.trim(), platform: platformName()))
            .timeout(const Duration(seconds: 8));
        server = s;
        paired = true;
        break;
      } on TimeoutException catch (e) {
        unreachable = e;
      } on http.ClientException catch (e) {
        unreachable = e;
      }
    }
    if (!paired) throw unreachable ?? StateError('no address to pair with');
    _creds = Credentials(server: server, token: res.token, deviceId: res.device.id);
    await credentials.save(_creds!);
    me = res.device;
    _open();
    await refresh();
  }

  void _open() {
    _api = openApi(_creds!.server, token: _creds!.token, inner: httpClient?.call());
    final gen = ++_generation;
    if (!liveUpdates) return;
    if (streamEvents) {
      unawaited(_listen(gen));
    } else {
      _poll?.cancel();
      _poll = Timer.periodic(pollEvery, (_) => refresh());
    }
  }

  /// Reloads what the home screen shows.
  Future<void> refresh() async {
    final api = _api;
    if (api == null) return;
    try {
      me = await api.getMe();
      final results = await Future.wait([
        api.listAttention().then((r) => r.items),
        api.listThreads().then((r) => r.threads),
        if (me!.role == Role.owner) api.listDigests().then((r) => r.digests),
      ]);
      attention = results[0] as List<AttentionItem>;
      final before = {for (final t in threads) t.id: t.updatedAt};
      threads = results[1] as List<ThreadSummary>;
      // Open conversations reload when theirs changed, which matters where
      // there is no event stream.
      for (final t in threads) {
        if (before.isNotEmpty && before[t.id] != t.updatedAt) _threadChanges.add(t.id);
      }
      digests = results.length > 2 ? results[2] as List<Digest> : const [];
      error = null;
    } catch (e) {
      await _failed(e);
    }
    _notify();
  }

  Future<void> _failed(Object e) async {
    if (e is ApiError && e.unauthorized) {
      // The device was revoked: forget the token and pair again.
      await forget();
      error = 'This device was removed from your heyrobots server. Pair it again to continue.';
      return;
    }
    error = describeError(e);
  }

  /// Forgets the pairing on this device.
  Future<void> forget() async {
    _generation++;
    _poll?.cancel();
    _refreshSoon?.cancel();
    await credentials.clear();
    _creds = null;
    _api = null;
    me = null;
    attention = const [];
    threads = const [];
    digests = const [];
    live = false;
    _notify();
  }

  Future<void> _listen(int gen) async {
    var wait = const Duration(seconds: 1);
    while (!_disposed && gen == _generation) {
      try {
        await for (final ev in _api!.streamEvents()) {
          if (gen != _generation) return;
          if (!live) {
            live = true;
            wait = const Duration(seconds: 1);
            _notify();
            // Catch up on anything missed while disconnected.
            _scheduleRefresh();
          }
          _onEvent(ev['event'] as String? ?? '', ev['data'] as String? ?? '');
        }
      } on ApiError catch (e) {
        if (e.unauthorized) {
          await _failed(e);
          return;
        }
      } catch (_) {
        // Lost the connection; try again below.
      }
      if (gen != _generation) return;
      if (live) {
        live = false;
        _notify();
      }
      await Future<void>.delayed(wait);
      wait = Duration(seconds: min(wait.inSeconds * 2, 60));
    }
  }

  void _onEvent(String type, String data) {
    Map<String, Object?> body = const {};
    try {
      final d = jsonDecode(data);
      if (d is Map<String, Object?>) body = d;
    } on FormatException {
      // Ignore a malformed event; the next refresh corrects things.
    }
    if (type == 'device.revoked' && body['id'] == _creds?.deviceId) {
      unawaited(_failed(ApiError(401, 'revoked')));
      return;
    }
    if (type == 'thread.updated' && body['id'] is String) {
      _threadChanges.add(body['id'] as String);
    }
    _scheduleRefresh();
  }

  void _scheduleRefresh() {
    _refreshSoon?.cancel();
    _refreshSoon = Timer(const Duration(milliseconds: 300), refresh);
  }

  /// Sends a chat message, starting a new conversation without threadId.
  /// A chat with one agent or a group starts with agentId or groupId.
  Future<SendMessageResponse> send(String text, {String? threadId, String? agentId, String? groupId}) async {
    final res = await api.sendMessage(
      body: SendMessageRequest(clientMessageId: _messageId(), text: text.trim(), threadId: threadId, agentId: agentId, groupId: groupId),
    );
    _scheduleRefresh();
    return res;
  }

  /// Approves or rejects an action. Approving one that reaches other
  /// people or can't be undone first asks for Face ID, Touch ID or a
  /// fingerprint. Returns a message for the person when it didn't happen.
  Future<String?> decide(Approval a, {required bool approve, String? note}) async {
    var verified = false;
    if (approve && needsBiometrics(a.reach)) {
      switch (await verifier.verify('Approve sending this ${actionName(a.action)}')) {
        case VerifyResult.verified:
          verified = true;
        case VerifyResult.failed:
          return "Not approved: you weren't confirmed.";
        case VerifyResult.unavailable:
          return "This device can't check Face ID, Touch ID or a fingerprint. Approve this on your phone.";
      }
    }
    try {
      await api.decideApproval(
        approvalId: a.id,
        body: ApprovalDecision(
          decision: approve ? ApprovalDecisionDecision.approve : ApprovalDecisionDecision.reject,
          note: note == null || note.trim().isEmpty ? null : note.trim(),
          userVerified: verified ? true : null,
        ),
      );
    } catch (e) {
      return describeError(e);
    }
    await refresh();
    return null;
  }

  Future<String?> dismiss(AttentionItem item) async {
    try {
      await api.dismissAttention(itemId: item.id);
    } catch (e) {
      return describeError(e);
    }
    await refresh();
    return null;
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _generation++;
    _poll?.cancel();
    _refreshSoon?.cancel();
    _threadChanges.close();
    super.dispose();
  }
}

final _rand = Random.secure();

String _messageId() =>
    List.generate(16, (_) => _rand.nextInt(256).toRadixString(16).padLeft(2, '0')).join();

/// Actions that reach other people or can't be undone need biometrics.
bool needsBiometrics(String reach) => reach == 'others' || reach == 'irreversible';

/// "send_email" reads as "email".
String actionName(String action) {
  // An MCP tool's action is "server/tool".
  final slash = action.indexOf('/');
  if (slash > 0) return '${action.substring(slash + 1).split('_').join(' ')} on ${action.substring(0, slash)}';
  final words = action.split('_');
  if (words.length > 1 && words.first == 'send') words.removeAt(0);
  return words.join(' ');
}
