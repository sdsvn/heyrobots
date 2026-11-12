import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'agents_screen.dart' show toolsetLabel;
import 'bit.dart';
import 'chat_screen.dart';
import 'theme.dart';
import 'thread_screen.dart';

/// Small pieces the chat, team and plans screens share.

/// An agent's color dark enough for text on white.
Color agentInk(String color, Palette p) {
  if (p.ground.computeLuminance() < .2) return agentColors(color, p).$1;
  return switch (color) {
    'peach' => const Color(0xFFC2602E),
    'mint' => const Color(0xFF1E7A62),
    'sky' => const Color(0xFF1F5F99),
    'rose' => const Color(0xFFB23A52),
    'sun' => const Color(0xFFB07A0F),
    _ => p.accentDeep,
  };
}

/// The thin line under the chats sheet and headers.
Color hairline(Palette p) => p.ground.computeLuminance() < .2 ? p.line : const Color(0xFFEEE9FC);

/// The divider between rows inside a card.
Color rowLine(Palette p) => p.ground.computeLuminance() < .2 ? p.line : const Color(0xFFF0ECFB);

const _days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

String two(int n) => n.toString().padLeft(2, '0');

String hhmm(DateTime at) {
  final l = at.toLocal();
  return '${two(l.hour)}:${two(l.minute)}';
}

DateTime _day(DateTime d) => DateTime(d.year, d.month, d.day);

/// 09:41 today, Yesterday, Mon this week, else 3 Nov.
String chatTime(DateTime at, {DateTime? now}) {
  final l = at.toLocal();
  final days = _day(now ?? DateTime.now()).difference(_day(l)).inDays;
  if (days <= 0) return hhmm(l);
  if (days == 1) return 'Yesterday';
  if (days < 7) return _days[l.weekday - 1];
  return '${l.day} ${_months[l.month - 1]}';
}

/// Today, Yesterday, Monday-ish, or a date, for the separators in a chat.
String dayLabel(DateTime at, {DateTime? now}) {
  final t = chatTime(at, now: now);
  return t.contains(':') ? 'Today' : t;
}

/// White surface with the design's solid bottom edge.
BoxDecoration edgeBox(Palette p, {double radius = 22, double edge = 3, Color? color}) => BoxDecoration(
  color: color ?? p.card,
  borderRadius: BorderRadius.circular(radius),
  boxShadow: [BoxShadow(color: p.line, offset: Offset(0, edge))],
);

/// A tappable [edgeBox].
class EdgeCard extends StatelessWidget {
  const EdgeCard({super.key, required this.child, this.onTap, this.radius = 22, this.edge = 3, this.padding = EdgeInsets.zero, this.color});

  final Widget child;
  final VoidCallback? onTap;
  final double radius, edge;
  final EdgeInsets padding;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final r = BorderRadius.circular(radius);
    return DecoratedBox(
      decoration: BoxDecoration(borderRadius: r, boxShadow: edge == 0 ? null : [BoxShadow(color: p.line, offset: Offset(0, edge))]),
      child: Material(
        color: color ?? p.card,
        borderRadius: r,
        clipBehavior: Clip.antiAlias,
        child: InkWell(onTap: onTap, child: Padding(padding: padding, child: child)),
      ),
    );
  }
}

/// A square-ish header button: white with an edge, or ground-tinted.
class SquareButton extends StatelessWidget {
  const SquareButton({super.key, required this.icon, required this.label, required this.onPressed, this.width = 44, this.height = 44, this.radius = 16, this.color, this.edge = 2});

  final Widget icon;
  final String label;
  final VoidCallback onPressed;
  final double width, height, radius, edge;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: label,
      child: EdgeCard(
        radius: radius,
        edge: edge,
        color: color,
        onTap: onPressed,
        child: Semantics(label: label, button: true, child: SizedBox(width: width, height: height, child: Center(child: icon))),
      ),
    );
  }
}

/// Runs a looping animation unless animations are off (tests, reduced motion).
class Looping extends StatefulWidget {
  const Looping({super.key, required this.period, required this.builder});

  final Duration period;
  final Widget Function(BuildContext context, double t) builder;

  @override
  State<Looping> createState() => _LoopingState();
}

class _LoopingState extends State<Looping> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: widget.period);

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = !Bit.animate || MediaQuery.maybeDisableAnimationsOf(context) == true;
    if (still) {
      _c.stop();
      _c.value = .4;
    } else if (!_c.isAnimating) {
      _c.repeat();
    }
  }

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(animation: _c, builder: (context, _) => widget.builder(context, _c.value));
}

/// A dot that softly pulses while something is running.
class LiveDot extends StatelessWidget {
  const LiveDot({super.key, this.size = 10, this.color = const Color(0xFF2FA585)});

  final double size;
  final Color color;

  @override
  Widget build(BuildContext context) => Looping(
    period: const Duration(milliseconds: 1600),
    builder: (context, t) => Opacity(
      opacity: .5 + .5 * (1 - (2 * t - 1).abs()),
      child: Container(width: size, height: size, decoration: BoxDecoration(color: color, shape: BoxShape.circle)),
    ),
  );
}

/// Three dots that blink in turn; [bounce] lifts them too, as in a bubble.
class Dots extends StatelessWidget {
  const Dots({super.key, required this.color, this.size = 5, this.gap = 2, this.bounce = false});

  final Color color;
  final double size, gap;
  final bool bounce;

  @override
  Widget build(BuildContext context) => ExcludeSemantics(
    child: Looping(
      period: const Duration(milliseconds: 1200),
      builder: (context, t) => Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (var i = 0; i < 3; i++)
            Builder(builder: (_) {
              final ph = (t - i * .125) % 1;
              final k = ph < .4 ? ph / .4 : (ph < .8 ? 1 - (ph - .4) / .4 : 0.0);
              return Padding(
                padding: EdgeInsets.only(right: i < 2 ? gap : 0),
                child: Transform.translate(
                  offset: Offset(0, bounce ? -4 * k : 0),
                  child: Container(
                    width: size,
                    height: size,
                    decoration: BoxDecoration(color: color.withValues(alpha: .35 + .65 * k), shape: BoxShape.circle),
                  ),
                ),
              );
            }),
        ],
      ),
    ),
  );
}

/// ✓ sent, ✓✓ taken on or seen.
class Ticks extends StatelessWidget {
  const Ticks({super.key, required this.color, this.double = true, this.label});

  final Color color;
  final bool double;
  final String? label;

  @override
  Widget build(BuildContext context) => Semantics(
    label: label,
    child: CustomPaint(size: const Size(18, 12), painter: _TicksPainter(color, double)),
  );
}

class _TicksPainter extends CustomPainter {
  _TicksPainter(this.color, this.twice);

  final Color color;
  final bool twice;

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 2
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round;
    canvas.drawPath(Path()..moveTo(1, 6.5)..lineTo(4.5, 10)..lineTo(11, 3), paint);
    if (twice) canvas.drawPath(Path()..moveTo(7, 9.5)..lineTo(7.5, 10)..lineTo(14, 3), paint);
  }

  @override
  bool shouldRepaint(_TicksPainter old) => old.color != color || old.twice != twice;
}

/// Two members' faces, one over the other, for a group.
class GroupAvatar extends StatelessWidget {
  const GroupAvatar({super.key, required this.members, this.width = 50, this.height = 50, this.face = 34, this.ring = 3});

  final List<AgentRef> members;
  final double width, height, face, ring;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    Widget one(AgentRef? a) => a == null
        ? Container(width: face, height: face, decoration: BoxDecoration(color: p.accentSoft, shape: BoxShape.circle, border: Border.all(color: p.line, width: 2)))
        : AgentAvatar(color: a.color, badge: a.badge, size: face, showBadge: false, name: a.name);
    return SizedBox(
      width: width,
      height: height,
      child: Stack(
        children: [
          Positioned(left: 0, top: 0, child: one(members.firstOrNull)),
          Positioned(
            right: 0,
            bottom: 0,
            child: Container(
              decoration: BoxDecoration(shape: BoxShape.circle, boxShadow: [BoxShadow(color: p.card, spreadRadius: ring)]),
              child: one(members.length > 1 ? members[1] : null),
            ),
          ),
        ],
      ),
    );
  }
}

/// The agent's circle without Bit inside, for compact lists.
class ColorDot extends StatelessWidget {
  const ColorDot({super.key, required this.color, this.size = 26});

  final String color;
  final double size;

  @override
  Widget build(BuildContext context) {
    final (strong, soft) = agentColors(color, Palette.of(context));
    return Container(width: size, height: size, decoration: BoxDecoration(color: soft, shape: BoxShape.circle, border: Border.all(color: strong, width: 2)));
  }
}

/// An uppercase section title.
class SectionLabel extends StatelessWidget {
  const SectionLabel(this.text, {super.key, this.color, this.size = 12, this.weight = FontWeight.w900});

  final String text;
  final Color? color;
  final double size;
  final FontWeight weight;

  @override
  Widget build(BuildContext context) => Text(
    text.toUpperCase(),
    style: TextStyle(fontSize: size, fontWeight: weight, letterSpacing: .6, color: color ?? Palette.of(context).accent),
  );
}

/// What an agent can reach, as a small chip; outward ones in peach.
class ReachChip extends StatelessWidget {
  const ReachChip(this.text, {super.key, this.height = 26, this.tintOutward = false});

  final String text;
  final double height;
  final bool tintOutward;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final ask = tintOutward && text.contains('after your OK');
    return Container(
      height: height,
      padding: EdgeInsets.symmetric(horizontal: height > 26 ? 10 : 9),
      decoration: BoxDecoration(color: ask ? p.peachSoft : p.ground, borderRadius: BorderRadius.circular(999)),
      alignment: Alignment.center,
      child: Text(text, style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: ask ? p.peachInk : p.ink)),
    );
  }
}

/// An agent's reach in plain words.
List<String> reachLabels(Agent a) => a.anyToolset ? ['Anything a task allows'] : [for (final t in a.toolsets) toolsetLabel(t)];

/// "Gmail · Calendar · 4 standing jobs" for a chat header.
String reachLine(Agent a, int jobs) => [
  for (final r in reachLabels(a)) r.split(' · ').first,
  if (jobs > 0) jobs == 1 ? '1 standing job' : '$jobs standing jobs',
].join(' · ');

AgentRef refOf(Agent a) => AgentRef(id: a.id, name: a.name, color: a.color.toJson(), badge: a.badge.toJson());

// ---- Standing jobs ---------------------------------------------------------

const _dayKeys = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'];
const _dayNames = ['Mondays', 'Tuesdays', 'Wednesdays', 'Thursdays', 'Fridays', 'Saturdays', 'Sundays'];

/// A schedule such as "weekdays 08:00" or "mon,fri 16:00": its days
/// (1 = Monday) and minute of the day. Null when it isn't one.
({Set<int> days, int minute})? parseSchedule(String schedule) {
  final parts = schedule.trim().split(RegExp(r'\s+'));
  if (parts.isEmpty) return null;
  final m = RegExp(r'^(\d{1,2}):(\d{2})$').firstMatch(parts.last);
  if (m == null) return null;
  final days = <int>{};
  for (final d in parts.sublist(0, parts.length - 1).join(',').split(',')) {
    final k = d.trim().toLowerCase();
    if (k.isEmpty) continue;
    if (k == 'weekdays') {
      days.addAll([1, 2, 3, 4, 5]);
    } else if (_dayKeys.contains(k)) {
      days.add(_dayKeys.indexOf(k) + 1);
    }
  }
  return (days: days.isEmpty ? {1, 2, 3, 4, 5, 6, 7} : days, minute: int.parse(m[1]!) * 60 + int.parse(m[2]!));
}

/// The times a schedule runs from [from] up to [to].
List<DateTime> runsBetween(String schedule, DateTime from, DateTime to) {
  final s = parseSchedule(schedule);
  if (s == null) return const [];
  final out = <DateTime>[];
  for (var d = _day(from); !d.isAfter(to); d = DateTime(d.year, d.month, d.day + 1)) {
    final at = DateTime(d.year, d.month, d.day, s.minute ~/ 60, s.minute % 60);
    if (s.days.contains(at.weekday) && !at.isBefore(from) && !at.isAfter(to)) out.add(at);
  }
  return out;
}

DateTime? nextRun(String schedule, DateTime now) => runsBetween(schedule, now, now.add(const Duration(days: 8))).firstOrNull;

/// "every weekday", "Fridays", "Mon, Thu", "every day".
String scheduleDays(String schedule) {
  final s = parseSchedule(schedule);
  if (s == null) return schedule;
  final d = s.days.toList()..sort();
  if (d.length == 7) return 'every day';
  if (d.length == 5 && d.last == 5) return 'every weekday';
  if (d.length == 1) return _dayNames[d.first - 1];
  return d.map((i) => _days[i - 1]).join(', ');
}

/// "Fridays at 16:00".
String scheduleWords(String schedule) {
  final s = parseSchedule(schedule);
  if (s == null) return schedule;
  final days = scheduleDays(schedule);
  return '${days[0].toUpperCase()}${days.substring(1)} at ${two(s.minute ~/ 60)}:${two(s.minute % 60)}';
}

/// A job's short name: its description when short, else its file name.
String jobTitle(Job j) {
  final d = j.description.trim().replaceAll(RegExp(r'\.$'), '');
  if (d.isNotEmpty && d.length <= 40 && !d.contains('\n')) return d;
  final n = j.name.replaceAll(RegExp(r'[-_]+'), ' ').trim();
  return n.isEmpty ? j.name : n[0].toUpperCase() + n.substring(1);
}

bool jobAsks(Job j) => j.steps.any((s) => s.kind == JobStepKind.ask);

/// The next scheduled run across [jobs], with its job.
(Job, DateTime)? nextJob(Iterable<Job> jobs, DateTime now) {
  (Job, DateTime)? best;
  for (final j in jobs) {
    final at = j.schedule.isEmpty ? null : nextRun(j.schedule, now);
    if (at != null && (best == null || at.isBefore(best.$2))) best = (j, at);
  }
  return best;
}

// ---- Who needs you -----------------------------------------------------------

/// The agent a waiting item belongs to, from its task or its chat.
AgentRef? agentOfItem(AttentionItem item, List<Agent> agents, List<ThreadSummary> threads) {
  for (final a in agents) {
    if (a.working.any((w) => w.ticketId == item.ticketId && item.ticketId != null)) return refOf(a);
  }
  return threads.where((t) => t.id == item.threadId).firstOrNull?.agent;
}

// ---- Opening a conversation --------------------------------------------------

/// Chats with an agent or a group open as a chat; anything else in the
/// older conversation view.
void openThread(BuildContext context, AppState state, ThreadSummary t) {
  final chat = t.agent != null || t.group != null;
  Navigator.of(context).push(MaterialPageRoute<void>(
    builder: (_) => chat ? ChatScreen(state: state, threadId: t.id, agent: t.agent, group: t.group) : ThreadScreen(state: state, threadId: t.id),
  ));
}

/// The chat with one agent, starting it if there is none yet.
void openAgentChat(BuildContext context, AppState state, AgentRef agent, {String? draft}) {
  final existing = state.threads.where((t) => t.group == null && t.agent?.id == agent.id).firstOrNull;
  Navigator.of(context).push(MaterialPageRoute<void>(
    builder: (_) => ChatScreen(state: state, threadId: existing?.id, agent: agent, draft: draft),
  ));
}
