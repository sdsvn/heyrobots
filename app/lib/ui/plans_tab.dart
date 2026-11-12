import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'agent_profile_screen.dart';
import 'approval_screen.dart';
import 'chat_kit.dart';
import 'common.dart';
import 'icons.dart';
import 'theme.dart';
import 'thread_screen.dart';

/// Everything the team is doing and will do, whether it started in a chat
/// or not.
class PlansTab extends StatefulWidget {
  const PlansTab({super.key, required this.state, this.pushed = false});

  final AppState state;

  /// Opened on top of another screen, so it shows a back button.
  final bool pushed;

  @override
  State<PlansTab> createState() => _PlansTabState();
}

class _PlansTabState extends State<PlansTab> {
  bool _week = false;
  int _version = 0;

  AppState get s => widget.state;

  Future<(List<Agent>, List<Job>)> _load() async {
    if (!s.isOwner) return (const <Agent>[], const <Job>[]);
    final r = await Future.wait([s.api.listAgents(), s.api.listJobs()]);
    return ((r[0] as AgentList).agents, (r[1] as JobList).jobs);
  }

  Future<void> _push(Widget w) async {
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
              padding: EdgeInsets.fromLTRB(widget.pushed ? 12 : 20, 20, 20, 4),
              child: Row(
                children: [
                  if (widget.pushed)
                    IconButton(
                      tooltip: 'Back',
                      icon: LineIcon(Glyph.back, size: 24, color: p.ink, stroke: 2.6),
                      onPressed: () => Navigator.of(context).maybePop(),
                    ),
                  const Expanded(child: Text('Plans', style: TextStyle(fontSize: 28, fontWeight: FontWeight.w900, letterSpacing: -.4))),
                  Container(
                    padding: const EdgeInsets.all(3),
                    decoration: edgeBox(p, radius: 999, edge: 2),
                    child: Row(
                      children: [
                        _RangeButton(label: 'Today', selected: !_week, onTap: () => setState(() => _week = false)),
                        _RangeButton(label: 'Week', selected: _week, onTap: () => setState(() => _week = true)),
                      ],
                    ),
                  ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 0, 20, 12),
              child: Text('Everything your team is doing and will do, whether it started in a chat or not.', style: TextStyle(fontSize: 14, color: p.muted)),
            ),
            Expanded(
              child: Loaded<(List<Agent>, List<Job>)>(
                key: ValueKey(_version),
                load: _load,
                builder: (context, v, reload) => ListenableBuilder(
                  listenable: s,
                  builder: (context, _) => RefreshIndicator(
                    onRefresh: () async {
                      await s.refresh();
                      reload();
                    },
                    child: ListView(
                      padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
                      children: _sections(context, p, v.$1, v.$2),
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  String? _where(String threadId) {
    final t = s.threads.where((t) => t.id == threadId).firstOrNull;
    return t == null ? null : (t.group?.name ?? t.agent?.name ?? t.title);
  }

  List<Widget> _sections(BuildContext context, Palette p, List<Agent> agents, List<Job> jobs) {
    final byId = {for (final a in agents) a.id: a};
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    final end = _week ? today.add(const Duration(days: 7)) : today.add(const Duration(hours: 23, minutes: 59));
    final runs = <(DateTime, Job)>[
      for (final j in jobs)
        if (j.schedule.isNotEmpty)
          for (final at in runsBetween(j.schedule, today, end)) (at, j),
    ]..sort((a, b) => a.$1.compareTo(b.$1));
    final working = [for (final a in agents) for (final w in a.working) (a, w)];
    final ranToday = jobs.where((j) => j.lastRunAt != null && !j.lastRunAt!.toLocal().isBefore(today)).length;
    final out = <Widget>[];

    void section(String title, Color? color, List<Widget> body) {
      if (out.isNotEmpty) out.add(const SizedBox(height: 14));
      out.add(SectionLabel(title, color: color));
      out.add(const SizedBox(height: 8));
      out.addAll(body);
    }

    if (s.attention.isNotEmpty) {
      section('Needs you · ${s.attention.length}', const Color(0xFFB4501E), [
        for (final item in s.attention) ...[_needsYou(context, p, item, agents), const SizedBox(height: 8)],
      ]..removeLast());
    }

    if (working.isNotEmpty) {
      section('Now', p.mintInk, [
        Container(
          decoration: edgeBox(p, radius: 18),
          clipBehavior: Clip.antiAlias,
          child: Material(
            type: MaterialType.transparency,
            child: Column(
              children: [
                for (var i = 0; i < working.length; i++)
                  _Row(
                    divider: i < working.length - 1,
                    leading: ColorDot(color: working[i].$1.color.toJson()),
                    title: working[i].$2.scope,
                    titleWeight: FontWeight.w800,
                    subtitle: [working[i].$1.name, if (_where(working[i].$2.threadId) case final w?) 'in $w'].join(' · '),
                    trailing: LiveDot(size: 8, color: p.mint),
                    onTap: () {
                      final t = s.threads.where((t) => t.id == working[i].$2.threadId).firstOrNull;
                      if (t != null) {
                        openThread(context, s, t);
                      } else {
                        _push(ThreadScreen(state: s, threadId: working[i].$2.threadId));
                      }
                    },
                  ),
              ],
            ),
          ),
        ),
      ]);
    }

    if (s.isOwner) {
      section(_week ? 'Coming up this week' : 'Coming up today', null, [
        if (runs.isEmpty)
          Padding(
            padding: const EdgeInsets.only(left: 4),
            child: Text(_week ? 'Nothing scheduled this week.' : 'Nothing scheduled for today.', style: TextStyle(fontSize: 14, color: p.muted)),
          )
        else
          Container(
            margin: const EdgeInsets.only(left: 12),
            padding: const EdgeInsets.only(left: 16),
            decoration: const BoxDecoration(border: Border(left: BorderSide(color: Color(0xFFD7D2EA), width: 2))),
            child: Column(
              children: [
                for (final (at, j) in runs)
                  _TimelineRow(
                    key: Key('run:${j.name}:${at.toIso8601String()}'),
                    at: at,
                    showDay: _week,
                    job: j,
                    agent: byId[j.agent],
                    past: at.isBefore(now),
                    onTap: byId[j.agent] == null ? null : () => _push(AgentProfileScreen(state: s, agentId: j.agent)),
                  ),
              ],
            ),
          ),
      ]);

      if (jobs.isNotEmpty) {
        out.add(const SizedBox(height: 14));
        final first = jobs.firstWhere((j) => byId.containsKey(j.agent), orElse: () => jobs.first);
        out.add(EdgeCard(
          key: const Key('standing-jobs'),
          radius: 18,
          edge: 0,
          color: p.accentSoft,
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
          onTap: () => _push(AgentProfileScreen(state: s, agentId: first.agent)),
          child: Row(
            children: [
              LineIcon(Glyph.bolt, color: p.accent, size: 22),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('${jobs.length} standing job${jobs.length == 1 ? '' : 's'} across the team', style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w900)),
                    Text('Schedules and triggers · $ranToday done today', style: TextStyle(fontSize: 12, color: p.accentDeep)),
                  ],
                ),
              ),
              LineIcon(Glyph.chevron, color: p.accent, size: 20),
            ],
          ),
        ));
      }
    }
    if (out.isEmpty) {
      out.add(Padding(
        padding: const EdgeInsets.only(top: 32),
        child: Text('Nothing is waiting on you.', textAlign: TextAlign.center, style: TextStyle(fontSize: 14, color: p.muted)),
      ));
    }
    return out;
  }

  Widget _needsYou(BuildContext context, Palette p, AttentionItem item, List<Agent> agents) {
    final agent = agentOfItem(item, agents, s.threads);
    final approval = item.kind == AttentionItemKind.approval;
    final title = switch (item.kind) {
      AttentionItemKind.approval => item.scope ?? 'Approve ${actionName(item.action ?? item.text)}?',
      AttentionItemKind.question => item.text,
      AttentionItemKind.failure => "Something couldn't be sent",
      _ => item.text,
    };
    final where = _where(item.threadId);
    final sub = [
      ?agent?.name,
      if (approval && item.scope != null) 'Approve ${actionName(item.action ?? item.text)}' else if (where != null) 'in $where',
    ].join(' · ');
    return EdgeCard(
      key: Key('needs:${item.id}'),
      radius: 18,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      onTap: () {
        if (item.approvalId != null) {
          _push(ApprovalScreen(state: s, approvalId: item.approvalId!));
          return;
        }
        final t = s.threads.where((t) => t.id == item.threadId).firstOrNull;
        if (t != null) {
          openThread(context, s, t);
        } else {
          _push(ThreadScreen(state: s, threadId: item.threadId));
        }
      },
      child: Row(
        children: [
          agent == null ? const ColorDot(color: 'peach') : ColorDot(color: agent.color),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, maxLines: 2, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w900)),
                if (sub.isNotEmpty) Text(sub, maxLines: 1, overflow: TextOverflow.ellipsis, style: TextStyle(fontSize: 12, color: p.muted)),
              ],
            ),
          ),
          const SizedBox(width: 10),
          if (approval)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
              decoration: BoxDecoration(color: p.accent, borderRadius: BorderRadius.circular(999)),
              child: const Text('Review', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: Colors.white)),
            )
          else
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 3),
              decoration: BoxDecoration(border: Border.all(color: p.accent, width: 2), borderRadius: BorderRadius.circular(999)),
              child: Text(item.kind == AttentionItemKind.question ? 'Answer' : 'Open', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: p.accent)),
            ),
        ],
      ),
    );
  }
}

class _RangeButton extends StatelessWidget {
  const _RangeButton({required this.label, required this.selected, required this.onTap});

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Material(
      color: selected ? p.accent : Colors.transparent,
      shape: const StadiumBorder(),
      child: InkWell(
        key: Key('range:$label'),
        customBorder: const StadiumBorder(),
        onTap: onTap,
        child: Container(
          height: 32,
          padding: const EdgeInsets.symmetric(horizontal: 12),
          alignment: Alignment.center,
          child: Text(label, style: TextStyle(fontSize: 13, fontWeight: selected ? FontWeight.w900 : FontWeight.w800, color: selected ? Colors.white : p.ink)),
        ),
      ),
    );
  }
}

class _Row extends StatelessWidget {
  const _Row({required this.leading, required this.title, required this.subtitle, required this.divider, this.trailing, this.onTap, this.titleWeight = FontWeight.w900});

  final Widget leading;
  final String title, subtitle;
  final bool divider;
  final Widget? trailing;
  final VoidCallback? onTap;
  final FontWeight titleWeight;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return InkWell(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(border: divider ? Border(bottom: BorderSide(color: rowLine(p))) : null),
        child: Row(
          children: [
            leading,
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: TextStyle(fontSize: 14, fontWeight: titleWeight)),
                  if (subtitle.isNotEmpty) Text(subtitle, style: TextStyle(fontSize: 12, color: p.muted)),
                ],
              ),
            ),
            if (trailing != null) ...[const SizedBox(width: 10), trailing!],
          ],
        ),
      ),
    );
  }
}

const _weekdays = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

class _TimelineRow extends StatelessWidget {
  const _TimelineRow({super.key, required this.at, required this.showDay, required this.job, required this.agent, required this.past, this.onTap});

  final DateTime at;
  final bool showDay, past;
  final Job job;
  final Agent? agent;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final (strong, _) = agentColors(agent?.color.toJson() ?? 'violet', p);
    // Who planned it comes first: an agent proposed it, or you set it.
    final sub = [
      job.plannedBy != null ? 'planned by ${agent?.name ?? 'an agent'}' : 'you set this${agent == null ? '' : ' · ${agent!.name}'}',
      scheduleDays(job.schedule),
      if (jobAsks(job)) 'asks you first',
    ].join(' · ');
    return InkWell(
      onTap: onTap,
      child: Opacity(
        opacity: past && !showDay ? .55 : 1,
        child: Stack(
          clipBehavior: Clip.none,
          children: [
            Positioned(
              left: -24,
              top: 0,
              bottom: 0,
              child: Center(
                child: Container(
                  width: 14,
                  height: 14,
                  decoration: BoxDecoration(color: strong, shape: BoxShape.circle, border: Border.all(color: p.ground, width: 3)),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 6),
              child: Row(
                children: [
                  SizedBox(
                    width: 44,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        if (showDay) Text(_weekdays[at.weekday - 1], style: TextStyle(fontSize: 11, fontWeight: FontWeight.w800, color: p.muted)),
                        Text(hhmm(at), style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w800)),
                      ],
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(jobTitle(job), style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w800)),
                        Text(sub, style: TextStyle(fontSize: 12, color: p.muted)),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
