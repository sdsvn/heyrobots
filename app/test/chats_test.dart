import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/api/session.dart';
import 'package:heyrobots/pairing.dart';
import 'package:heyrobots/platform.dart';
import 'package:heyrobots/state.dart';
import 'package:heyrobots/ui/agent_profile_screen.dart';
import 'package:heyrobots/ui/chat_screen.dart';
import 'package:heyrobots/ui/chats_tab.dart';
import 'package:heyrobots/ui/new_group_screen.dart';
import 'package:heyrobots/ui/plans_tab.dart';
import 'package:heyrobots/ui/team_tab.dart';
import 'package:heyrobots/ui/theme.dart';

import 'fake_server.dart';
import 'state_test.dart' show FakeVerifier;

void main() {
  late FakeServer server;
  late AppState state;

  final coder = AgentRef(id: 'ag_coder', name: 'Coder', color: 'mint', badge: 'code');

  setUp(() {
    server = FakeServer()..addChats();
    state = AppState(credentials: MemoryCredentialStore(), verifier: FakeVerifier(VerifyResult.verified), httpClient: server.client, liveUpdates: false);
  });

  Future<void> settle(WidgetTester tester) async {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();
  }

  Future<void> show(WidgetTester tester, Widget screen) async {
    await tester.runAsync(() async {
      await state.start();
      await state.pair(PairingPayload(Uri.parse('http://192.168.1.2:7340'), 'ABCDE-FGHJK'), 'phone');
    });
    tester.view.physicalSize = const Size(780, 1688);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(MaterialApp(theme: buildTheme(Brightness.light), home: Scaffold(body: screen)));
    await settle(tester);
  }

  testWidgets('the chats tab shows who is typing and what is unread', (tester) async {
    await show(tester, ChatsTab(state: state));
    expect(find.text('Chats'), findsOneWidget);
    expect(find.text('Launch v2'), findsOneWidget);
    expect(find.text('Coder'), findsOneWidget);
    expect(find.text('thinking…'), findsOneWidget);
    expect(find.descendant(of: find.byKey(const Key('unread:th_group')), matching: find.text('3')), findsOneWidget);
    expect(find.text('1 agent working, outside any chat'), findsOneWidget);

    await tester.tap(find.text('Groups'));
    await tester.pump();
    expect(find.text('Coder'), findsNothing);
    expect(find.text('Launch v2'), findsOneWidget);
  });

  testWidgets('on a wide screen the open chat sits beside the list', (tester) async {
    await show(tester, ChatsTab(state: state));
    tester.view.physicalSize = const Size(2800, 1800);
    await settle(tester);
    // The latest chat opens beside the list, with no way "back".
    expect(find.byType(ChatScreen), findsOneWidget);
    expect(find.byTooltip('Back to chats'), findsNothing);
    expect(find.text('Launch v2'), findsNWidgets(2));
    await tester.tap(find.text('Coder').first);
    await settle(tester);
    // Picking another chat swaps the main window; the list stays.
    expect(find.byType(ChatScreen), findsOneWidget);
    expect(find.text('Launch v2'), findsOneWidget);
    expect(find.text('Chats'), findsOneWidget);
  });

  testWidgets('a chat switches model, and an agent opens beside it on a wide screen', (tester) async {
    await show(tester, ChatScreen(state: state, threadId: 'th_coder', agent: coder));
    tester.view.physicalSize = const Size(2800, 1800);
    await settle(tester);
    expect(find.text('deepseek-chat'), findsOneWidget);
    await tester.tap(find.byKey(const Key('model-picker')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('model:deepseek-reasoner')));
    await settle(tester);
    final put = server.requests.where((r) => r.method == 'PUT' && r.url.path == '/api/v1/chat-model').single;
    expect(jsonDecode(put.body), {'agent_id': 'ag_coder', 'model': 'deepseek-reasoner'});
    expect(find.text('deepseek-reasoner'), findsOneWidget);

    // The agent's profile opens beside the chat, and closes again.
    await tester.tap(find.byKey(const Key('header-name')));
    await settle(tester);
    expect(find.byType(AgentProfileScreen), findsOneWidget);
    expect(find.byType(ChatScreen), findsOneWidget);
    await tester.tap(find.byKey(const Key('close-panel')));
    await settle(tester);
    expect(find.byType(AgentProfileScreen), findsNothing);
  });

  testWidgets('sending in a new direct chat starts it with the agent', (tester) async {
    await show(tester, ChatScreen(state: state, agent: coder));
    expect(find.text('Say hi to Coder'), findsOneWidget);
    expect(find.text('Code'), findsOneWidget);

    await tester.enterText(find.byKey(const Key('message')), 'Fix the flaky test');
    await tester.tap(find.byKey(const Key('send')));
    await settle(tester);
    final sent = server.requests.where((r) => r.url.path == '/api/v1/messages').single;
    expect(jsonDecode(sent.body), containsPair('agent_id', 'ag_coder'));
    expect(jsonDecode(sent.body), isNot(contains('thread_id')));
    expect(find.text('Fix the flaky test'), findsOneWidget);
    expect(find.bySemanticsLabel('Seen'), findsNothing);
  });

  testWidgets('a chat shows the card an agent shared and is marked read', (tester) async {
    await show(tester, ChatScreen(state: state, threadId: 'th_coder', agent: coder));
    expect(find.text('Done. They run at 02:00.'), findsOneWidget);
    expect(find.text('Nightly test run'), findsOneWidget);
    expect(find.text('Every day 02:00 · first one tonight'), findsOneWidget);
    expect(find.text('Plans'), findsOneWidget);
    expect(server.requests.where((r) => r.method == 'POST' && r.url.path == '/api/v1/threads/th_coder/read'), hasLength(1));
  });

  testWidgets('makes a group and opens its chat', (tester) async {
    await show(tester, NewGroupScreen(state: state));
    await tester.enterText(find.byKey(const Key('group-name')), 'Trip planning');
    await tester.tap(find.byKey(const Key('member:Coder')));
    await tester.pump();
    expect(find.text('MEMBERS · 1'), findsOneWidget);
    await tester.tap(find.byKey(const Key('create-group')));
    await settle(tester);
    final post = server.requests.where((r) => r.method == 'POST' && r.url.path == '/api/v1/groups').single;
    expect(jsonDecode(post.body), {'name': 'Trip planning', 'members': ['ag_coder']});
    expect(find.text('Message the group…'), findsOneWidget);
  });

  testWidgets('the team tab shows a working agent', (tester) async {
    await show(tester, TeamTab(state: state));
    expect(find.text('Team'), findsOneWidget);
    expect(find.descendant(of: find.byKey(const Key('team:General')), matching: find.text('Working')), findsOneWidget);
    expect(find.text('Plan Lisbon'), findsOneWidget);
    expect(find.text('Idle'), findsOneWidget);
  });

  testWidgets('plans show what is running and the scheduled jobs', (tester) async {
    await show(tester, PlansTab(state: state));
    expect(find.text('NOW'), findsOneWidget);
    expect(find.text('Plan Lisbon'), findsOneWidget);
    await tester.tap(find.byKey(const Key('range:Week')));
    await tester.pump();
    expect(find.text('COMING UP THIS WEEK'), findsOneWidget);
    expect(find.text('Morning brief'), findsWidgets);
    expect(find.text('1 standing job across the team'), findsOneWidget);
  });
}
