import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'bit.dart';
import 'chat_kit.dart';
import 'common.dart';
import 'icons.dart';
import 'theme.dart';

/// One agent: what it's doing, its standing jobs and what it can reach.
class AgentProfileScreen extends StatefulWidget {
  const AgentProfileScreen({super.key, required this.state, required this.agentId, this.onClose});

  final AppState state;
  final String agentId;

  /// Shown as a panel beside a chat: a close button instead of back, and no
  /// button to open the chat that's already open.
  final VoidCallback? onClose;

  @override
  State<AgentProfileScreen> createState() => _AgentProfileScreenState();
}

/// How wide the profile gets on a big screen, so lines stay readable.
const _maxWidth = 720.0;

class _AgentProfileScreenState extends State<AgentProfileScreen> {
  int _version = 0;
  bool _pausing = false;

  AppState get s => widget.state;

  Future<(Agent?, List<Job>)> _load() async {
    final r = await Future.wait([s.api.listAgents(), s.api.listJobs()]);
    final agent = (r[0] as AgentList).agents.where((a) => a.id == widget.agentId).firstOrNull;
    return (agent, (r[1] as JobList).jobs.where((j) => j.agent == widget.agentId).toList());
  }

  Future<void> _togglePause(Agent a) async {
    setState(() => _pausing = true);
    try {
      await s.api.updateAgent(
        agentId: a.id,
        body: AgentRequest(
          name: a.name,
          purpose: a.purpose,
          instructions: a.instructions,
          toolsets: a.toolsets,
          anyToolset: a.anyToolset,
          skills: a.skills,
          provider: a.provider == null ? null : AgentRequestProvider.fromJson(a.provider),
          model: a.model,
          baseUrl: a.baseUrl,
          capacity: a.capacity,
          color: AgentRequestColor.fromJson(a.color.toJson()),
          badge: AgentRequestBadge.fromJson(a.badge.toJson()),
          enabled: !a.enabled,
          delegates: a.delegates,
          rules: a.rules,
        ),
      );
      if (mounted) {
        showMessage(context, a.enabled ? '${a.name} is paused' : '${a.name} is back at work');
        setState(() => _version++);
      }
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    } finally {
      if (mounted) setState(() => _pausing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Scaffold(
      backgroundColor: p.ground,
      body: Loaded<(Agent?, List<Job>)>(
        key: ValueKey(_version),
        load: _load,
        builder: (context, v, reload) {
          final (a, jobs) = v;
          if (a == null) return const ErrorPanel(message: 'This agent is no longer on the team.');
          return ListenableBuilder(listenable: s, builder: (context, _) => _body(context, p, a, jobs, reload));
        },
      ),
    );
  }

  Widget _body(BuildContext context, Palette p, Agent a, List<Job> jobs, VoidCallback reload) {
    final ref = refOf(a);
    final (_, soft) = agentColors(a.color.toJson(), p);
    final ink = agentInk(a.color.toJson(), p);
    final busy = a.working.isNotEmpty;
    final reach = reachLabels(a);
    final model = a.model ?? "Worker's model";
    final purpose = a.purpose.trim().replaceAll(RegExp(r'\.$'), '');
    final about = [if (purpose.isNotEmpty) '$purpose.', '$model · ${a.capacity == 1 ? '1 task' : '${a.capacity} tasks'} at a time'].join(' ');
    final recent = _recent(a, jobs);
    return RefreshIndicator(
      onRefresh: () async => reload(),
      child: ListView(
        padding: EdgeInsets.zero,
        children: [
          Container(
            color: soft,
            alignment: Alignment.topCenter,
            child: SafeArea(
              bottom: false,
              child: ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: _maxWidth),
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Row(
                        children: [
                          if (widget.onClose != null)
                            SquareButton(
                              key: const Key('close-panel'),
                              icon: Icon(Icons.close_rounded, size: 22, color: p.ink),
                              label: 'Close',
                              width: 40,
                              height: 40,
                              radius: 14,
                              edge: 0,
                              onPressed: widget.onClose!,
                            )
                          else
                            SquareButton(
                              icon: LineIcon(Glyph.back, size: 22, color: p.ink, stroke: 2.6),
                              label: 'Back to team',
                              width: 40,
                              height: 40,
                              radius: 14,
                              edge: 0,
                              onPressed: () => Navigator.of(context).maybePop(),
                            ),
                          const Spacer(),
                          if (widget.onClose == null)
                            Tooltip(
                              message: 'Change anything about this agent by asking it',
                              child: EdgeCard(
                                radius: 14,
                                edge: 0,
                                onTap: () => openAgentChat(context, s, ref),
                                child: Container(
                                  height: 40,
                                  padding: const EdgeInsets.symmetric(horizontal: 12),
                                  child: Row(
                                    children: [
                                      Icon(Icons.chat_bubble_outline_rounded, size: 16, color: p.accent),
                                      const SizedBox(width: 6),
                                      Text(
                                        'Ask to change',
                                        style: TextStyle(fontSize: 13, fontWeight: FontWeight.w900, color: p.accent),
                                      ),
                                    ],
                                  ),
                                ),
                              ),
                            ),
                        ],
                      ),
                      const SizedBox(height: 10),
                      Row(
                        children: [
                          AgentAvatar(
                            color: ref.color,
                            badge: ref.badge,
                            size: 64,
                            fill: p.card,
                            name: a.name,
                            seed: a.id,
                            mood: busy ? BitMood.thinking : (a.enabled ? BitMood.idle : BitMood.sleeping),
                          ),
                          const SizedBox(width: 14),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  a.name,
                                  style: TextStyle(fontSize: 22, fontWeight: FontWeight.w900, color: p.ink),
                                ),
                                Text(a.enabled ? about : 'Paused · $about', style: TextStyle(fontSize: 13, color: ink)),
                              ],
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 10),
                      Row(
                        children: [
                          // Beside its chat, messaging it is the chat itself.
                          if (widget.onClose == null) ...[
                            Expanded(
                              child: _HeaderButton(
                                key: const Key('message-agent'),
                                label: 'Message',
                                icon: Icons.chat_bubble_outline_rounded,
                                primary: true,
                                onTap: () => openAgentChat(context, s, ref),
                              ),
                            ),
                            const SizedBox(width: 8),
                          ],
                          Expanded(
                            child: _HeaderButton(
                              key: const Key('give-job'),
                              label: 'Give it a job',
                              onTap: () => openAgentChat(context, s, ref, draft: 'Give you a job: '),
                            ),
                          ),
                          const SizedBox(width: 8),
                          Tooltip(
                            message: a.enabled ? 'Pause this agent' : 'Let this agent work again',
                            child: Material(
                              color: p.card,
                              shape: const CircleBorder(),
                              child: InkWell(
                                key: const Key('pause'),
                                customBorder: const CircleBorder(),
                                onTap: _pausing ? null : () => _togglePause(a),
                                child: SizedBox(
                                  width: 42,
                                  height: 42,
                                  child: Icon(a.enabled ? Icons.pause_rounded : Icons.play_arrow_rounded, color: p.ink, size: 22),
                                ),
                              ),
                            ),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
          Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: _maxWidth),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 14, 16, 32),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    if (busy)
                      _doingNow(context, p, a)
                    else
                      _note(p, a.enabled ? 'Not working on anything right now.' : 'Paused: it takes no new work until you let it.'),
                    const SizedBox(height: 16),
                    const SectionLabel('Instructions'),
                    const SizedBox(height: 8),
                    a.instructions.trim().isEmpty
                        ? _empty(
                            p,
                            'Only its purpose so far. Tell it how you like things done.',
                            'Add instructions',
                            () => openAgentChat(context, s, ref, draft: 'From now on, '),
                          )
                        : _note(p, a.instructions.trim()),
                    const SizedBox(height: 16),
                    const SectionLabel('Can reach'),
                    const SizedBox(height: 8),
                    Wrap(spacing: 6, runSpacing: 6, children: [for (final r in reach) ReachChip(r, height: 28, tintOutward: true)]),
                    if (reach.isEmpty)
                      _empty(
                        p,
                        'Nothing yet, so it can only think and write in chat.',
                        'Give it an app',
                        () => openAgentChat(context, s, ref, draft: 'Give yourself access to '),
                      ),
                    const SizedBox(height: 16),
                    const SectionLabel('Skills'),
                    const SizedBox(height: 8),
                    a.skills.isEmpty
                        ? _empty(
                            p,
                            'No skills yet. A skill teaches it a way of working, such as writing release notes.',
                            'Find a skill',
                            () => openAgentChat(context, s, ref, draft: 'Find a skill that helps you '),
                          )
                        : Wrap(spacing: 6, runSpacing: 6, children: [for (final sk in a.skills) ReachChip(sk, height: 28)]),
                    const SizedBox(height: 16),
                    const SectionLabel('Standing jobs · runs without a chat'),
                    const SizedBox(height: 8),
                    jobs.isEmpty
                        ? _empty(
                            p,
                            'None. Ask for one in plain words, like "every weekday at 9, brief me".',
                            'Give it a job',
                            () => openAgentChat(context, s, ref, draft: 'Give you a job: '),
                          )
                        : Container(
                            decoration: edgeBox(p, radius: 20),
                            child: Column(
                              children: [for (var i = 0; i < jobs.length; i++) _JobRow(job: jobs[i], divider: i < jobs.length - 1)],
                            ),
                          ),
                    const SizedBox(height: 16),
                    const SectionLabel('Who can ask it'),
                    const SizedBox(height: 8),
                    Wrap(spacing: 6, runSpacing: 6, children: [const ReachChip('You', height: 28), for (final d in a.delegates) ReachChip(d, height: 28)]),
                    const SizedBox(height: 16),
                    const SectionLabel('Recently'),
                    const SizedBox(height: 6),
                    if (recent.isEmpty) Text('Nothing yet.', style: TextStyle(fontSize: 13, color: p.muted)),
                    for (final (at, text) in recent)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 5),
                        child: Row(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            SizedBox(
                              width: 42,
                              child: Text(
                                chatTime(at) == 'Yesterday' ? 'Yest.' : chatTime(at),
                                style: TextStyle(fontSize: 13, fontWeight: FontWeight.w800, color: p.muted),
                              ),
                            ),
                            const SizedBox(width: 10),
                            Expanded(child: Text(text, style: const TextStyle(fontSize: 13))),
                          ],
                        ),
                      ),
                  ],
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  /// A short note in a card.
  Widget _note(Palette p, String text) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
    decoration: edgeBox(p, radius: 20),
    child: Text(text, style: TextStyle(fontSize: 13, color: p.ink)),
  );

  /// An empty section: what's missing, and one tap to fill it by asking.
  Widget _empty(Palette p, String text, String action, VoidCallback onTap) => Container(
    padding: const EdgeInsets.fromLTRB(14, 10, 8, 10),
    decoration: edgeBox(p, radius: 20),
    child: Row(
      children: [
        Expanded(
          child: Text(text, style: TextStyle(fontSize: 13, color: p.muted)),
        ),
        const SizedBox(width: 8),
        TextButton(
          onPressed: onTap,
          child: Text(
            action,
            style: TextStyle(fontWeight: FontWeight.w900, color: p.accent),
          ),
        ),
      ],
    ),
  );

  /// What the agent did lately: its standing jobs' runs and what it asked you.
  List<(DateTime, String)> _recent(Agent a, List<Job> jobs) {
    final out = <(DateTime, String)>[
      for (final j in jobs)
        if (j.lastRunAt != null) (j.lastRunAt!, 'Ran ${jobTitle(j)}'),
      for (final item in s.attention)
        if (agentOfItem(item, [a], s.threads)?.id == a.id)
          (
            item.at,
            switch (item.kind) {
              AttentionItemKind.approval => 'Asked you to approve ${actionName(item.action ?? item.text)}',
              AttentionItemKind.question => 'Asked you: ${item.text}',
              _ => item.text,
            },
          ),
    ]..sort((x, y) => y.$1.compareTo(x.$1));
    return out.take(5).toList();
  }

  Widget _doingNow(BuildContext context, Palette p, Agent a) {
    final first = a.working.first;
    final thread = s.threads.where((t) => t.id == first.threadId).firstOrNull;
    final where = thread == null ? null : (thread.group?.name ?? thread.agent?.name ?? thread.title);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      decoration: edgeBox(p, radius: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const LiveDot(size: 8),
              const SizedBox(width: 8),
              const SectionLabel('Doing now', color: Color(0xFF1E6E5A)),
              const Spacer(),
              if (thread != null && where != null)
                InkWell(
                  onTap: () => openThread(context, s, thread),
                  child: Text(
                    'in $where',
                    style: TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: p.accent),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 8),
          for (final w in a.working)
            Padding(
              padding: const EdgeInsets.only(bottom: 3),
              child: Text(w.scope, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w900)),
            ),
          if (a.waiting > 0) Text('${a.waiting} more waiting', style: TextStyle(fontSize: 13, color: p.muted)),
        ],
      ),
    );
  }
}

class _HeaderButton extends StatelessWidget {
  const _HeaderButton({super.key, required this.label, required this.onTap, this.icon, this.primary = false});

  final String label;
  final IconData? icon;
  final bool primary;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final fg = primary ? Colors.white : p.ink;
    return Material(
      color: primary ? p.accent : p.card,
      shape: const StadiumBorder(),
      child: InkWell(
        customBorder: const StadiumBorder(),
        onTap: onTap,
        child: SizedBox(
          height: 42,
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              if (icon != null) ...[Icon(icon, size: 16, color: fg), const SizedBox(width: 6)],
              Text(
                label,
                style: TextStyle(fontSize: 14, fontWeight: FontWeight.w900, color: fg),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _JobRow extends StatelessWidget {
  const _JobRow({required this.job, required this.divider});

  final Job job;
  final bool divider;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final j = job;
    final trigger = j.trigger.split(' ').first;
    final title = j.schedule.isNotEmpty ? scheduleWords(j.schedule) : (trigger.isNotEmpty ? 'When $trigger has something new' : jobTitle(j));
    return Container(
      key: Key('profile-job:${j.name}'),
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        border: divider ? Border(bottom: BorderSide(color: rowLine(p))) : null,
      ),
      child: Row(
        children: [
          LineIcon(j.schedule.isNotEmpty ? Glyph.clock : Glyph.bolt, size: 18, color: p.accent),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w800)),
                Text(j.description.isEmpty ? jobTitle(j) : j.description, style: TextStyle(fontSize: 12, color: p.muted)),
              ],
            ),
          ),
          if (jobAsks(j))
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
              decoration: BoxDecoration(color: p.peachSoft, borderRadius: BorderRadius.circular(999)),
              child: Text(
                'asks you',
                style: TextStyle(fontSize: 11, fontWeight: FontWeight.w900, color: p.peachInk),
              ),
            ),
        ],
      ),
    );
  }
}
