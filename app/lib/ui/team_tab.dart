import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'agent_profile_screen.dart';
import 'agents_screen.dart';
import 'bit.dart';
import 'chat_kit.dart';
import 'common.dart';
import 'icons.dart';
import 'theme.dart';

/// The team: each agent with its reach, what it's doing and what's next.
class TeamTab extends StatefulWidget {
  const TeamTab({super.key, required this.state});

  final AppState state;

  @override
  State<TeamTab> createState() => _TeamTabState();
}

class _TeamTabState extends State<TeamTab> {
  int _version = 0;

  AppState get s => widget.state;

  Future<(List<Agent>, List<Job>)> _load() async {
    final r = await Future.wait([s.api.listAgents(), s.api.listJobs()]);
    return ((r[0] as AgentList).agents, (r[1] as JobList).jobs);
  }

  Future<void> _open(Widget w) async {
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => w));
    if (mounted) setState(() => _version++);
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return ColoredBox(
      color: p.ground,
      child: SafeArea(
        bottom: false,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 20, 20, 4),
              child: Row(
                children: [
                  const Expanded(child: Text('Team', style: TextStyle(fontSize: 28, fontWeight: FontWeight.w900, letterSpacing: -.4))),
                  if (s.isOwner)
                    Tooltip(
                      message: 'Add an agent',
                      child: DecoratedBox(
                        decoration: BoxDecoration(borderRadius: BorderRadius.circular(16), boxShadow: [BoxShadow(color: p.accentDeep, offset: const Offset(0, 3))]),
                        child: Material(
                          color: p.accent,
                          borderRadius: BorderRadius.circular(16),
                          child: InkWell(
                            key: const Key('add-agent'),
                            borderRadius: BorderRadius.circular(16),
                            onTap: () => _open(AgentEditScreen(state: s)),
                            child: SizedBox(
                              height: 44,
                              child: Padding(
                                padding: EdgeInsets.symmetric(horizontal: 16),
                                child: Row(
                                  children: [
                                    LineIcon(Glyph.plus, color: Colors.white, size: 18, stroke: 3),
                                    SizedBox(width: 6),
                                    Text('Agent', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w900, color: Colors.white)),
                                  ],
                                ),
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 0, 20, 12),
              child: Text('Each agent has its own reach and jobs. They work when you chat, and on their own.', style: TextStyle(fontSize: 14, color: p.muted)),
            ),
            Expanded(
              child: !s.isOwner
                  ? const ErrorPanel(message: 'The team is managed on the owner’s phone.')
                  : Loaded<(List<Agent>, List<Job>)>(
                      key: ValueKey(_version),
                      load: _load,
                      builder: (context, v, reload) => ListenableBuilder(
                        listenable: s,
                        builder: (context, _) => _list(context, v.$1, v.$2, reload),
                      ),
                    ),
            ),
          ],
        ),
      ),
    );
  }

  int _needsYou(Agent a) => s.attention.where((i) => agentOfItem(i, [a], s.threads)?.id == a.id).length;

  Widget _list(BuildContext context, List<Agent> agents, List<Job> jobs, VoidCallback reload) {
    final active = <Agent>[];
    final idle = <Agent>[];
    for (final a in agents) {
      (a.working.isNotEmpty || _needsYou(a) > 0 || a.waiting > 0 ? active : idle).add(a);
    }
    // Unavailable agents come last.
    idle.sort((x, y) => (unavailable(x) ? 1 : 0) - (unavailable(y) ? 1 : 0));
    final now = DateTime.now();
    Widget idleCard(Agent a) => _IdleCard(agent: a, jobs: jobs.where((j) => j.agent == a.id).length, onTap: () => _open(AgentProfileScreen(state: s, agentId: a.id)));
    return RefreshIndicator(
      onRefresh: () async => reload(),
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
        children: [
          for (final a in active) ...[
            _AgentCard(
              agent: a,
              jobs: jobs.where((j) => j.agent == a.id).toList(),
              needsYou: _needsYou(a),
              now: now,
              onTap: () => _open(AgentProfileScreen(state: s, agentId: a.id)),
            ),
            const SizedBox(height: 12),
          ],
          for (var i = 0; i < idle.length; i += 2) ...[
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: idleCard(idle[i])),
                const SizedBox(width: 10),
                Expanded(child: i + 1 < idle.length ? idleCard(idle[i + 1]) : const SizedBox.shrink()),
              ],
            ),
            const SizedBox(height: 12),
          ],
        ],
      ),
    );
  }
}

class _AgentCard extends StatelessWidget {
  const _AgentCard({required this.agent, required this.jobs, required this.needsYou, required this.now, required this.onTap});

  final Agent agent;
  final List<Job> jobs;
  final int needsYou;
  final DateTime now;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final a = agent;
    final busy = a.working.isNotEmpty;
    final next = nextJob(jobs, now);
    final today = DateTime(now.year, now.month, now.day);
    final done = (jobs.where((j) => j.lastRunAt != null && !j.lastRunAt!.toLocal().isBefore(today)).toList()
          ..sort((x, y) => y.lastRunAt!.compareTo(x.lastRunAt!)))
        .firstOrNull;
    final lines = <(String, String, bool)>[
      if (done != null) ('Done', 'Ran ${jobTitle(done)} at ${hhmm(done.lastRunAt!)}', false),
      if (busy) ('Now', '${a.working.first.scope}${a.working.length > 1 ? ' · ${a.working.length - 1} more' : ''}', true),
      if (next != null || a.waiting > 0 || jobs.isNotEmpty)
        ('Next', [
          if (a.waiting > 0) '${a.waiting} waiting',
          if (next != null) '${jobTitle(next.$1)} at ${hhmm(next.$2)}',
          if (jobs.length > 1 || next == null && jobs.isNotEmpty) jobs.length == 1 ? '1 standing job' : '${jobs.length} standing jobs',
        ].join(' · '), false),
    ];
    return EdgeCard(
      key: Key('team:${a.name}'),
      onTap: onTap,
      padding: const EdgeInsets.all(14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              AgentAvatar(color: a.color.toJson(), badge: a.badge.toJson(), size: 48, name: a.name, mood: busy ? BitMood.thinking : BitMood.idle),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(a.name, style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w900)),
                    if (a.purpose.isNotEmpty) Text(a.purpose, maxLines: 2, overflow: TextOverflow.ellipsis, style: TextStyle(fontSize: 13, color: p.muted)),
                  ],
                ),
              ),
              if (needsYou > 0)
                Container(
                  margin: const EdgeInsets.only(left: 8),
                  padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
                  decoration: BoxDecoration(color: p.peachSoft, borderRadius: BorderRadius.circular(999)),
                  child: Text('$needsYou needs you', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: p.peachInk)),
                )
              else if (busy)
                Container(
                  margin: const EdgeInsets.only(left: 8),
                  padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
                  decoration: BoxDecoration(color: p.mintSoft, borderRadius: BorderRadius.circular(999)),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      LiveDot(size: 7, color: p.mint),
                      const SizedBox(width: 5),
                      Text('Working', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: p.mintInk)),
                    ],
                  ),
                ),
            ],
          ),
          const SizedBox(height: 10),
          Wrap(spacing: 6, runSpacing: 6, children: [for (final r in reachLabels(a)) ReachChip(r)]),
          if (lines.isNotEmpty) ...[
            const SizedBox(height: 10),
            for (final (label, text, live) in lines)
              Padding(
                padding: const EdgeInsets.only(bottom: 4),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    SizedBox(width: 40, child: Text(label, style: TextStyle(fontSize: 13, fontWeight: FontWeight.w800, color: live ? p.mintInk : p.muted))),
                    const SizedBox(width: 8),
                    Expanded(child: Text(text, style: const TextStyle(fontSize: 13))),
                  ],
                ),
              ),
          ],
        ],
      ),
    );
  }
}

class _IdleCard extends StatelessWidget {
  const _IdleCard({required this.agent, required this.jobs, required this.onTap});

  final Agent agent;
  final int jobs;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final a = agent;
    final what = unavailable(a)
        ? 'Unavailable${a.unavailableSince == null ? '' : ' since ${hhmm(a.unavailableSince!)}'}'
        : !a.enabled
        ? 'Paused'
        : a.presence?.toJson() == 'busy'
        ? 'Working'
        : jobs > 0
        ? 'Idle · $jobs job${jobs == 1 ? '' : 's'}'
        : a.builtin || a.anyToolset
        ? 'Idle · anything else'
        : 'Idle';
    final card = EdgeCard(
      key: Key('team:${a.name}'),
      radius: 20,
      padding: const EdgeInsets.all(12),
      onTap: onTap,
      child: Row(
        children: [
          ColorDot(color: a.color.toJson(), size: 36),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(a.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w900)),
                Text(what, maxLines: 1, overflow: TextOverflow.ellipsis, style: TextStyle(fontSize: 12, color: p.muted)),
              ],
            ),
          ),
        ],
      ),
    );
    return unavailable(a) ? Opacity(opacity: .5, child: card) : card;
  }
}

/// Whether an agent's machine is down.
bool unavailable(Agent a) => a.presence?.toJson() == 'unavailable';
