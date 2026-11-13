import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'bit.dart';
import 'chat_kit.dart';
import 'chat_screen.dart';
import 'icons.dart';
import 'new_group_screen.dart';
import 'plans_tab.dart';
import 'settings_screen.dart';
import 'theme.dart';
import 'thread_screen.dart';

enum _Filter { all, unread, groups }

/// Every chat with an agent or a group, newest first.
class ChatsTab extends StatefulWidget {
  const ChatsTab({super.key, required this.state});

  final AppState state;

  @override
  State<ChatsTab> createState() => _ChatsTabState();
}

class _ChatsTabState extends State<ChatsTab> {
  _Filter _filter = _Filter.all;
  bool _searching = false;
  final _query = TextEditingController();
  List<Agent> _agents = const [];
  List<Job> _jobs = const [];
  bool _loading = false;

  /// On a wide screen the open chat sits beside the list.
  bool _wide = false;
  String? _openId;

  AppState get s => widget.state;

  @override
  void initState() {
    super.initState();
    s.addListener(_loadTeam);
    _loadTeam();
  }

  @override
  void dispose() {
    s.removeListener(_loadTeam);
    _query.dispose();
    super.dispose();
  }

  /// The team and its jobs feed the strip above the chats. Owner only.
  Future<void> _loadTeam() async {
    if (_loading || !s.paired || !s.isOwner) return;
    _loading = true;
    try {
      final r = await Future.wait([s.api.listAgents(), s.api.listJobs()]);
      if (mounted) {
        setState(() {
          _agents = (r[0] as AgentList).agents;
          _jobs = (r[1] as JobList).jobs;
        });
      }
    } catch (_) {
      // The strip is a nicety; the chats still show.
    } finally {
      _loading = false;
    }
  }

  List<ThreadSummary> get _shown {
    final q = _query.text.trim().toLowerCase();
    return [
      for (final t in s.threads)
        if ((_filter != _Filter.unread || t.unread > 0) && (_filter != _Filter.groups || t.group != null))
          if (q.isEmpty || [_name(t), t.last?.text ?? '', t.title].any((x) => x.toLowerCase().contains(q))) t,
    ];
  }

  void _push(Widget w) => Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => w));

  /// Width from which the open chat sits beside the list.
  static const wideFrom = 900.0;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return LayoutBuilder(
      builder: (context, c) {
        _wide = c.maxWidth >= wideFrom;
        if (!_wide) return _list(context, p);
        return Row(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            SizedBox(width: 380, child: _list(context, p)),
            Container(width: 2, color: p.line),
            Expanded(
              child: ListenableBuilder(listenable: s, builder: (context, _) => _main(p)),
            ),
          ],
        );
      },
    );
  }

  /// The open chat in the main window: the one picked, else the latest.
  Widget _main(Palette p) {
    final t = s.threads.where((t) => t.id == _openId).firstOrNull ?? s.threads.firstOrNull;
    if (t == null) {
      return ColoredBox(
        color: p.ground,
        child: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Bit(size: 72),
              const SizedBox(height: 12),
              Text('Pick a chat, or start one with the pencil.', style: TextStyle(fontSize: 15, color: p.muted)),
            ],
          ),
        ),
      );
    }
    final chat = t.agent != null || t.group != null;
    return chat
        ? ChatScreen(key: ValueKey('chat:${t.id}'), state: s, threadId: t.id, agent: t.agent, group: t.group, embedded: true)
        : ThreadScreen(key: ValueKey('thread:${t.id}'), state: s, threadId: t.id);
  }

  void _openThread(ThreadSummary t) {
    if (_wide) {
      setState(() => _openId = t.id);
    } else {
      openThread(context, s, t);
    }
  }

  Widget _list(BuildContext context, Palette p) {
    return ColoredBox(
      color: p.ground,
      child: SafeArea(
        bottom: false,
        child: ListenableBuilder(
          listenable: s,
          builder: (context, _) {
            final threads = _shown;
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(20, 20, 20, 10),
                  child: Row(
                    children: [
                      const Expanded(
                        child: Text('Chats', style: TextStyle(fontSize: 28, fontWeight: FontWeight.w900, letterSpacing: -.4)),
                      ),
                      SquareButton(
                        icon: _searching ? Icon(Icons.close_rounded, size: 22, color: p.ink) : LineIcon(Glyph.search, size: 20, color: p.ink, stroke: 2.4),
                        label: 'Search',
                        onPressed: () => setState(() {
                          _searching = !_searching;
                          if (!_searching) _query.clear();
                        }),
                      ),
                      const SizedBox(width: 10),
                      SquareButton(
                        icon: Icon(Icons.settings_outlined, size: 21, color: p.ink),
                        label: 'Settings',
                        onPressed: () => _push(SettingsScreen(state: s)),
                      ),
                    ],
                  ),
                ),
                if (_searching)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
                    child: TextField(
                      key: const Key('search'),
                      controller: _query,
                      autofocus: true,
                      onChanged: (_) => setState(() {}),
                      decoration: InputDecoration(
                        hintText: 'Search chats',
                        prefixIcon: Icon(Icons.search_rounded, color: p.muted),
                        isDense: true,
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(999),
                          borderSide: BorderSide(color: p.line, width: 2),
                        ),
                        enabledBorder: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(999),
                          borderSide: BorderSide(color: p.line, width: 2),
                        ),
                        focusedBorder: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(999),
                          borderSide: BorderSide(color: p.accent, width: 2),
                        ),
                      ),
                    ),
                  )
                else
                  ?_strip(p),
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 10),
                  child: Row(
                    children: [
                      for (final f in _Filter.values) ...[
                        _FilterChip(
                          label: switch (f) {
                            _Filter.all => 'All',
                            _Filter.unread => 'Unread',
                            _Filter.groups => 'Groups',
                          },
                          selected: _filter == f,
                          onTap: () => setState(() => _filter = f),
                        ),
                        const SizedBox(width: 8),
                      ],
                    ],
                  ),
                ),
                Expanded(
                  child: Container(
                    decoration: BoxDecoration(
                      color: p.card,
                      borderRadius: const BorderRadius.vertical(top: Radius.circular(28)),
                      boxShadow: [BoxShadow(color: hairline(p), offset: const Offset(0, -2))],
                    ),
                    clipBehavior: Clip.antiAlias,
                    child: Stack(
                      children: [
                        RefreshIndicator(
                          onRefresh: s.refresh,
                          child: threads.isEmpty
                              ? ListView(children: [_empty(p)])
                              : ListView.builder(
                                  padding: const EdgeInsets.only(top: 4, bottom: 88),
                                  itemCount: threads.length,
                                  itemBuilder: (context, i) => _ChatRow(
                                    thread: threads[i],
                                    last: i == threads.length - 1,
                                    selected: _wide && threads[i].id == (_openId ?? s.threads.firstOrNull?.id),
                                    onTap: () => _openThread(threads[i]),
                                  ),
                                ),
                        ),
                        Positioned(
                          right: 20,
                          bottom: 12,
                          child: _Fab(onTap: () => _push(NewGroupScreen(state: s))),
                        ),
                      ],
                    ),
                  ),
                ),
              ],
            );
          },
        ),
      ),
    );
  }

  Widget _empty(Palette p) => Padding(
    padding: const EdgeInsets.fromLTRB(32, 48, 32, 32),
    child: Column(
      children: [
        const Bit(size: 72),
        const SizedBox(height: 12),
        Text(
          _filter == _Filter.all && _query.text.isEmpty ? 'No chats yet. Tap the pencil to start one.' : 'Nothing here.',
          textAlign: TextAlign.center,
          style: TextStyle(fontSize: 14, color: p.muted),
        ),
      ],
    ),
  );

  /// "3 agents working, outside any chat · Next: … · 2 need you".
  Widget? _strip(Palette p) {
    if (!s.isOwner) return null;
    final chats = {
      for (final t in s.threads)
        if (t.agent != null || t.group != null) t.id,
    };
    final working = _agents.where((a) => a.working.any((w) => !chats.contains(w.threadId))).length;
    final next = nextJob(_jobs, DateTime.now());
    final need = s.attention.length;
    if (working == 0 && next == null && need == 0) return null;
    final sub = [if (next != null) 'Next: ${jobTitle(next.$1)} at ${hhmm(next.$2)}', if (need > 0) '$need need${need == 1 ? 's' : ''} you'].join(' · ');
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      child: EdgeCard(
        key: const Key('working-strip'),
        radius: 20,
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        onTap: () => _push(Scaffold(body: PlansTab(state: s, pushed: true))),
        child: Row(
          children: [
            working > 0
                ? const LiveDot()
                : Container(
                    width: 10,
                    height: 10,
                    decoration: BoxDecoration(color: p.line, shape: BoxShape.circle),
                  ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    working > 0 ? '$working agent${working == 1 ? '' : 's'} working, outside any chat' : 'Nothing running outside your chats',
                    style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w900),
                  ),
                  if (sub.isNotEmpty) Text(sub, style: TextStyle(fontSize: 13, color: p.muted)),
                ],
              ),
            ),
            const SizedBox(width: 12),
            Text(
              'Plans',
              style: TextStyle(fontSize: 13, fontWeight: FontWeight.w900, color: p.accent),
            ),
          ],
        ),
      ),
    );
  }
}

String _name(ThreadSummary t) => t.group?.name ?? t.agent?.name ?? (t.title.isEmpty ? 'Bit' : t.title);

class _FilterChip extends StatelessWidget {
  const _FilterChip({required this.label, required this.selected, required this.onTap});

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return EdgeCard(
      radius: 999,
      edge: selected ? 0 : 2,
      color: selected ? p.accent : p.card,
      onTap: onTap,
      child: Container(
        height: 34,
        padding: const EdgeInsets.symmetric(horizontal: 14),
        alignment: Alignment.center,
        child: Text(
          label,
          style: TextStyle(fontSize: 14, fontWeight: FontWeight.w800, color: selected ? Colors.white : p.ink),
        ),
      ),
    );
  }
}

class _Fab extends StatelessWidget {
  const _Fab({required this.onTap});

  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Tooltip(
      message: 'New chat or group',
      child: DecoratedBox(
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(20),
          boxShadow: [BoxShadow(color: p.accentDeep, offset: const Offset(0, 4))],
        ),
        child: Material(
          color: p.accent,
          borderRadius: BorderRadius.circular(20),
          child: InkWell(
            key: const Key('new-chat'),
            borderRadius: BorderRadius.circular(20),
            onTap: onTap,
            child: const SizedBox(
              width: 56,
              height: 56,
              child: Icon(Icons.edit_outlined, color: Colors.white, size: 24, semanticLabel: 'New chat or group'),
            ),
          ),
        ),
      ),
    );
  }
}

class _ChatRow extends StatelessWidget {
  const _ChatRow({required this.thread, required this.last, required this.onTap, this.selected = false});

  final ThreadSummary thread;
  final bool last;

  /// The chat open beside the list.
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final t = thread;
    final needs = (t.needsYou ?? '').isNotEmpty;
    final unread = t.unread > 0;
    final timeColor = needs ? const Color(0xFFB4501E) : (unread ? const Color(0xFF1E7A62) : p.muted);
    final Widget avatar;
    if (t.group != null) {
      avatar = GroupAvatar(members: t.group!.members);
    } else if (t.agent != null) {
      avatar = AgentAvatar(color: t.agent!.color, badge: t.agent!.badge, size: 50, name: t.agent!.name, seed: t.agent!.id, mood: t.typing ? BitMood.thinking : BitMood.idle);
    } else {
      avatar = const AgentAvatar(color: 'violet', badge: 'star', size: 50, showBadge: false, name: 'Bit');
    }
    return Material(
      color: selected ? p.accentSoft : Colors.transparent,
      child: InkWell(
        key: Key('chat:${t.id}'),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 11, 20, 0),
          child: Row(
            children: [
              avatar,
              const SizedBox(width: 12),
              Expanded(
                child: Container(
                  padding: const EdgeInsets.only(bottom: 11),
                  decoration: BoxDecoration(
                    border: last ? null : Border(bottom: BorderSide(color: rowLine(p))),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.baseline,
                        textBaseline: TextBaseline.alphabetic,
                        children: [
                          Expanded(
                            child: Text(
                              _name(t),
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w900),
                            ),
                          ),
                          const SizedBox(width: 8),
                          Text(
                            chatTime(t.last?.at ?? t.updatedAt),
                            style: TextStyle(fontSize: 12, fontWeight: needs || unread ? FontWeight.w800 : FontWeight.w400, color: timeColor),
                          ),
                        ],
                      ),
                      const SizedBox(height: 2),
                      SizedBox(height: 22, child: _lastLine(p, needs)),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _lastLine(Palette p, bool needs) {
    final t = thread;
    if (t.typing) {
      return const Row(
        children: [
          Dots(color: Color(0xFF2FA585)),
          SizedBox(width: 4),
          Text(
            'thinking…',
            style: TextStyle(fontSize: 14, fontWeight: FontWeight.w800, color: Color(0xFF1E7A62)),
          ),
        ],
      );
    }
    final last = t.last;
    final muted = TextStyle(fontSize: 14, color: p.muted);
    final mine = last?.from == 'you';
    InlineSpan? who;
    if (last != null && t.group != null && !mine) {
      final m = t.group!.members.where((m) => m.name == last.from).firstOrNull;
      who = TextSpan(
        text: '${last.from}: ',
        style: TextStyle(fontWeight: FontWeight.w800, color: m == null ? p.ink : agentInk(m.color, p)),
      );
    }
    return Row(
      children: [
        if (mine) ...[
          Ticks(color: last!.seen == true ? p.accent : p.muted, double: last.seen == true, label: last.seen == true ? 'Seen' : 'Sent'),
          const SizedBox(width: 6),
        ],
        Expanded(
          child: Text.rich(
            TextSpan(
              children: [
                ?who,
                TextSpan(
                  text: last == null ? (t.group != null ? t.group!.members.map((m) => m.name).join(', ') : 'Say hi') : (mine ? 'You: ${last.text}' : last.text),
                ),
              ],
            ),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: muted,
          ),
        ),
        if (needs) ...[
          const SizedBox(width: 8),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 3),
            decoration: BoxDecoration(color: const Color(0xFFC2602E), borderRadius: BorderRadius.circular(999)),
            child: const Text(
              'Review',
              style: TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: Colors.white),
            ),
          ),
        ],
        if (t.unread > 0) ...[
          const SizedBox(width: 8),
          Container(
            key: Key('unread:${t.id}'),
            constraints: const BoxConstraints(minWidth: 22),
            height: 22,
            padding: const EdgeInsets.symmetric(horizontal: 6),
            alignment: Alignment.center,
            decoration: BoxDecoration(color: const Color(0xFF2FA585), borderRadius: BorderRadius.circular(999)),
            child: Text(
              '${t.unread}',
              style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: Colors.white),
            ),
          ),
        ],
      ],
    );
  }
}
