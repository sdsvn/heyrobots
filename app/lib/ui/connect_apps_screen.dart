import 'dart:async';

import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../api/session.dart';
import '../platform.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'app_screen.dart';
import 'chat_screen.dart';
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

/// The starter set, in sections for a team doing everyday work. Apps not
/// listed, such as servers added by link, show under "Your own".
const _sections = [
  ('Mail & calendar', ['gmail', 'google_calendar', 'apple_calendar', 'imessage']),
  ('Tasks & projects', ['linear', 'atlassian', 'monday', 'todoist']),
  ('Notes & data', ['notion', 'airtable']),
  ('Code', ['github', 'supabase', 'context7', 'claude_code']),
  ('Design', ['figma', 'canva']),
  ('Money', ['stripe']),
  ('Research & browser', ['exa', 'playwright']),
  ('Everything else', ['zapier']),
];

/// The IT agent, who sets up the team.
const itAgent = AgentRef(id: 'ag_it', name: 'IT', color: 'sky', badge: 'bolt');

class _ConnectAppsScreenState extends State<ConnectAppsScreen> {
  late Future<AppList> _apps = widget.state.api.listApps();
  final _search = TextEditingController();
  Timer? _typing;

  /// 'all' or 'connected'.
  String _filter = 'all';
  String? _busy;

  /// How many are connected, from the last list without a search.
  int? _connected;

  @override
  void dispose() {
    _typing?.cancel();
    _search.dispose();
    super.dispose();
  }

  void _reload() => setState(() {
    final q = _search.text.trim();
    _apps = widget.state.api.listApps(q: q.length >= 2 && !_isLink(q) ? q : null);
  });

  /// Searches the server, and through it the MCP registry, once typing stops.
  void _onSearch(String _) {
    setState(() {});
    _typing?.cancel();
    _typing = Timer(const Duration(milliseconds: 400), _reload);
  }

  static bool _isLink(String s) => s.startsWith('http://') || s.startsWith('https://');

  Future<void> _connect(App a, {ConnectRequest? body}) async {
    if (a.runsHere == true && !a.connected && !await _confirmRunsHere(a)) return;
    if (!mounted) return;
    setState(() => _busy = a.id);
    try {
      await connectApp(context, widget.state, a.id, body: body);
    } finally {
      if (mounted) setState(() => _busy = null);
      _reload();
    }
  }

  /// A server that runs on this computer runs code as you: show what runs
  /// before it starts.
  Future<bool> _confirmRunsHere(App a) async {
    final p = Palette.of(context);
    final ok = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  AppLogo(app: a, size: 44, radius: 14, fontSize: 16, dot: false),
                  const SizedBox(width: 12),
                  Expanded(child: TwoLines(a.name, a.publisher ?? a.summary, titleSize: 20, titleWeight: FontWeight.w900, subtitleSize: 13)),
                ],
              ),
              const SizedBox(height: 14),
              Container(
                padding: const EdgeInsets.all(14),
                decoration: BoxDecoration(color: p.peachSoft, borderRadius: BorderRadius.circular(16)),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Runs on this computer',
                      style: TextStyle(fontSize: 14, fontWeight: FontWeight.w900, color: p.peachInk),
                    ),
                    const SizedBox(height: 4),
                    Text('It starts as a program on the computer running heyrobots, as you. This is what runs:', style: TextStyle(fontSize: 13, color: p.ink)),
                    const SizedBox(height: 8),
                    Container(
                      width: double.infinity,
                      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
                      decoration: BoxDecoration(color: p.card, borderRadius: BorderRadius.circular(10)),
                      child: SelectableText(
                        a.command ?? '',
                        key: const Key('run-command'),
                        style: TextStyle(fontFamily: 'monospace', fontSize: 13, color: p.ink),
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 12),
              Text('Read-only tools work on their own; the rest ask you first.', style: TextStyle(fontSize: 13, color: p.muted)),
              const SizedBox(height: 16),
              BigButton(key: const Key('confirm-run'), label: 'Connect', onPressed: () => Navigator.pop(context, true)),
            ],
          ),
        ),
      ),
    );
    return ok == true;
  }

  Future<void> _open(App a) async {
    if (!a.connected) return _connect(a);
    await Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => AppScreen(state: widget.state, appId: a.id),
      ),
    );
    _reload();
  }

  void _askIT() {
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => ChatScreen(state: widget.state, agent: itAgent),
      ),
    );
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
            final ours = all.where((a) => !a.id.startsWith('registry~')).where((a) {
              if (q.isNotEmpty && !_isLink(q) && !a.name.toLowerCase().contains(q)) return false;
              return _filter == 'all' || a.connected;
            }).toList();
            final found = _filter == 'all' && q.isNotEmpty ? all.where((a) => a.id.startsWith('registry~')).toList() : const <App>[];
            final official = found.where((a) => a.official == true).toList();
            final others = found.where((a) => a.official != true).toList();
            final placed = {for (final (_, ids) in _sections) ...ids};
            final own = ours.where((a) => !placed.contains(a.id)).toList();
            if (snap.hasData && q.isEmpty) _connected = all.where((a) => a.connected).length;
            final connected = _connected ?? all.where((a) => a.connected).length;
            final onTile = isPhoneApp ? null : (App a) => _connect(a);
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const ScreenHeader('Add-ons', padding: EdgeInsets.fromLTRB(20, 20, 20, 8)),
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 10),
                  child: SearchBox(
                    fieldKey: const Key('app-search'),
                    hint: isPhoneApp ? 'Search any app or MCP server' : 'Search any app or MCP server, or paste a link',
                    controller: _search,
                    onChanged: _onSearch,
                    onSubmitted: (v) {
                      if (_isLink(v.trim())) {
                        _search.clear();
                        _connect(
                          App(
                            id: 'custom',
                            name: 'Custom',
                            category: AppCategory.other,
                            kind: AppKind.mcp,
                            mark: '',
                            tint: AppTint.ink,
                            connected: false,
                            accounts: const [],
                            status: AppStatus.off,
                            summary: '',
                            newTools: 0,
                            agents: const [],
                            toolset: '',
                          ),
                          body: ConnectRequest(url: v.trim()),
                        );
                      }
                    },
                  ),
                ),
                if (isPhoneApp)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(20, 0, 20, 10),
                    child: Text(
                      onComputerOnly,
                      key: const Key('apps-hint'),
                      style: TextStyle(fontSize: 13, color: p.muted),
                    ),
                  ),
                SizedBox(
                  height: 36,
                  child: ListView(
                    scrollDirection: Axis.horizontal,
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    children: [
                      for (final (id, label) in [('all', 'All'), ('connected', 'Connected · $connected')]) ...[
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
                            if (q.isEmpty) ...[_SetUpBanner(onTap: _askIT), const SizedBox(height: 14)],
                            if (official.isNotEmpty) ...[
                              CapsLabel("${official.first.name}'s own", padding: const EdgeInsets.fromLTRB(6, 4, 6, 0)),
                              const SizedBox(height: 10),
                              _Grid(apps: official, busy: _busy, onTap: _open, onAdd: onTile, wide: true),
                              const SizedBox(height: 10),
                            ],
                            for (final (title, ids) in _sections)
                              if ([for (final id in ids) ...ours.where((a) => a.id == id)] case final apps when apps.isNotEmpty) ...[
                                CapsLabel(title, padding: const EdgeInsets.fromLTRB(6, 4, 6, 0)),
                                const SizedBox(height: 10),
                                _Grid(apps: apps, busy: _busy, onTap: _open, onAdd: onTile),
                                const SizedBox(height: 10),
                              ],
                            if (own.isNotEmpty) ...[
                              CapsLabel('Your own', padding: const EdgeInsets.fromLTRB(6, 4, 6, 0)),
                              const SizedBox(height: 10),
                              _Grid(apps: own, busy: _busy, onTap: _open, onAdd: onTile),
                              const SizedBox(height: 10),
                            ],
                            if (others.isNotEmpty) ...[
                              CapsLabel('From the MCP registry', padding: const EdgeInsets.fromLTRB(6, 4, 6, 0)),
                              const SizedBox(height: 10),
                              _Grid(apps: others, busy: _busy, onTap: _open, onAdd: onTile, wide: true),
                              Padding(
                                padding: const EdgeInsets.fromLTRB(6, 8, 6, 10),
                                child: Text('Each shows who publishes it. Pick the one you trust.', style: TextStyle(fontSize: 12, color: p.muted)),
                              ),
                            ],
                            if (q.length >= 2 && found.isEmpty && ours.isEmpty)
                              Padding(
                                padding: const EdgeInsets.all(20),
                                child: Text(
                                  'Nothing called "$q" yet. Paste its MCP link instead.',
                                  textAlign: TextAlign.center,
                                  style: TextStyle(color: p.muted),
                                ),
                              ),
                          ],
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

/// "Set up with an agent": opens a chat with IT, who connects things for you.
class _SetUpBanner extends StatelessWidget {
  const _SetUpBanner({required this.onTap});

  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return EdgedBox(
      key: const Key('set-up-with-agent'),
      radius: 20,
      depth: 3,
      color: p.accentSoft,
      edge: p.accent,
      padding: const EdgeInsets.fromLTRB(12, 12, 14, 12),
      onTap: onTap,
      child: Row(
        children: [
          const AgentAvatar(color: 'sky', badge: 'bolt', size: 44, name: 'IT', seed: 'ag_it'),
          const SizedBox(width: 12),
          const Expanded(
            child: TwoLines(
              'Set up with an agent',
              'Tell IT what you use; it finds it, connects it and gives it to the right agent.',
              titleSize: 15,
              titleWeight: FontWeight.w900,
              subtitleSize: 12,
            ),
          ),
          LineIcon(Glyph.chevron, size: 18, color: p.accent, stroke: 2.6),
        ],
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
  if (r.needsSettings case final need? when need.isNotEmpty) {
    final v = await askInSheet(
      context,
      title: '${r.app.name} needs a few things',
      body: 'They go to the vault on your server and start it on this computer.',
      fields: [
        for (final n in need)
          (label: n.description.isEmpty ? n.name : '${n.name} · ${n.description}', secret: n.secret, initial: null, key: Key('setting:${n.name}')),
      ],
      action: 'Connect',
    );
    if (v == null) return;
    return again(ConnectRequest(url: body?.url, token: body?.token, settings: {for (final (i, n) in need.indexed) n.name: v[i]}));
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
  const _Grid({required this.apps, required this.busy, required this.onTap, this.onAdd, this.wide = false});

  final List<App> apps;

  /// One app per row, with room for its publisher and what it does.
  final bool wide;
  final String? busy;
  final void Function(App) onTap;

  /// Adds another account; null where apps can't be connected from here.
  final void Function(App)? onAdd;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    if (wide) {
      return Container(
        decoration: edged(p.card, edge: p.line),
        padding: const EdgeInsets.all(6),
        child: Column(
          children: [for (final a in apps) _Tile(app: a, busy: busy == a.id, onTap: onTap, onAdd: onAdd, wide: true)],
        ),
      );
    }
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
  const _Tile({required this.app, required this.busy, required this.onTap, this.onAdd, this.wide = false});

  final App app;
  final bool wide;
  final bool busy;
  final void Function(App) onTap;

  /// Adds another account; null where apps can't be connected from here.
  final void Function(App)? onAdd;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final review = app.newTools > 0;
    final sub = review ? '${app.newTools} new tool${app.newTools == 1 ? '' : 's'} to review' : app.summary;
    final chips = [if (app.official == true) ('Official', 'mint'), if (app.runsHere == true) ('Runs on this computer', 'peach')];
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
                      if (wide && app.publisher != null)
                        Text(
                          app.publisher!,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: p.accent),
                        ),
                      if (sub.isNotEmpty)
                        Text(
                          sub,
                          maxLines: wide ? 2 : 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(fontSize: 12, color: review ? p.warn : p.muted),
                        ),
                      if (wide && chips.isNotEmpty) ...[
                        const SizedBox(height: 4),
                        Wrap(
                          spacing: 6,
                          children: [
                            for (final (label, tint) in chips)
                              Pill(label, background: tintColors(tint, p).$1, foreground: tint == 'peach' ? p.peachInk : tintColors(tint, p).$2),
                          ],
                        ),
                      ],
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
