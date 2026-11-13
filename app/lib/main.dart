import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'pairing.dart';
import 'platform.dart';
import 'local_credentials.dart' if (dart.library.js_interop) 'local_credentials_web.dart';
import 'state.dart';
import 'ui/approval_screen.dart';
import 'ui/bit.dart';
import 'ui/chats_tab.dart';
import 'ui/common.dart';
import 'ui/digest_screen.dart';
import 'ui/icons.dart';
import 'ui/pair_screen.dart';
import 'ui/plans_tab.dart';
import 'ui/setup_screen.dart';
import 'ui/team_tab.dart';
import 'ui/theme.dart';
import 'ui/thread_screen.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  // The bundled Nunito font is under the SIL Open Font License.
  LicenseRegistry.addLicense(() async* {
    yield LicenseEntryWithLineBreaks(['Nunito'], await rootBundle.loadString('assets/fonts/OFL.txt'));
  });
  final state = AppState(
    credentials: localCredentialStore() ?? SecureCredentialStore(),
    verifier: BiometricVerifier(),
  );
  // Push notifications open heyrobots:// links.
  final links = kIsWeb ? null : AppLinks().uriLinkStream;
  runApp(HeyrobotsApp(state: state, links: links));
  unawaited(state.start().then((_) => signInFromPage(state)));
}

/// In a browser on the server's own machine, `/?pair=CODE` signs in
/// without asking: the installer opens it on the first start.
Future<void> signInFromPage(AppState state) async {
  final code = kIsWeb ? Uri.base.queryParameters['pair'] : null;
  if (code == null || code.isEmpty || state.paired || !isLoopbackHost(Uri.base.host)) return;
  try {
    await state.pair(PairingPayload(Uri.parse(Uri.base.origin), code), 'Browser on this machine');
  } catch (e) {
    state.signInFailed(e);
  }
}

class HeyrobotsApp extends StatefulWidget {
  const HeyrobotsApp({super.key, required this.state, this.links});

  final AppState state;
  final Stream<Uri>? links;

  @override
  State<HeyrobotsApp> createState() => _HeyrobotsAppState();
}

class _HeyrobotsAppState extends State<HeyrobotsApp> {
  final _navigator = GlobalKey<NavigatorState>();
  StreamSubscription<Uri>? _links;
  Uri? _pendingLink;
  Uri? _pendingSignIn;

  @override
  void initState() {
    super.initState();
    _links = widget.links?.listen(_openLink);
    widget.state.addListener(_onState);
  }

  @override
  void dispose() {
    _links?.cancel();
    widget.state.removeListener(_onState);
    super.dispose();
  }

  /// Signs in from the installer's link, once, when this device isn't
  /// paired yet and the link names a server on this machine.
  Future<void> _signIn(Uri uri) async {
    final p = localPairingLink(uri);
    final s = widget.state;
    if (p == null || s.paired) return;
    if (s.loading) {
      _pendingSignIn = uri;
      return;
    }
    try {
      await s.pair(p, 'This ${platformName() == 'macOS' ? 'Mac' : 'computer'}');
    } catch (e) {
      // Such as an expired code, or a Keychain that refused the token.
      s.signInFailed(e);
    }
  }

  void _onState() {
    final signIn = _pendingSignIn;
    if (signIn != null && !widget.state.loading) {
      _pendingSignIn = null;
      _signIn(signIn);
    }
    final link = _pendingLink;
    if (link != null && widget.state.paired && !widget.state.loading) {
      _pendingLink = null;
      _openLink(link);
    }
  }

  /// Opens `heyrobots://thread/ID`, `heyrobots://approval/ID`,
  /// `heyrobots://digest` or `heyrobots://home`.
  void _openLink(Uri uri) {
    if (uri.scheme != 'heyrobots') return;
    if (uri.host == 'pair') {
      unawaited(_signIn(uri));
      return;
    }
    final nav = _navigator.currentState;
    if (nav == null || !widget.state.paired || widget.state.loading) {
      _pendingLink = uri;
      return;
    }
    final id = uri.pathSegments.isNotEmpty ? uri.pathSegments.first : '';
    nav.popUntil((r) => r.isFirst);
    switch (uri.host) {
      case 'thread' when id.isNotEmpty:
        nav.push(MaterialPageRoute<void>(builder: (_) => ThreadScreen(state: widget.state, threadId: id)));
      case 'approval' when id.isNotEmpty:
        nav.push(MaterialPageRoute<void>(builder: (_) => ApprovalScreen(state: widget.state, approvalId: id)));
      case 'digest':
        nav.push(MaterialPageRoute<void>(builder: (_) => DigestScreen(state: widget.state)));
    }
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'heyrobots',
      navigatorKey: _navigator,
      theme: buildTheme(Brightness.light),
      darkTheme: buildTheme(Brightness.dark),
      home: ListenableBuilder(
        listenable: widget.state,
        builder: (context, _) {
          final s = widget.state;
          if (s.loading) {
            return const Scaffold(body: Center(child: Bit(mood: BitMood.thinking, size: 96)));
          }
          return s.paired ? Paired(state: s) : PairScreen(state: s);
        },
      ),
    );
  }
}

/// After pairing: the brain setup once, then the three tabs.
class Paired extends StatefulWidget {
  const Paired({super.key, required this.state});

  final AppState state;

  @override
  State<Paired> createState() => _PairedState();
}

class _PairedState extends State<Paired> {
  /// Whether both brains are chosen; null while asking. Only the owner
  /// picks them, so other devices go straight to the tabs.
  bool? _chosen;

  @override
  void initState() {
    super.initState();
    if (!widget.state.isOwner) {
      _chosen = true;
      return;
    }
    widget.state.api.listBrains().then(
      (b) => mounted ? setState(() => _chosen = b.chosen) : null,
      // Asking failed, perhaps an older server: don't block the app on it.
      onError: (_) => mounted ? setState(() => _chosen = true) : null,
    );
  }

  @override
  Widget build(BuildContext context) {
    return switch (_chosen) {
      null => const Scaffold(body: Center(child: Bit(mood: BitMood.thinking, size: 96))),
      false => SetupScreen(state: widget.state, onDone: () => setState(() => _chosen = true)),
      true => MainTabs(state: widget.state),
    };
  }
}

/// Chats, Team and Plans, with the bottom navigation from the design.
class MainTabs extends StatelessWidget {
  const MainTabs({super.key, required this.state});

  final AppState state;

  @override
  Widget build(BuildContext context) {
    return ValueListenableBuilder<int>(
      valueListenable: homeTab,
      builder: (context, tab, _) => Scaffold(
        body: SafeArea(
          bottom: false,
          child: IndexedStack(
            index: tab,
            children: [ChatsTab(state: state), TeamTab(state: state), PlansTab(state: state)],
          ),
        ),
        bottomNavigationBar: ListenableBuilder(
          listenable: state,
          builder: (context, _) {
            final p = Palette.of(context);
            final unread = state.threads.fold<int>(0, (n, t) => n + t.unread);
            return Container(
              key: const Key('main-nav'),
              decoration: BoxDecoration(
                color: p.card,
                border: Border(top: BorderSide(color: p.ground.computeLuminance() < .2 ? p.line : const Color(0xFFEEE9FC))),
              ),
              padding: EdgeInsets.fromLTRB(8, 8, 8, MediaQuery.paddingOf(context).bottom.clamp(24, 48).toDouble()),
              child: Row(
                children: [
                  for (final (i, (label, glyph, badge, badgeColor)) in [
                    ('Chats', Glyph.chat, unread, p.online),
                    ('Team', Glyph.team, 0, p.online),
                    ('Plans', Glyph.plans, state.attention.length, const Color(0xFFE07A45)),
                  ].indexed)
                    Expanded(
                      child: _NavItem(
                        label: label,
                        glyph: glyph,
                        badge: badge,
                        badgeColor: badgeColor,
                        active: tab == i,
                        onTap: () => homeTab.value = i,
                      ),
                    ),
                ],
              ),
            );
          },
        ),
      ),
    );
  }
}

class _NavItem extends StatelessWidget {
  const _NavItem({required this.label, required this.glyph, required this.badge, required this.badgeColor, required this.active, required this.onTap});

  final String label;
  final Glyph glyph;
  final int badge;
  final Color badgeColor;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final color = active ? p.accent : p.muted;
    return Semantics(
      selected: active,
      button: true,
      label: badge > 0 ? '$label, $badge new' : label,
      excludeSemantics: true,
      child: InkResponse(
        key: Key('tab:$label'),
        onTap: onTap,
        radius: 36,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
              width: 60,
              height: 32,
              decoration: BoxDecoration(color: active ? p.accentSoft : null, borderRadius: BorderRadius.circular(999)),
              child: Stack(
                clipBehavior: Clip.none,
                alignment: Alignment.center,
                children: [
                  LineIcon(glyph, size: 22, color: color),
                  if (badge > 0)
                    Positioned(
                      top: -2,
                      right: 6,
                      child: Container(
                        constraints: const BoxConstraints(minWidth: 18),
                        height: 18,
                        padding: const EdgeInsets.symmetric(horizontal: 4),
                        alignment: Alignment.center,
                        decoration: BoxDecoration(color: badgeColor, borderRadius: BorderRadius.circular(999)),
                        child: Text('$badge', style: const TextStyle(color: Colors.white, fontSize: 11, fontWeight: FontWeight.w900, height: 1)),
                      ),
                    ),
                ],
              ),
            ),
            const SizedBox(height: 2),
            Text(label, style: TextStyle(fontSize: 12, fontWeight: active ? FontWeight.w900 : FontWeight.w800, color: color)),
          ],
        ),
      ),
    );
  }
}
