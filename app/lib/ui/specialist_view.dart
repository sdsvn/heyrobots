import 'package:flutter/material.dart';

import '../api/session.dart';
import 'chat_kit.dart';
import 'theme.dart';

/// A small pill that marks a specialist beside its name, so it doesn't
/// look like the generalist with another name.
class SpecialistPill extends StatelessWidget {
  const SpecialistPill({super.key});

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Container(
      key: const Key('specialist-pill'),
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(color: p.mintSoft, borderRadius: BorderRadius.circular(999)),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.verified_rounded, size: 12, color: p.mintInk),
          const SizedBox(width: 4),
          Text('Specialist', style: TextStyle(fontSize: 11, fontWeight: FontWeight.w900, color: p.mintInk)),
        ],
      ),
    );
  }
}

/// A name with the specialist pill after it, when the agent is one.
class AgentName extends StatelessWidget {
  const AgentName(this.agent, {super.key, this.size = 16, this.compact = false});

  final Agent agent;
  final double size;

  /// Shows only the specialist's mark when the card is too narrow for the
  /// pill.
  final bool compact;

  @override
  Widget build(BuildContext context) => LayoutBuilder(builder: (context, box) => _row(context, compact && box.maxWidth < 220));

  Widget _row(BuildContext context, bool small) => Row(
    children: [
      Flexible(
        child: Text(agent.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: TextStyle(fontSize: size, fontWeight: FontWeight.w900)),
      ),
      if (agent.specialist != null) ...[
        const SizedBox(width: 5),
        small
            ? Tooltip(
                key: const Key('specialist-pill'),
                message: 'Specialist',
                child: Icon(Icons.verified_rounded, size: size, color: Palette.of(context).mintInk),
              )
            : const SpecialistPill(),
      ],
    ],
  );
}

/// A tool's name in plain words: web_search → "Web search".
String toolLabel(String name) {
  final words = name.replaceAll('_', ' ');
  return words.isEmpty ? name : words[0].toUpperCase() + words.substring(1);
}

/// What makes an agent a specialist, as the server enforces it: what it is
/// for, the checks its work passes before you see it, the tools it is held
/// to, and how its checked work went.
class SpecialistSection extends StatelessWidget {
  const SpecialistSection({super.key, required this.specialist, required this.onExample});

  final Specialist specialist;

  /// Called with an example request, to try it in a chat.
  final ValueChanged<String> onExample;

  static const _kindIcon = {'shape': Icons.rule_rounded, 'tool': Icons.build_circle_outlined, 'rubric': Icons.fact_check_outlined};
  static const _kindWord = {'shape': 'in code', 'tool': 'by doing it again', 'rubric': 'by a second reader'};

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final s = specialist;
    final r = s.record;
    return Container(
      key: const Key('specialist'),
      decoration: edgeBox(p, radius: 20),
      padding: const EdgeInsets.all(14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              const SpecialistPill(),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  r.line,
                  key: const Key('specialist:record'),
                  style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: r.failed > 0 ? p.peachInk : p.muted),
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          const SectionLabel('Good at'),
          const SizedBox(height: 6),
          for (final sk in s.skills) ...[
            Text(sk.description, style: TextStyle(fontSize: 14, fontWeight: FontWeight.w800, color: p.ink)),
            const SizedBox(height: 4),
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                for (final ex in sk.examples)
                  Tooltip(
                    message: 'Try it in a chat',
                    child: InkWell(
                      borderRadius: BorderRadius.circular(999),
                      onTap: () => onExample(ex),
                      child: Container(
                        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
                        decoration: BoxDecoration(color: p.ground, borderRadius: BorderRadius.circular(999)),
                        child: Text('"$ex"', style: TextStyle(fontSize: 12, color: p.ink)),
                      ),
                    ),
                  ),
              ],
            ),
            const SizedBox(height: 10),
          ],
          if (s.notFor.isNotEmpty) ...[
            Text('Not for: ${s.notFor.join(', ')}', style: TextStyle(fontSize: 12, color: p.muted)),
            const SizedBox(height: 12),
          ],
          const SectionLabel('Its work is checked before you see it'),
          const SizedBox(height: 6),
          for (final c in s.checks)
            Padding(
              key: Key('specialist:check:${c.id}'),
              padding: const EdgeInsets.only(bottom: 6),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Icon(_kindIcon[c.kind.toJson()] ?? Icons.check_circle_outline, size: 18, color: p.mintInk),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text.rich(
                      TextSpan(
                        children: [
                          TextSpan(text: c.about, style: TextStyle(fontSize: 13, color: p.ink)),
                          TextSpan(text: '  ${_kindWord[c.kind.toJson()] ?? ''}', style: TextStyle(fontSize: 12, color: p.muted)),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            ),
          Text(
            'A report that fails is sent back with the reasons. After two tries it reaches you saying plainly what didn\'t pass.',
            style: TextStyle(fontSize: 12, color: p.muted),
          ),
          const SizedBox(height: 12),
          const SectionLabel('Its heyrobots tools'),
          const SizedBox(height: 6),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              for (final t in s.tools) ReachChip(toolLabel(t)),
              if (s.appTools) const ReachChip('Connected apps'),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            [
              'Never more than its task allows.',
              if (s.runtime.toJson() == 'claude-code') 'It works in your Claude Code when that\'s signed in here, where it also reads and edits files in its task folder.',
            ].join(' '),
            style: TextStyle(fontSize: 12, color: p.muted),
          ),
          if (s.output.isNotEmpty) ...[
            const SizedBox(height: 12),
            const SectionLabel('A finished answer'),
            const SizedBox(height: 6),
            Text(s.output, style: TextStyle(fontSize: 13, color: p.ink)),
          ],
        ],
      ),
    );
  }
}
