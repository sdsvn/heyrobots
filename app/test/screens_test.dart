import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/main.dart';
import 'package:heyrobots/pairing.dart';
import 'package:heyrobots/platform.dart';
import 'package:heyrobots/state.dart';
import 'package:heyrobots/ui/app_screen.dart';
import 'package:heyrobots/ui/connect_apps_screen.dart';
import 'package:heyrobots/ui/settings_screen.dart';
import 'package:heyrobots/ui/skills_screen.dart';
import 'package:heyrobots/ui/theme.dart';

import 'fake_server.dart';
import 'state_test.dart' show FakeVerifier;

void main() {
  late FakeServer server;
  late AppState state;

  setUp(() {
    server = FakeServer();
    state = AppState(credentials: MemoryCredentialStore(), verifier: FakeVerifier(VerifyResult.verified), httpClient: server.client, liveUpdates: false);
  });

  Future<void> settle(WidgetTester tester) async {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pumpAndSettle();
  }

  /// Shows one screen on a 390 by 844 phone, paired as the owner.
  Future<void> show(WidgetTester tester, Widget screen) async {
    await tester.runAsync(() async {
      await state.start();
      await state.pair(PairingPayload(Uri.parse('http://192.168.1.2:7340'), 'ABCDE-FGHJK'), 'phone');
    });
    tester.view.physicalSize = const Size(780, 1688);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(MaterialApp(theme: buildTheme(Brightness.light), home: screen));
    await settle(tester);
  }

  Iterable<Map<String, Object?>> bodies(String method, String path) =>
      server.requests.where((r) => r.method == method && r.url.path == path).map((r) => jsonDecode(r.body) as Map<String, Object?>);

  testWidgets('setup picks a brain with a key, tests both, then shows the tabs', (tester) async {
    server.brainsChosen = false;
    tester.view.physicalSize = const Size(780, 1688);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(HeyrobotsApp(state: state));
    await tester.runAsync(() async {
      await state.start();
      await state.pair(PairingPayload(Uri.parse('http://192.168.1.2:7340'), 'ABCDE-FGHJK'), 'phone');
    });
    await settle(tester);
    expect(find.text('Pick my brain'), findsOneWidget);
    expect(find.text('Sign in in a terminal first'), findsOneWidget);

    // Claude Code isn't available, so tapping it picks nothing.
    await tester.tap(find.byKey(const Key('brain:claude_code')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('brain-key')), findsNothing);

    await tester.tap(find.byKey(const Key('brain:cloud')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('brain-url')), findsNothing);
    await tester.enterText(find.byKey(const Key('brain-key')), 'sk-test');
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('go')));
    await settle(tester);
    expect(bodies('PUT', '/api/v1/models/worker').single, {'provider': 'openai', 'model': 'deepseek-chat', 'base_url': 'https://api.deepseek.com', 'key': 'sk-test'});
    for (final role in ['router', 'reader']) {
      expect(bodies('PUT', '/api/v1/models/$role').single, {'provider': 'openai', 'model': 'qwen3', 'base_url': 'http://localhost:11434/v1'});
    }
    expect(find.byKey(const Key('main-nav')), findsOneWidget);
  });

  testWidgets('add-ons show the starter sections, filter, and connect an app', (tester) async {
    // On the computer running heyrobots.
    await show(tester, ConnectAppsScreen(state: state));
    expect(find.text('MAIL & CALENDAR'), findsOneWidget);
    expect(find.text('CODE'), findsOneWidget);
    expect(find.text('YOUR OWN'), findsOneWidget);
    expect(find.byKey(const Key('set-up-with-agent')), findsOneWidget);
    expect(find.text('Connected · 2'), findsOneWidget);
    // A connected Google app can add another account.
    expect(find.byKey(const Key('add-account:gmail')), findsOneWidget);

    await tester.tap(find.byKey(const Key('filter:connected')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('app:slack')), findsNothing);
    await tester.tap(find.byKey(const Key('filter:all')));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('app:slack')));
    await settle(tester);
    expect(server.requests.where((r) => r.method == 'POST' && r.url.path == '/api/v1/apps/slack/connect'), hasLength(1));
    expect(find.text('Connected · 3'), findsOneWidget);
  }, variant: TargetPlatformVariant.only(TargetPlatform.macOS));

  testWidgets('searching finds MCP registry servers; one that runs here shows its command and asks for its settings', (tester) async {
    await show(tester, ConnectAppsScreen(state: state));
    await tester.enterText(find.byKey(const Key('app-search')), 'no');
    await tester.pump(const Duration(milliseconds: 450));
    await settle(tester);
    expect(server.requests.any((r) => r.url.path == '/api/v1/apps' && r.url.queryParameters['q'] == 'no'), isTrue);
    expect(find.text("NOTION'S OWN"), findsOneWidget);
    expect(find.text('FROM THE MCP REGISTRY'), findsOneWidget);
    expect(find.text('io.github.acme'), findsOneWidget);
    expect(find.text('Runs on this computer'), findsOneWidget);

    await tester.tap(find.byKey(const Key('app:registry~io.github.acme~notes')));
    await tester.pumpAndSettle();
    expect(find.text('npx -y @acme/notes@2.0.0'), findsOneWidget);
    await tester.tap(find.byKey(const Key('confirm-run')));
    // The tile spins while the settings sheet is open, so pump frames.
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 600));
    await tester.enterText(find.byKey(const Key('setting:NOTES_TOKEN')), 'tok-1');
    await tester.tap(find.text('Connect'));
    await settle(tester);
    final sent = bodies('POST', '/api/v1/apps/registry~io.github.acme~notes/connect');
    expect(sent.last, {'settings': {'NOTES_TOKEN': 'tok-1'}});
  }, variant: TargetPlatformVariant.only(TargetPlatform.macOS));

  testWidgets('a phone lists apps but never starts a sign-in', (tester) async {
    await show(tester, ConnectAppsScreen(state: state));
    expect(find.text('Connected · 2'), findsOneWidget);
    expect(find.textContaining('from the computer running heyrobots'), findsWidgets);
    expect(find.byKey(const Key('add-account:gmail')), findsNothing);
    await tester.tap(find.byKey(const Key('app:slack')));
    await settle(tester);
    expect(server.requests.where((r) => r.url.path == '/api/v1/apps/slack/connect'), isEmpty);
  }, variant: TargetPlatformVariant.only(TargetPlatform.android));

  testWidgets('an app screen switches a tool and shows who uses it', (tester) async {
    await show(tester, AppScreen(state: state, appId: 'gmail'));
    expect(find.text('Gmail'), findsOneWidget);
    expect(find.text('On its own'), findsWidgets);
    expect(find.text('Asks first'), findsOneWidget);
    expect(find.text('General'), findsOneWidget);
    expect(find.text('Ask General to do something with it'), findsOneWidget);

    await tester.tap(find.byKey(const Key('tool:delete')));
    await settle(tester);
    expect(bodies('PUT', '/api/v1/apps/gmail/tools/delete').single, {'mode': 'own'});
    await tester.tap(find.byKey(const Key('tool:archive')));
    await settle(tester);
    expect(bodies('PUT', '/api/v1/apps/gmail/tools/archive').single, {'mode': 'off'});

    // Any other tool is set from its menu, such as a read that's off.
    await tester.tap(find.byKey(const Key('tool:send_email')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('mode:reads')));
    await settle(tester);
    expect(bodies('PUT', '/api/v1/apps/gmail/tools/send_email').single, {'mode': 'reads'});
  });

  testWidgets('skills adds one from the catalogue', (tester) async {
    await show(tester, SkillsScreen(state: state));
    expect(find.text('INSTALLED'), findsOneWidget);
    expect(find.text('Trip planner'), findsOneWidget);
    expect(find.text('anthropics/skills · for General'), findsOneWidget);

    await tester.tap(find.byKey(const Key('add:release-notes')));
    await settle(tester);
    expect(bodies('POST', '/api/v1/skills/add').single, {'source': 'anthropics/skills/release-notes'});
    expect(find.byKey(const Key('skill:release-notes')), findsOneWidget);
    expect(find.text('PICKED FOR YOUR TEAM'), findsNothing);
  });

  testWidgets('settings changes the accept mode', (tester) async {
    await show(tester, SettingsScreen(state: state));
    for (final row in ['Brain', 'Add-ons', 'Skills', 'People', 'Spending', 'Add a phone']) {
      expect(find.text(row), findsOneWidget);
    }
    expect(find.text('\$4.20 this month · stops at \$30'), findsOneWidget);
    expect(find.text('1 installed'), findsOneWidget);

    await tester.tap(find.text('Just do it'));
    await settle(tester);
    expect(bodies('PUT', '/api/v1/policy').single['mode'], 'auto');
    expect(server.policyMode, 'auto');
  });
}
