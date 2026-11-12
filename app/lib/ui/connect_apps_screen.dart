import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../api/session.dart';
import '../platform.dart';
import '../state.dart';
import 'app_screen.dart';
import 'common.dart';
import 'icons.dart';
import 'theme.dart';

/// "Connect apps": the catalogue of apps and MCP servers, by category.
class ConnectAppsScreen extends StatefulWidget {
  const ConnectAppsScreen({super.key, required this.state});

  final AppState state;

  @override
  State<ConnectAppsScreen> createState() => _ConnectAppsScreenState();
}

const _sections = [('email', 'Email & calendar'), ('messaging', 'Messaging'), ('code', 'Code & work'), ('docs', 'Docs'), ('other', 'More')];

class _ConnectAppsScreenState extends State<ConnectAppsScreen> {
  late Future<AppList> _apps = widget.state.api.listApps();
  final _search = TextEditingController();

  /// 'all', 'connected', or a category.
  String _filter = 'all';
  String? _busy;

  void _reload() => setState(() {
    _apps = widget.state.api.listApps();
  });

  static bool _isLink(String s) => s.startsWith('http://') || s.startsWith('https://');

  Future<void> _connect(String id, {ConnectRequest? body}) async {
    setState(() => _busy = id);
    try {
      await connectApp(context, widget.state, id, body: body);
    } finally {
      if (mounted) setState(() => _busy = null);
      _reload();
    }
  }

  Future<void> _open(App a) async {
    if (!a.connected) return _connect(a.id);
    await Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => AppScreen(state: widget.state, appId: a.id),
      ),
    );
    _reload();
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: FutureBuilder<AppList>(
          future: _apps,
          builder: (context, snap) {
            final all = snap.data?.apps ?? const <App>[];
            final q = _search.text.trim().toLowerCase();
            final shown = all.where((a) {
              if (q.isNotEmpty && !_isLink(q) && !a.name.toLowerCase().contains(q)) return false;
              return switch (_filter) {
                'all' => true,
                'connected' => a.connected,
                final c => a.category.toJson() == c,
              };
            }).toList();
            final connected = all.where((a) => a.connected).length;
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const ScreenHeader('Connect apps', padding: EdgeInsets.fromLTRB(20, 20, 20, 8)),
                Padding(
                  padding: const EdgeInsets.fromLTRB(20, 0, 20, 12),
                  child: Text(
                    isPhoneApp ? onComputerOnly : "Tap one and I'll ask for what I need. Apps on this Mac just work.",
                    key: const Key('apps-hint'),
                    style: TextStyle(fontSize: 14, color: p.muted),
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 10),
                  child: SearchBox(
                    fieldKey: const Key('app-search'),
                    hint: isPhoneApp ? 'Search apps' : 'Search apps, or paste an MCP link',
                    controller: _search,
                    onChanged: (_) => setState(() {}),
                    onSubmitted: (v) {
                      if (_isLink(v.trim())) {
                        _search.clear();
                        _connect('custom', body: ConnectRequest(url: v.trim()));
                      }
                    },
                  ),
                ),
                SizedBox(
                  height: 36,
                  child: ListView(
                    scrollDirection: Axis.horizontal,
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    children: [
                      for (final (id, label) in [
                        ('all', 'All'),
                        ('connected', 'Connected · $connected'),
                        ('email', 'Email'),
                        ('messaging', 'Messaging'),
                        ('code', 'Code'),
                        ('docs', 'Docs'),
                      ]) ...[
                        Align(
                          alignment: Alignment.topCenter,
                          child: PillChip(label, key: Key('filter:$id'), selected: _filter == id, onTap: () => setState(() => _filter = id)),
                        ),
                        const SizedBox(width: 8),
                      ],
                    ],
                  ),
                ),
                const SizedBox(height: 10),
                Expanded(
                  child: snap.hasError
                      ? ErrorPanel(message: describeError(snap.error!), onRetry: _reload)
                      : !snap.hasData
                      ? const Center(child: CircularProgressIndicator())
                      : ListView(
                          padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                          children: [
                            for (final (cat, title) in _sections)
                              if (shown.where((a) => a.category.toJson() == cat) case final apps when apps.isNotEmpty) ...[
                                CapsLabel(title, padding: const EdgeInsets.fromLTRB(6, 4, 6, 0)),
                                const SizedBox(height: 10),
                                _Grid(apps: apps.toList(), busy: _busy, onTap: _open, onAdd: isPhoneApp ? null : (a) => _connect(a.id)),
                                const SizedBox(height: 10),
                              ],
                          ],
                        ),
                ),
                Padding(
                  padding: const EdgeInsets.fromLTRB(20, 10, 20, 28),
                  child: Text(
                    '${all.length} apps · any MCP server works too',
                    textAlign: TextAlign.center,
                    style: TextStyle(fontSize: 13, color: p.muted),
                  ),
                ),
              ],
            );
          },
        ),
      ),
    );
  }
}

/// Connects an app, walking the person through whatever the server asks
/// for: a sign-in page, a token, Google's own client, or steps on the Mac.
/// What a phone says instead of connecting: sign-ins come back to the
/// computer running heyrobots, which a phone can never be.
const onComputerOnly = 'Add apps from the computer running heyrobots. Their sign-in comes back to that computer, so a phone can\'t finish it.';

Future<void> connectApp(BuildContext context, AppState state, String appId, {ConnectRequest? body, int depth = 0}) async {
  if (isPhoneApp) {
    showMessage(context, onComputerOnly);
    return;
  }
  ConnectResult r;
  try {
    r = await state.api.connectApp(appId: appId, body: body);
  } catch (e) {
    if (context.mounted) showMessage(context, describeError(e));
    return;
  }
  if (!context.mounted || depth > 3) return;
  Future<void> again([ConnectRequest? b]) => connectApp(context, state, appId, body: b ?? body, depth: depth + 1);

  if (r.needsClient == true) {
    final v = await askInSheet(
      context,
      title: 'Google needs a key of your own (3 minutes)',
      steps: (r.steps ?? const []).isNotEmpty
          ? r.steps!
          : const [
              'Open the Google Cloud console and make a new project.',
              'Set up the OAuth consent screen.',
              'Create an OAuth client of type Desktop app.',
              'Set the app to In production.',
              'Paste its client ID and secret here.',
            ],
      fields: [
        (label: 'Client ID', secret: false, initial: null, key: const Key('google-id')),
        (label: 'Client secret', secret: true, initial: null, key: const Key('google-secret')),
      ],
    );
    if (v == null || v[0].isEmpty || !context.mounted) return;
    try {
      await state.api.setGoogleClient(
        body: GoogleClient(clientId: v[0], clientSecret: v[1]),
      );
    } catch (e) {
      if (context.mounted) showMessage(context, describeError(e));
      return;
    }
    return again();
  }
  if (r.needsToken == true) {
    final v = await askInSheet(
      context,
      title: '${r.app.name} needs a token',
      body: 'Paste the token from ${r.app.name}. It goes to the vault on your server.',
      fields: [(label: 'Token', secret: true, initial: null, key: const Key('app-token'))],
      action: 'Connect',
    );
    if (v == null || v[0].isEmpty) return;
    return again(ConnectRequest(url: body?.url, token: v[0]));
  }
  if ((r.steps ?? const []).isNotEmpty && !r.app.connected) {
    final v = await askInSheet(context, title: 'One thing on your Mac first', steps: r.steps!, fields: const [], action: "I've done that");
    if (v == null) return;
    return again();
  }
  final url = r.openUrl;
  if (url != null && url.isNotEmpty) {
    var opened = false;
    try {
      opened = await launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication);
    } catch (_) {
      // No browser here, as in tests; say where to go instead.
    }
    if (!opened && context.mounted) showMessage(context, 'Open $url to sign in.');
    return;
  }
  if (r.app.connected) showMessage(context, '${r.app.name} is connected.');
}

class _Grid extends StatelessWidget {
  const _Grid({required this.apps, required this.busy, required this.onTap, this.onAdd});

  final List<App> apps;
  final String? busy;
  final void Function(App) onTap;

  /// Adds another account; null where apps can't be connected from here.
  final void Function(App)? onAdd;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Container(
      decoration: edged(p.card, edge: p.line),
      padding: const EdgeInsets.all(6),
      child: Column(
        children: [
          for (var i = 0; i < apps.length; i += 2) ...[
            if (i > 0) const SizedBox(height: 2),
            Row(
              children: [
                Expanded(
                  child: _Tile(app: apps[i], busy: busy == apps[i].id, onTap: onTap, onAdd: onAdd),
                ),
                const SizedBox(width: 2),
                Expanded(
                  child: i + 1 < apps.length ? _Tile(app: apps[i + 1], busy: busy == apps[i + 1].id, onTap: onTap, onAdd: onAdd) : const SizedBox(),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}

/// An app's logo tile: its mark in its tint, with a green dot when connected.
class AppLogo extends StatelessWidget {
  const AppLogo({super.key, required this.app, this.size = 34, this.radius = 11, this.fontSize = 13, this.dot = true});

  final App app;
  final double size, radius, fontSize;
  final bool dot;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final (bg, ink) = tintColors(app.tint.toJson(), p);
    return Stack(
      clipBehavior: Clip.none,
      children: [
        Container(
          width: size,
          height: size,
          alignment: Alignment.center,
          decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(radius)),
          child: Text(
            app.mark,
            style: TextStyle(color: ink, fontWeight: FontWeight.w900, fontSize: fontSize),
          ),
        ),
        if (dot && app.connected)
          Positioned(
            right: -3,
            bottom: -3,
            child: Container(
              width: 12,
              height: 12,
              decoration: BoxDecoration(
                color: p.online,
                shape: BoxShape.circle,
                border: Border.all(color: p.card, width: 2),
              ),
            ),
          ),
      ],
    );
  }
}

class _Tile extends StatelessWidget {
  const _Tile({required this.app, required this.busy, required this.onTap, this.onAdd});

  final App app;
  final bool busy;
  final void Function(App) onTap;

  /// Adds another account; null where apps can't be connected from here.
  final void Function(App)? onAdd;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final review = app.newTools > 0;
    final sub = review ? '${app.newTools} new tool${app.newTools == 1 ? '' : 's'} to review' : app.summary;
    return Material(
      type: MaterialType.transparency,
      child: InkWell(
        key: Key('app:${app.id}'),
        borderRadius: BorderRadius.circular(16),
        hoverColor: p.ground,
        onTap: busy ? null : () => onTap(app),
        child: ConstrainedBox(
          constraints: const BoxConstraints(minHeight: 54),
          child: Padding(
            padding: const EdgeInsets.all(10),
            child: Row(
              children: [
                AppLogo(app: app),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        app.name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w900),
                      ),
                      if (sub.isNotEmpty)
                        Text(
                          sub,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(fontSize: 12, color: review ? p.warn : p.muted),
                        ),
                    ],
                  ),
                ),
                if (busy)
                  const SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2.4))
                else if (app.connected && app.kind.toJson() == 'google' && onAdd != null) ...[
                  const SizedBox(width: 10),
                  Semantics(
                    button: true,
                    label: 'Add another ${app.name} account',
                    child: InkWell(
                      key: Key('add-account:${app.id}'),
                      borderRadius: BorderRadius.circular(9),
                      onTap: () => onAdd!(app),
                      child: Container(
                        width: 24,
                        height: 24,
                        alignment: Alignment.center,
                        decoration: BoxDecoration(color: p.track, borderRadius: BorderRadius.circular(9)),
                        child: LineIcon(Glyph.plus, size: 14, color: p.accent, stroke: 3),
                      ),
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
