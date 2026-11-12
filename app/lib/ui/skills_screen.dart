import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'common.dart';
import 'icons.dart';
import 'theme.dart';
import 'chat_screen.dart';

/// Skills: what is installed, and a few picked from the catalogue.
class SkillsScreen extends StatefulWidget {
  const SkillsScreen({super.key, required this.state});

  final AppState state;

  @override
  State<SkillsScreen> createState() => _SkillsScreenState();
}

class _SkillsScreenState extends State<SkillsScreen> {
  final _search = TextEditingController();
  late Future<(SkillList, List<Agent>)> _installed = _loadInstalled();
  late Future<SkillCatalog> _catalog = widget.state.api.searchSkills();
  final _adding = <String>{};

  Future<(SkillList, List<Agent>)> _loadInstalled() async {
    final api = widget.state.api;
    final skills = await api.listSkills();
    // Agents only color the rings; the list shows without them.
    final agents = await api.listAgents().then((l) => l.agents, onError: (_) => <Agent>[]);
    return (skills, agents);
  }

  void _find(String q) {
    final t = q.trim();
    setState(() {
      _catalog = widget.state.api.searchSkills(q: t.isEmpty ? null : t);
    });
  }

  Future<void> _add(CatalogSkill s) async {
    setState(() => _adding.add(s.source));
    try {
      final added = await widget.state.api.addSkill(body: AddSkillRequest(source: s.source));
      if (mounted) showMessage(context, 'Added ${added.name}.');
      setState(() {
        _installed = _loadInstalled();
        _catalog = widget.state.api.searchSkills(q: _search.text.trim().isEmpty ? null : _search.text.trim());
      });
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    } finally {
      if (mounted) setState(() => _adding.remove(s.source));
    }
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final q = _search.text.trim().toLowerCase();
    bool matches(String name, String description) => q.isEmpty || q.contains('/') || '$name $description'.toLowerCase().contains(q);
    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const ScreenHeader('Skills'),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 14),
              child: SearchBox(
                fieldKey: const Key('skill-search'),
                hint: 'Find a skill, or paste a GitHub link',
                controller: _search,
                onChanged: (_) => setState(() {}),
                onSubmitted: _find,
              ),
            ),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                children: [
                  FutureBuilder<(SkillList, List<Agent>)>(
                    future: _installed,
                    builder: (context, snap) {
                      if (snap.hasError) {
                        return ErrorPanel(
                          message: describeError(snap.error!),
                          onRetry: () => setState(() {
                            _installed = _loadInstalled();
                          }),
                        );
                      }
                      if (!snap.hasData) return const SizedBox(height: 80, child: Center(child: CircularProgressIndicator()));
                      final (list, agents) = snap.data!;
                      final skills = list.skills.where((s) => matches(s.name, s.description)).toList();
                      if (skills.isEmpty) return const SizedBox();
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          const CapsLabel('Installed'),
                          const SizedBox(height: 10),
                          RowsCard(
                            children: [
                              for (final s in skills)
                                _Item(key: Key('skill:${s.name}'), name: _title(s.name), detail: s.description, trailing: _rings(s.usedBy, agents, p)),
                            ],
                          ),
                          const SizedBox(height: 16),
                        ],
                      );
                    },
                  ),
                  FutureBuilder<SkillCatalog>(
                    future: _catalog,
                    builder: (context, snap) {
                      if (snap.hasError) return Text(describeError(snap.error!), style: TextStyle(fontSize: 13, color: p.danger));
                      if (!snap.hasData) return const SizedBox(height: 80, child: Center(child: CircularProgressIndicator()));
                      final picks = snap.data!.skills.where((s) => !s.installed && matches(s.name, s.description)).toList();
                      if (picks.isEmpty) return const SizedBox();
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          const CapsLabel('Picked for your team'),
                          const SizedBox(height: 10),
                          RowsCard(
                            children: [
                              for (final s in picks)
                                _Item(
                                  key: Key('catalog:${s.name}'),
                                  name: _title(s.name),
                                  detail: [_repo(s.source), if ((s.suggestedFor ?? '').isNotEmpty) 'for ${s.suggestedFor}'].join(' · '),
                                  trailing: _AddButton(busy: _adding.contains(s.source), onPressed: () => _add(s), key: Key('add:${s.name}')),
                                ),
                            ],
                          ),
                        ],
                      );
                    },
                  ),
                  Padding(
                    padding: const EdgeInsets.fromLTRB(6, 12, 6, 12),
                    child: Text(
                      'A skill is a folder with a SKILL.md, the same format Claude Code uses. Adding one shows what it does before any agent gets it.',
                      style: TextStyle(fontSize: 13, color: p.muted),
                    ),
                  ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 12, 20, 28),
              child: BigButton(
                primary: false,
                leading: Glyph.chat,
                label: "Or just tell Bit what you'd like it to learn",
                onPressed: () => Navigator.of(context).push(
                  MaterialPageRoute<void>(
                    builder: (_) => ChatScreen(state: widget.state, draft: 'Learn how to '),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _rings(List<String> usedBy, List<Agent> agents, Palette p) {
    final colors = [
      for (final name in usedBy)
        if (agents.where((a) => a.name == name).firstOrNull case final a?) agentColors(a.color.toJson(), p).$1 else p.accent,
    ];
    if (colors.isEmpty) return const SizedBox();
    return Semantics(
      label: 'Used by ${usedBy.join(' and ')}',
      child: Padding(
        padding: const EdgeInsets.only(left: 6),
        child: SizedBox(
          width: 20 + 14.0 * (colors.length - 1),
          height: 20,
          child: Stack(
            children: [
              for (final (i, c) in colors.indexed)
                Positioned(
                  left: 14.0 * i,
                  child: AgentRing(color: c),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

/// "trip-planner" reads as "Trip planner".
String _title(String name) {
  final s = name.replaceAll(RegExp('[-_]+'), ' ').trim();
  return s.isEmpty ? name : s[0].toUpperCase() + s.substring(1);
}

/// "anthropics/skills/release-notes" shows as "anthropics/skills".
String _repo(String source) => source.split('/').take(2).join('/');

/// An icon and tint that fit the skill's name.
(Glyph, String) _look(String name) {
  final n = name.toLowerCase();
  bool has(List<String> words) => words.any(n.contains);
  if (has(['trip', 'travel', 'train', 'flight'])) return (Glyph.train, 'sky');
  if (has(['code', 'review', 'go ', 'test', 'git'])) return (Glyph.code, 'mint');
  if (has(['mail', 'inbox', 'email'])) return (Glyph.mail, 'peach');
  if (has(['note', 'meeting', 'minutes'])) return (Glyph.note, 'sun');
  if (has(['gift', 'love', 'birthday'])) return (Glyph.heart, 'rose');
  return (Glyph.book, 'violet');
}

class _Item extends StatelessWidget {
  const _Item({super.key, required this.name, required this.detail, required this.trailing});

  final String name, detail;
  final Widget trailing;

  @override
  Widget build(BuildContext context) {
    final (g, tint) = _look(name);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      child: Row(
        children: [
          IconTile(g, tint: tint, size: 42, iconSize: 22),
          const SizedBox(width: 12),
          Expanded(child: TwoLines(name, detail)),
          const SizedBox(width: 12),
          trailing,
        ],
      ),
    );
  }
}

class _AddButton extends StatelessWidget {
  const _AddButton({super.key, required this.busy, required this.onPressed});

  final bool busy;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return EdgedBox(
      height: 34,
      radius: 999,
      depth: 2,
      color: p.accent,
      edge: p.accentDeep,
      padding: const EdgeInsets.symmetric(horizontal: 14),
      onTap: busy ? null : onPressed,
      child: Center(
        widthFactor: 1,
        child: busy
            ? const SizedBox.square(dimension: 14, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
            : const Text(
                'Add',
                style: TextStyle(fontSize: 13, fontWeight: FontWeight.w900, color: Colors.white),
              ),
      ),
    );
  }
}
