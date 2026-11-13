import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/main.dart';
import 'package:heyrobots/platform.dart';
import 'package:heyrobots/state.dart';
import 'package:heyrobots/ui/agents_screen.dart';
import 'package:heyrobots/ui/team_tab.dart';
import 'package:heyrobots/ui/thread_screen.dart';

import 'fake_server.dart';
import 'state_test.dart' show FakeVerifier;

void main() {
  late FakeServer server;
  late FakeVerifier verifier;
  late AppState state;
  late StreamController<Uri> links;

  setUp(() {
    server = FakeServer();
    verifier = FakeVerifier(VerifyResult.verified);
    links = StreamController<Uri>.broadcast();
    addTearDown(links.close);
    state = AppState(credentials: MemoryCredentialStore(), verifier: verifier, httpClient: server.client, liveUpdates: false);
  });

  Future<void> pump(WidgetTester tester) async {
    tester.view.physicalSize = const Size(1200, 2400);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(HeyrobotsApp(state: state, links: links.stream));
    await tester.runAsync(state.start);
    await tester.pumpAndSettle();
  }

  Future<void> pairByHand(WidgetTester tester, {String server = '192.168.1.20:7340'}) async {
    await tester.enterText(find.byKey(const Key('server')), server);
    await tester.enterText(find.byKey(const Key('code')), 'abcde-fghjk');
    await tester.tap(find.byKey(const Key('pair')));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();
  }

  Future<void> open(WidgetTester tester, String link) async {
    links.add(Uri.parse(link));
    await tester.pump();
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();
  }

  Future<void> push(WidgetTester tester, Widget screen) async {
    tester.state<NavigatorState>(find.byType(Navigator).first).push(MaterialPageRoute<void>(builder: (_) => screen));
    await tester.pump();
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();
  }

  testWidgets('pairs by hand and shows the three tabs, with what waits on the Plans badge', (tester) async {
    server.addApproval();
    await pump(tester);
    expect(find.text('Pair'), findsOneWidget);

    await pairByHand(tester);
    final nav = find.byKey(const Key('main-nav'));
    for (final t in ['Chats', 'Agents', 'Plans']) {
      expect(find.descendant(of: nav, matching: find.text(t)), findsOneWidget);
    }
    expect(find.descendant(of: nav, matching: find.text('1')), findsOneWidget);
  });

  testWidgets('refuses plain http to a public address', (tester) async {
    await pump(tester);
    await pairByHand(tester, server: 'http://heyrobots.example.com');
    expect(find.textContaining('needs https'), findsOneWidget);
    expect(server.requests, isEmpty);
  });

  testWidgets('approving shows the details and asks for biometrics', (tester) async {
    server.addApproval();
    await pump(tester);
    await pairByHand(tester);

    await open(tester, 'heyrobots://approval/ef_7');
    expect(find.text('Send this email?'), findsOneWidget);
    expect(find.text('bob@example.com'), findsOneWidget);
    expect(find.text('Hi Bob, Thursday works.'), findsOneWidget);
    expect(find.text('Check this detail'), findsOneWidget);

    await tester.tap(find.byKey(const Key('approve')));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();
    expect(verifier.asked, 1);
    expect(server.decisions['ef_7'], {'decision': 'approve', 'user_verified': true});
    expect(state.attention, isEmpty);
  });

  testWidgets('tapping an answer sends it as the reply', (tester) async {
    server.addQuestion();
    await pump(tester);
    await pairByHand(tester);
    await open(tester, 'heyrobots://thread/th_q');
    expect(find.byKey(const Key('choice:Window')), findsOneWidget);

    await tester.tap(find.byKey(const Key('choice:Aisle')));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 400)));
    await tester.pumpAndSettle();
    final sent = server.requests.where((r) => r.url.path == '/api/v1/messages').single;
    expect(jsonDecode(sent.body), containsPair('text', 'Aisle'));
    expect(jsonDecode(sent.body), containsPair('thread_id', 'th_q'));
    expect(find.byKey(const Key('choice:Window')), findsNothing);
    // Let the refresh after sending run.
    await tester.pump(const Duration(milliseconds: 400));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();
  });

  testWidgets('adds an agent to the team', (tester) async {
    await pump(tester);
    await pairByHand(tester);
    await push(tester, Scaffold(body: TeamTab(state: state)));
    expect(find.text('General'), findsWidgets);

    await tester.tap(find.byKey(const Key('add-agent')));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('agent-name')), 'Researcher');
    await tester.enterText(find.byKey(const Key('agent-purpose')), 'Finds things out.');
    await tester.tap(find.byKey(const Key('toolset:research')));
    await tester.tap(find.text('trip-planner'));
    await tester.scrollUntilVisible(find.text('sam'), 200, scrollable: find.byType(Scrollable).last);
    await tester.tap(find.text('sam'));
    await tester.pump();
    await tester.scrollUntilVisible(find.byKey(const Key('save-agent')), 200, scrollable: find.byType(Scrollable).last);
    await tester.tap(find.byKey(const Key('save-agent')));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();

    final post = server.requests.where((r) => r.method == 'POST' && r.url.path == '/api/v1/agents').single;
    final body = jsonDecode(post.body) as Map<String, Object?>;
    expect(body['name'], 'Researcher');
    expect(body['toolsets'], ['research']);
    expect(body['skills'], ['trip-planner']);
    expect(body['delegates'], ['sam']);
    expect(body['color'], 'sky');
    expect(find.text('Researcher'), findsOneWidget);

    // An agent's profile lists the standing jobs its files name.
    final general = (await tester.runAsync(state.api.listAgents))!.agents.firstWhere((a) => a.id == 'ag_general');
    await push(tester, AgentEditScreen(state: state, agent: general));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(find.byKey(const Key('job:brief')), 200, scrollable: find.byType(Scrollable).last);
    expect(find.text('Morning brief.'), findsOneWidget);
    expect(find.text('weekdays 08:00 · 2 steps'), findsOneWidget);
  });

  testWidgets('sends a new request', (tester) async {
    await pump(tester);
    await pairByHand(tester);
    await push(tester, ThreadScreen(state: state));
    await tester.enterText(find.byKey(const Key('message')), 'Find me a dentist near work');
    await tester.tap(find.byKey(const Key('send')));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 400)));
    await tester.pumpAndSettle();
    expect(find.text('Find me a dentist near work'), findsOneWidget);
    expect(find.text('Conversation'), findsOneWidget);
  });
}
