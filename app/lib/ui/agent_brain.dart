import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'chat_kit.dart';
import 'common.dart';
import 'theme.dart';

const _days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// "Wed 19 Nov", in this device's time.
String dayLabel(DateTime at) {
  final l = at.toLocal();
  return '${_days[l.weekday - 1]} ${l.day} ${_months[l.month - 1]}';
}

/// Whose tasks a memory comes from, when it isn't yours.
String? _whose(String person) => person == 'owner' ? null : 'for ${person.replaceFirst('delegate-', '')}';

/// What an agent keeps between tasks: what it is waiting on, what it knows,
/// and what went wrong lately. Each part can be corrected in one tap.
class AgentBrainSections extends StatefulWidget {
  const AgentBrainSections({super.key, required this.state, required this.agent, required this.brain, required this.onChanged});

  final AppState state;
  final Agent agent;
  final AgentBrain brain;
  final VoidCallback onChanged;

  @override
  State<AgentBrainSections> createState() => _AgentBrainSectionsState();
}

class _AgentBrainSectionsState extends State<AgentBrainSections> {
  bool _allFacts = false;
  bool _busy = false;

  AppState get s => widget.state;

  /// Two days after it was due, or after now when it is already overdue.
  DateTime _later(DateTime due) {
    final now = DateTime.now();
    return (due.isBefore(now) ? now : due).add(const Duration(days: 2)).toUtc();
  }

  /// Forgets a fact, with a way to take it back.
  Future<void> _forget(BrainFact f) async {
    setState(() => _busy = true);
    try {
      await s.api.deleteAgentFact(agentId: widget.agent.id, factId: f.id);
      if (!mounted) return;
      final messenger = ScaffoldMessenger.of(context);
      messenger
        ..hideCurrentSnackBar()
        ..showSnackBar(
          SnackBar(
            content: const Text('Forgotten'),
            action: SnackBarAction(
              label: 'Undo',
              onPressed: () => _act(() => s.api.restoreAgentFact(agentId: widget.agent.id, factId: f.id), 'Remembered again'),
            ),
          ),
        );
      widget.onChanged();
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _act(Future<void> Function() f, String done) async {
    setState(() => _busy = true);
    try {
      await f();
      if (mounted) showMessage(context, done);
      widget.onChanged();
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _toggleFollowUps() {
    final a = widget.agent;
    final off = !(a.followUpsOff ?? false);
    return _act(
      () => s.api.updateAgent(
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
          enabled: a.enabled,
          delegates: a.delegates,
          rules: a.rules,
          followUpsOff: off,
        ),
      ),
      off ? '${a.name} holds its follow-ups' : '${a.name} follows up again',
    );
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final b = widget.brain;
    final off = widget.agent.followUpsOff ?? false;
    final facts = [...b.facts.where((f) => f.putAwayAt == null)]..sort((x, y) => (x.kind == 'howto' ? 0 : 1).compareTo(y.kind == 'howto' ? 0 : 1));
    final putAway = b.facts.where((f) => f.putAwayAt != null).toList();
    final shown = _allFacts ? facts : facts.take(6).toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            const Expanded(child: SectionLabel('Waiting on')),
            if (b.waitingOn.isNotEmpty || off)
              TextButton(
                key: const Key('follow-ups-toggle'),
                onPressed: _busy ? null : _toggleFollowUps,
                child: Text(
                  off ? 'Resume follow-ups' : 'Hold follow-ups',
                  style: TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: p.accent),
                ),
              ),
          ],
        ),
        const SizedBox(height: 4),
        if (off) ...[_hint(p, 'Follow-ups are on hold: due ones wait until you resume them.'), const SizedBox(height: 6)],
        if (b.waitingOn.isEmpty)
          _hint(
            p,
            'Nothing. When it sends an email, it checks for a reply after 3 working days; mail you send from your own mail app isn\'t seen. Ask it to check back on anything.',
          )
        else
          _card(p, [for (final f in b.waitingOn) _followUp(p, f)]),
        const SizedBox(height: 16),
        const SectionLabel('What it knows'),
        const SizedBox(height: 8),
        if (facts.isEmpty)
          _hint(p, 'Nothing yet. It remembers what you tell it, like "Lena signs contracts with DocuSign".')
        else
          _card(p, [for (final f in shown) _fact(p, f)]),
        if (facts.length > shown.length)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton(
              onPressed: () => setState(() => _allFacts = true),
              child: Text(
                'Show all ${facts.length}',
                style: TextStyle(fontWeight: FontWeight.w900, color: p.accent),
              ),
            ),
          ),
        if (putAway.isNotEmpty)
          ExpansionTile(
            key: const Key('put-away'),
            tilePadding: const EdgeInsets.symmetric(horizontal: 4),
            title: Text(
              'Forgotten or corrected (${putAway.length})',
              style: TextStyle(fontSize: 13, fontWeight: FontWeight.w800, color: p.muted),
            ),
            children: [
              _card(p, [for (final f in putAway) _fact(p, f)]),
            ],
          ),
        if (b.wentWrong.isNotEmpty) ...[
          const SizedBox(height: 16),
          const SectionLabel('Went wrong lately'),
          const SizedBox(height: 8),
          _card(p, [for (final i in b.wentWrong) _incident(p, i)]),
        ],
      ],
    );
  }

  Widget _hint(Palette p, String text) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
    decoration: edgeBox(p, radius: 20),
    child: Text(text, style: TextStyle(fontSize: 13, color: p.muted)),
  );

  Widget _card(Palette p, List<Widget> rows) => Container(
    decoration: edgeBox(p, radius: 20),
    child: Column(
      children: [
        for (var i = 0; i < rows.length; i++)
          Container(
            decoration: BoxDecoration(
              border: i < rows.length - 1 ? Border(bottom: BorderSide(color: rowLine(p))) : null,
            ),
            child: rows[i],
          ),
      ],
    ),
  );

  Widget _tag(Palette p, String text, {Color? bg, Color? fg}) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
    decoration: BoxDecoration(color: bg ?? p.ground, borderRadius: BorderRadius.circular(999)),
    child: Text(
      text,
      style: TextStyle(fontSize: 11, fontWeight: FontWeight.w900, color: fg ?? p.muted),
    ),
  );

  Widget _followUp(Palette p, FollowUp f) {
    final now = f.state == 'fired';
    final when = now ? 'Checking now' : dayLabel(f.dueAt);
    final sub = [when, if (f.why != null) f.why!, if (_whose(f.person) != null) _whose(f.person)!].join(' · ');
    return Padding(
      key: Key('follow-up:${f.id}'),
      padding: const EdgeInsets.fromLTRB(14, 10, 4, 10),
      child: Row(
        children: [
          Icon(f.kind == 'reply' ? Icons.mark_email_unread_outlined : Icons.alarm_rounded, size: 18, color: p.accent),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(f.what, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w800)),
                Text(sub, style: TextStyle(fontSize: 12, color: p.muted)),
              ],
            ),
          ),
          PopupMenuButton<String>(
            key: Key('follow-up-menu:${f.id}'),
            tooltip: 'Change',
            enabled: !_busy,
            icon: Icon(Icons.more_horiz_rounded, color: p.muted),
            onSelected: (v) => v == 'cancel'
                ? _act(
                    () => s.api.updateFollowUp(
                      followUpId: f.id,
                      body: const FollowUpUpdate(action: 'cancel'),
                    ),
                    'Cancelled',
                  )
                : _act(
                    () => s.api.updateFollowUp(
                      followUpId: f.id,
                      body: FollowUpUpdate(action: 'snooze', until: _later(f.dueAt)),
                    ),
                    'Moved 2 days later',
                  ),
            itemBuilder: (_) => const [PopupMenuItem(value: 'snooze', child: Text('2 days later')), PopupMenuItem(value: 'cancel', child: Text('Cancel'))],
          ),
        ],
      ),
    );
  }

  Widget _fact(Palette p, BrainFact f) {
    final (label, bg, fg) = switch ((f.kind, f.source)) {
      ('howto', _) => ('how-to', p.mintSoft, p.mintInk),
      (_, 'person') => ('you said', null, null),
      (_, 'outside') => ('from mail or web', p.peachSoft, p.peachInk),
      _ => ('it noticed', null, null),
    };
    return Padding(
      key: Key('fact:${f.id}'),
      padding: const EdgeInsets.fromLTRB(14, 10, 4, 10),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(f.text, style: const TextStyle(fontSize: 14)),
                const SizedBox(height: 4),
                Wrap(
                  spacing: 6,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    _tag(p, label, bg: bg, fg: fg),
                    if (f.subject != null) Text(f.subject!, style: TextStyle(fontSize: 12, color: p.muted)),
                    if (_whose(f.person) != null) Text(_whose(f.person)!, style: TextStyle(fontSize: 12, color: p.muted)),
                  ],
                ),
              ],
            ),
          ),
          if (f.putAwayAt != null)
            TextButton(
              key: Key('restore:${f.id}'),
              onPressed: _busy ? null : () => _act(() => s.api.restoreAgentFact(agentId: widget.agent.id, factId: f.id), 'Remembered again'),
              child: Text(
                'Restore',
                style: TextStyle(fontWeight: FontWeight.w900, color: p.accent),
              ),
            )
          else
            IconButton(
              key: Key('forget:${f.id}'),
              tooltip: 'Forget this',
              onPressed: _busy ? null : () => _forget(f),
              icon: Icon(Icons.close_rounded, size: 18, color: p.muted),
            ),
        ],
      ),
    );
  }

  Widget _incident(Palette p, AgentIncident i) {
    final sub = [
      dayLabel(i.at),
      if (i.note != null) 'you said "${i.note}"' else if (i.detail != null) i.detail!,
      if (_whose(i.person) != null) _whose(i.person)!,
    ].join(' · ');
    return Padding(
      key: Key('incident:${i.id}'),
      padding: const EdgeInsets.fromLTRB(14, 10, 8, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(i.what, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w800)),
          Text(
            sub,
            maxLines: 3,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(fontSize: 12, color: p.muted),
          ),
          Wrap(
            alignment: WrapAlignment.end,
            children: [
              if (i.canKeep ?? false)
                TextButton(
                  key: Key('keep:${i.id}'),
                  onPressed: _busy
                      ? null
                      : () => _act(
                          () => s.api.updateIncident(
                            incidentId: i.id,
                            body: const IncidentUpdate(action: 'keep'),
                          ),
                          'It will remember that',
                        ),
                  child: Text(
                    'Make it a how-to',
                    style: TextStyle(fontWeight: FontWeight.w900, color: p.accent),
                  ),
                ),
              TextButton(
                key: Key('fixed:${i.id}'),
                onPressed: _busy
                    ? null
                    : () => _act(
                        () => s.api.updateIncident(
                          incidentId: i.id,
                          body: const IncidentUpdate(action: 'fixed'),
                        ),
                        'Marked fixed',
                      ),
                child: Text(
                  'Fixed',
                  style: TextStyle(fontWeight: FontWeight.w900, color: p.muted),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
