import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'chat_screen.dart';
import 'common.dart';
import 'connect_apps_screen.dart';
import 'icons.dart';
import 'theme.dart';

/// One connected app: what it can do, who uses it, what it did lately.
class AppScreen extends StatefulWidget {
  const AppScreen({super.key, required this.state, required this.appId});

  final AppState state;
  final String appId;

  @override
  State<AppScreen> createState() => _AppScreenState();
}

class _AppScreenState extends State<AppScreen> {
  late Future<AppDetail> _detail = widget.state.api.getApp(appId: widget.appId);

  /// Tools switched here, before the server answers.
  final _modes = <String, AppToolMode>{};

  void _reload() => setState(() {
    _detail = widget.state.api.getApp(appId: widget.appId);
  });

  /// The toolset an agent needs to use this app.
  static String _toolset(App a) => a.toolset;

  Future<void> _setTool(AppTool t, bool on) => _setMode(t, on ? AppToolMode.own : AppToolMode.off);

  Future<void> _setMode(AppTool t, AppToolMode mode) async {
    setState(() => _modes[t.name] = mode);
    try {
      await widget.state.api.setAppTool(
        appId: widget.appId,
        toolName: t.name,
        body: AppToolUpdate(mode: AppToolUpdateMode.fromJson(mode.toJson())),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _modes.remove(t.name));
      showMessage(context, describeError(e));
    }
  }

  Future<void> _disconnect(App a) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Disconnect ${a.name}?'),
        content: const Text('Your agents stop using it. You can connect it again any time.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          TextButton(onPressed: () => Navigator.pop(context, true), child: const Text('Disconnect')),
        ],
      ),
    );
    if (ok != true) return;
    try {
      await widget.state.api.disconnectApp(appId: a.id);
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    }
  }

  Future<void> _giveTo(App a) async {
    final List<Agent> agents;
    try {
      agents = (await widget.state.api.listAgents()).agents;
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
      return;
    }
    final toolset = _toolset(a);
    final others = agents.where((g) => !g.toolsets.contains(toolset) && !a.agents.any((r) => r.id == g.id)).toList();
    if (!mounted) return;
    final p = Palette.of(context);
    final pick = await showModalBottomSheet<Agent>(
      context: context,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 0, 20, 8),
              child: Text('Give ${a.name} to…', style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w900)),
            ),
            if (others.isEmpty)
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
                child: Text('Everyone on the team has it.', style: TextStyle(color: p.muted)),
              ),
            for (final g in others)
              ListTile(
                key: Key('give:${g.id}'),
                leading: AgentAvatar(color: g.color.toJson(), badge: g.badge.toJson(), size: 36),
                title: Text(g.name),
                subtitle: g.purpose.isEmpty ? null : Text(g.purpose, maxLines: 1, overflow: TextOverflow.ellipsis),
                onTap: () => Navigator.pop(context, g),
              ),
            const SizedBox(height: 12),
          ],
        ),
      ),
    );
    if (pick == null) return;
    try {
      await widget.state.api.updateAgent(
        agentId: pick.id,
        body: AgentRequest(
          name: pick.name,
          purpose: pick.purpose,
          instructions: pick.instructions,
          toolsets: [...pick.toolsets, toolset],
          anyToolset: pick.anyToolset,
          skills: pick.skills,
          provider: pick.provider == null ? null : AgentRequestProvider.fromJson(pick.provider),
          model: pick.model,
          baseUrl: pick.baseUrl,
          capacity: pick.capacity,
          color: AgentRequestColor.fromJson(pick.color.toJson()),
          badge: AgentRequestBadge.fromJson(pick.badge.toJson()),
          enabled: pick.enabled,
          delegates: pick.delegates,
          rules: pick.rules,
        ),
      );
      _reload();
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    }
  }

  /// Opens the chat with the agent that uses it, or with Bit.
  void _ask(App a) {
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => ChatScreen(state: widget.state, agent: a.agents.firstOrNull, draft: 'With ${a.name}, '),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: FutureBuilder<AppDetail>(
          future: _detail,
          builder: (context, snap) {
            if (snap.hasError) {
              return Column(
                children: [
                  const ScreenHeader(null),
                  Expanded(
                    child: ErrorPanel(message: describeError(snap.error!), onRetry: _reload),
                  ),
                ],
              );
            }
            if (!snap.hasData) return const Center(child: CircularProgressIndicator());
            final d = snap.data!;
            final a = d.app;
            final who = a.agents.isEmpty ? 'Bit' : a.agents.first.name;
            final sub = [if (a.accounts.isNotEmpty) a.accounts.join(', ') else if (a.summary.isNotEmpty) a.summary];
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                ScreenHeader(
                  null,
                  trailing: EdgedBox(
                    key: const Key('disconnect'),
                    height: 44,
                    radius: 16,
                    depth: 2,
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    onTap: () => _disconnect(a),
                    child: Center(
                      widthFactor: 1,
                      child: Text(
                        'Disconnect',
                        style: TextStyle(fontSize: 14, fontWeight: FontWeight.w900, color: p.danger),
                      ),
                    ),
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
                  child: Row(
                    children: [
                      AppLogo(app: a, size: 64, radius: 20, fontSize: 24, dot: false),
                      const SizedBox(width: 14),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(a.name, style: const TextStyle(fontSize: 24, fontWeight: FontWeight.w900, letterSpacing: -0.3)),
                            const SizedBox(height: 2),
                            if (sub.isNotEmpty) Text(sub.join(' · '), style: TextStyle(fontSize: 13, color: p.muted)),
                          ],
                        ),
                      ),
                      const SizedBox(width: 14),
                      _status(a, p),
                    ],
                  ),
                ),
                Expanded(
                  child: ListView(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    children: [
                      RowsCard(
                        header: Padding(
                          padding: const EdgeInsets.fromLTRB(16, 14, 16, 6),
                          child: const TwoLines('What it can do', 'Reads happen on their own. Anything that reaches people waits for your OK.'),
                        ),
                        children: [for (final t in d.tools) _toolRow(t, p)],
                      ),
                      const SizedBox(height: 12),
                      Container(
                        decoration: edged(p.card, edge: p.line),
                        padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            const Text('Who uses it', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w900)),
                            const SizedBox(height: 10),
                            Wrap(
                              spacing: 8,
                              runSpacing: 8,
                              children: [
                                for (final g in a.agents) _agentChip(g, p),
                                Semantics(
                                  button: true,
                                  child: InkWell(
                                    key: const Key('give-to'),
                                    borderRadius: BorderRadius.circular(999),
                                    onTap: () => _giveTo(a),
                                    child: Container(
                                      height: 32,
                                      padding: const EdgeInsets.symmetric(horizontal: 12),
                                      decoration: BoxDecoration(color: p.track, borderRadius: BorderRadius.circular(999)),
                                      child: Center(
                                        widthFactor: 1,
                                        child: Text('+ Give to…', style: TextStyle(fontSize: 13, fontWeight: FontWeight.w900, color: p.accent)),
                                      ),
                                    ),
                                  ),
                                ),
                              ],
                            ),
                          ],
                        ),
                      ),
                      if (d.recent.isNotEmpty) ...[
                        const SizedBox(height: 12),
                        Container(
                          decoration: edged(p.card, edge: p.line),
                          padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              const Text('Lately', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w900)),
                              for (final r in d.recent) ...[
                                const SizedBox(height: 6),
                                Row(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    SizedBox(
                                      width: 52,
                                      child: Text(
                                        _when(r.at),
                                        style: TextStyle(fontSize: 13, fontWeight: FontWeight.w700, color: p.muted),
                                      ),
                                    ),
                                    Expanded(
                                      child: Text(r.text, style: TextStyle(fontSize: 13, color: p.muted)),
                                    ),
                                  ],
                                ),
                              ],
                            ],
                          ),
                        ),
                      ],
                      const SizedBox(height: 12),
                    ],
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.fromLTRB(20, 12, 20, 28),
                  child: BigButton(primary: false, leading: Glyph.chat, label: 'Ask $who to do something with it', onPressed: () => _ask(a)),
                ),
              ],
            );
          },
        ),
      ),
    );
  }

  Widget _status(App a, Palette p) {
    final (bg, ink) = tintColors(switch (a.status.toJson()) {
      'on' => 'mint',
      'review' => 'sun',
      'broken' => 'rose',
      _ => 'violet',
    }, p);
    final label = switch (a.status.toJson()) {
      'on' => 'On',
      'review' => 'Review',
      'broken' => 'Broken',
      'found' => 'Found',
      _ => 'Off',
    };
    return Pill(label, background: bg, foreground: ink);
  }

  Widget _toolRow(AppTool t, Palette p) {
    final mode = _modes[t.name] ?? t.mode;
    final Widget trailing;
    if (t.switchable) {
      trailing = Toggle(key: Key('tool:${t.name}'), label: t.title, value: mode.toJson() == 'own', onChanged: (on) => _setTool(t, on));
    } else {
      final (label, tint) = switch (mode.toJson()) {
        'reads' => ('Reads', 'mint'),
        'after_ok' => ('After your OK', 'peach'),
        'own' => ('On its own', 'violet'),
        _ => ('Off', 'violet'),
      };
      final (bg, ink) = tintColors(tint, p);
      // Any tool can be set: reading on its own, acting after your OK, or off.
      trailing = PopupMenuButton<AppToolMode>(
        key: Key('tool:${t.name}'),
        tooltip: 'Change how ${t.title} is used',
        onSelected: (m) => _setMode(t, m),
        itemBuilder: (_) => [
          for (final (m, title, sub) in [
            (AppToolMode.reads, 'Runs on its own', 'For reading and looking things up'),
            (AppToolMode.afterOk, 'After your OK', 'For anything that changes or sends'),
            (AppToolMode.off, 'Off', 'Never used'),
          ])
            PopupMenuItem(
              key: Key('mode:${m.toJson()}'),
              value: m,
              child: TwoLines(title, sub, titleSize: 14, titleWeight: FontWeight.w800, subtitleSize: 12),
            ),
        ],
        child: Pill(label, background: bg, foreground: tint == 'peach' ? p.peachInk : ink),
      );
    }
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      child: Row(
        children: [
          Expanded(child: TwoLines(t.title, t.summary, titleSize: 14, titleWeight: FontWeight.w800, subtitleSize: 12)),
          const SizedBox(width: 12),
          trailing,
        ],
      ),
    );
  }

  Widget _agentChip(AgentRef g, Palette p) {
    final (strong, soft) = agentColors(g.color, p);
    return Container(
      height: 32,
      padding: const EdgeInsets.fromLTRB(4, 0, 10, 0),
      decoration: BoxDecoration(color: soft, borderRadius: BorderRadius.circular(999)),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          AgentRing(color: strong, size: 24),
          const SizedBox(width: 6),
          Text(g.name, style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w800)),
        ],
      ),
    );
  }
}

/// "07:30" today, "Mon" this week, else the date.
String _when(DateTime at) {
  final l = at.toLocal(), now = DateTime.now();
  if (l.year == now.year && l.month == now.month && l.day == now.day) return clock(at);
  if (now.difference(l).inDays < 7) return const ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'][l.weekday - 1];
  return '${l.day}/${l.month}';
}
