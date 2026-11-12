import 'dart:async';

import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'chat_kit.dart';
import 'chat_screen.dart';
import 'common.dart';
import 'theme.dart';

/// Makes a group of agents to chat with.
class NewGroupScreen extends StatefulWidget {
  const NewGroupScreen({super.key, required this.state});

  final AppState state;

  @override
  State<NewGroupScreen> createState() => _NewGroupScreenState();
}

class _NewGroupScreenState extends State<NewGroupScreen> {
  final _name = TextEditingController();
  final _members = <String>[];
  bool _saving = false;

  AppState get s => widget.state;

  @override
  void initState() {
    super.initState();
    _name.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  Future<void> _create(List<Agent> agents) async {
    setState(() => _saving = true);
    try {
      final g = await s.api.createGroup(body: GroupRequest(name: _name.text.trim(), members: _members));
      if (!mounted) return;
      unawaited(s.refresh());
      Navigator.of(context).pushReplacement(MaterialPageRoute<void>(builder: (_) => ChatScreen(state: s, group: g, threadId: g.threadId)));
    } catch (e) {
      if (mounted) {
        showMessage(context, describeError(e));
        setState(() => _saving = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Scaffold(
      backgroundColor: p.ground,
      body: SafeArea(
        child: Loaded<AgentList>(
          load: () => s.api.listAgents(),
          builder: (context, list, _) {
            final agents = list.agents.where((a) => a.enabled).toList();
            final picked = [for (final id in _members) ...agents.where((a) => a.id == id).map(refOf)];
            final ready = _name.text.trim().isNotEmpty && _members.isNotEmpty && !_saving;
            final field = OutlineInputBorder(borderRadius: BorderRadius.circular(14), borderSide: BorderSide(color: p.line, width: 2));
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(14, 16, 14, 12),
                  child: Row(
                    children: [
                      SizedBox(
                        width: 40,
                        height: 44,
                        child: IconButton(
                          padding: EdgeInsets.zero,
                          tooltip: 'Cancel',
                          icon: Icon(Icons.close_rounded, size: 26, color: p.ink),
                          onPressed: () => Navigator.of(context).maybePop(),
                        ),
                      ),
                      const SizedBox(width: 10),
                      const Expanded(child: Text('New group', style: TextStyle(fontSize: 20, fontWeight: FontWeight.w900))),
                    ],
                  ),
                ),
                Expanded(
                  child: ListView(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    children: [
                      Container(
                        padding: const EdgeInsets.all(16),
                        decoration: edgeBox(p),
                        child: Row(
                          children: [
                            GroupAvatar(members: picked, width: 60, height: 56, face: 40),
                            const SizedBox(width: 14),
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text('Group name', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: p.muted)),
                                  const SizedBox(height: 4),
                                  TextField(
                                    key: const Key('group-name'),
                                    controller: _name,
                                    maxLength: 60,
                                    style: TextStyle(fontSize: 16, fontWeight: FontWeight.w800, color: p.ink),
                                    decoration: InputDecoration(
                                      hintText: 'Trip planning',
                                      counterText: '',
                                      isDense: true,
                                      filled: true,
                                      fillColor: p.ground,
                                      contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 11),
                                      border: field,
                                      enabledBorder: field,
                                      focusedBorder: field.copyWith(borderSide: BorderSide(color: p.accent, width: 2)),
                                    ),
                                  ),
                                ],
                              ),
                            ),
                          ],
                        ),
                      ),
                      const SizedBox(height: 18),
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 4),
                        child: SectionLabel('Members · ${_members.length}', size: 13, weight: FontWeight.w800),
                      ),
                      const SizedBox(height: 14),
                      Container(
                        decoration: edgeBox(p),
                        clipBehavior: Clip.antiAlias,
                        child: Material(
                          type: MaterialType.transparency,
                          child: Column(
                            children: [
                              for (var i = 0; i < agents.length; i++) _member(p, agents[i], i == agents.length - 1),
                            ],
                          ),
                        ),
                      ),
                      const SizedBox(height: 14),
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                        decoration: BoxDecoration(color: p.peachSoft, borderRadius: BorderRadius.circular(20)),
                        child: Row(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Icon(Icons.shield_outlined, size: 20, color: p.peachInk),
                            const SizedBox(width: 12),
                            Expanded(
                              child: Text(
                                'Messages here go to one of these members, who can pass work to the others. A group adds no powers: each still uses only its own tools, and anything that reaches other people waits for your OK.',
                                style: TextStyle(fontSize: 13, height: 1.45, color: p.peachInk),
                              ),
                            ),
                          ],
                        ),
                      ),
                      const SizedBox(height: 12),
                    ],
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
                  child: DecoratedBox(
                    decoration: BoxDecoration(
                      borderRadius: BorderRadius.circular(999),
                      boxShadow: ready ? [BoxShadow(color: p.accentDeep, offset: const Offset(0, 4))] : null,
                    ),
                    child: FilledButton(
                      key: const Key('create-group'),
                      style: FilledButton.styleFrom(
                        minimumSize: const Size.fromHeight(54),
                        shape: const StadiumBorder(),
                        textStyle: const TextStyle(fontFamily: fontFamily, fontSize: 16, fontWeight: FontWeight.w900),
                      ),
                      onPressed: ready ? () => _create(agents) : null,
                      child: Text(_saving ? 'Creating…' : 'Create group'),
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

  Widget _member(Palette p, Agent a, bool last) {
    final on = _members.contains(a.id);
    void toggle() => setState(() => on ? _members.remove(a.id) : _members.add(a.id));
    return InkWell(
      key: Key('member:${a.name}'),
      onTap: toggle,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        decoration: BoxDecoration(border: last ? null : Border(bottom: BorderSide(color: rowLine(p)))),
        child: Row(
          children: [
            AgentAvatar(color: a.color.toJson(), badge: a.badge.toJson(), size: 44, showBadge: false, name: a.name),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(a.name, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w900)),
                  if (a.purpose.isNotEmpty) Text(a.purpose, style: TextStyle(fontSize: 13, color: p.muted)),
                ],
              ),
            ),
            Checkbox(
              value: on,
              onChanged: (_) => toggle(),
              activeColor: p.accent,
              side: BorderSide(color: p.line, width: 2),
              shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(6)),
            ),
          ],
        ),
      ),
    );
  }
}
