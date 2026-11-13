import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'common.dart';
import 'theme.dart';

const _colors = ['violet', 'peach', 'mint', 'sky', 'rose', 'sun'];
const _badges = ['star', 'book', 'pen', 'mail', 'calendar', 'code', 'search', 'bolt'];

/// What a toolset lets an agent do, in plain words. Ones that reach other
/// people say they ask first.
String toolsetLabel(String t) {
  if (t.startsWith('mcp:')) return '${t.substring(4)} tools';
  return switch (t) {
    'research' => 'Web research',
    'writing' => 'Writes files',
    'mail' => 'Email · sends after your OK',
    'calendar' => 'Calendar · invites after your OK',
    'data' => 'Data',
    'code' => 'Code',
    _ => t,
  };
}

/// Adds or changes an agent.
class AgentEditScreen extends StatefulWidget {
  const AgentEditScreen({super.key, required this.state, this.agent});

  final AppState state;
  final Agent? agent;

  @override
  State<AgentEditScreen> createState() => _AgentEditScreenState();
}

class _AgentEditScreenState extends State<AgentEditScreen> {
  late final Agent? a = widget.agent;
  late final _name = TextEditingController(text: a?.name ?? '');
  late final _purpose = TextEditingController(text: a?.purpose ?? '');
  late final _instructions = TextEditingController(text: a?.instructions ?? '');
  late final _model = TextEditingController(text: a?.model ?? '');
  late final _base = TextEditingController(text: a?.baseUrl ?? '');
  final _key = TextEditingController();
  late String _color = a?.color.toJson() ?? 'sky';
  late String _badge = a?.badge.toJson() ?? 'book';
  late final Set<String> _toolsets = {...?a?.toolsets};
  late bool _any = a?.anyToolset ?? false;
  late final Set<String> _skills = {...?a?.skills};
  late final Set<String> _delegates = {...?a?.delegates};
  late final Set<String> _rules = {...?a?.rules};
  late int _capacity = a?.capacity ?? 1;
  late bool _enabled = a?.enabled ?? true;
  late bool _ownModel = a?.model != null;
  late String _provider = a?.provider ?? 'anthropic';
  bool _busy = false;
  String? _error;

  late final Future<(PolicyView, SkillList, JobList)> _options = () async {
    final api = widget.state.api;
    return (await api.getPolicy(), await api.listSkills(), await api.listJobs());
  }();

  Future<void> _save() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    final body = AgentRequest(
      name: _name.text.trim(),
      purpose: _purpose.text.trim(),
      instructions: _instructions.text.trim(),
      toolsets: _toolsets.toList(),
      anyToolset: _any,
      skills: _skills.toList(),
      provider: _ownModel ? AgentRequestProvider.fromJson(_provider) : null,
      model: _ownModel ? _model.text.trim() : null,
      baseUrl: _ownModel && _provider == 'openai' ? _base.text.trim() : null,
      key: _ownModel && _key.text.trim().isNotEmpty ? _key.text.trim() : null,
      capacity: _capacity,
      color: AgentRequestColor.fromJson(_color),
      badge: AgentRequestBadge.fromJson(_badge),
      enabled: _enabled,
      delegates: _delegates.toList(),
      rules: _rules.toList(),
    );
    try {
      final api = widget.state.api;
      if (a == null) {
        await api.createAgent(body: body);
      } else {
        await api.updateAgent(agentId: a!.id, body: body);
      }
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      if (mounted) setState(() => _error = describeError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _delete() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Remove ${a!.name}?'),
        content: const Text('Its memory stays on the server, but no task goes to it any more. An agent with open tasks can\'t be removed.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('Remove')),
        ],
      ),
    );
    if (ok != true) return;
    try {
      await widget.state.api.deleteAgent(agentId: a!.id);
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    }
  }

  Widget _section(String title, [String? hint]) {
    final p = Palette.of(context);
    return Padding(
      padding: const EdgeInsets.only(top: 22, bottom: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w900)),
          if (hint != null) Text(hint, style: TextStyle(fontSize: 13, color: p.muted)),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final builtin = a?.builtin ?? false;
    return Scaffold(
      appBar: AppBar(
        title: Text(a == null ? 'New agent' : a!.name),
        actions: [
          if (a != null && !builtin) IconButton(tooltip: 'Remove', onPressed: _busy ? null : _delete, icon: const Icon(Icons.delete_outline_rounded)),
        ],
      ),
      body: FutureBuilder<(PolicyView, SkillList, JobList)>(
        future: _options,
        builder: (context, snap) {
          if (snap.hasError) return ErrorPanel(message: describeError(snap.error!));
          if (!snap.hasData) return const Center(child: CircularProgressIndicator());
          final (pol, lib, jobList) = snap.data!;
          final jobs = [for (final j in jobList.jobs) if (j.agent == a?.id) j];
          final toolsets = {...pol.toolsets, ..._toolsets}.toList()..sort();
          return ListView(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 40),
            children: [
              Center(child: AgentAvatar(color: _color, badge: _badge, size: 84, seed: a?.id)),
              const SizedBox(height: 12),
              Wrap(
                alignment: WrapAlignment.center,
                spacing: 10,
                runSpacing: 8,
                children: [
                  for (final c in _colors)
                    GestureDetector(
                      key: Key('color:$c'),
                      onTap: () => setState(() => _color = c),
                      child: Container(
                        width: 30,
                        height: 30,
                        decoration: BoxDecoration(
                          color: agentColors(c, p).$1,
                          shape: BoxShape.circle,
                          border: Border.all(color: _color == c ? p.ink : Colors.transparent, width: 3),
                        ),
                      ),
                    ),
                ],
              ),
              const SizedBox(height: 10),
              Wrap(
                alignment: WrapAlignment.center,
                spacing: 0,
                children: [
                  for (final b in _badges)
                    IconButton(
                      key: Key('badge:$b'),
                      visualDensity: VisualDensity.compact,
                      isSelected: _badge == b,
                      onPressed: () => setState(() => _badge = b),
                      icon: Icon(agentBadgeIcon(b), color: _badge == b ? agentColors(_color, p).$1 : p.muted),
                    ),
                ],
              ),
              const SizedBox(height: 8),
              TextField(key: const Key('agent-name'), controller: _name, enabled: !builtin, decoration: const InputDecoration(labelText: 'Name', hintText: 'Researcher')),
              const SizedBox(height: 12),
              TextField(
                key: const Key('agent-purpose'),
                controller: _purpose,
                maxLength: 300,
                decoration: const InputDecoration(labelText: 'What is it for?', helperText: 'The router reads this to choose who does a task.'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _instructions,
                minLines: 3,
                maxLines: 8,
                maxLength: 4000,
                decoration: const InputDecoration(labelText: 'Instructions (optional)', hintText: 'Always cite two sources.'),
              ),
              _section('What it can use', 'A task gives it only what both this list and the task allow.'),
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                title: const Text('Anything a task allows'),
                value: _any,
                onChanged: (v) => setState(() => _any = v),
              ),
              if (!_any)
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    for (final t in toolsets)
                      FilterChip(
                        key: Key('toolset:$t'),
                        label: Text(toolsetLabel(t)),
                        selected: _toolsets.contains(t),
                        onSelected: (v) => setState(() => v ? _toolsets.add(t) : _toolsets.remove(t)),
                      ),
                  ],
                ),
              _section('Skills', lib.skills.isEmpty ? 'Add skills under Settings → Skills.' : 'Instructions it can open when a task needs them.'),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final s in lib.skills)
                    FilterChip(
                      label: Text(s.name),
                      tooltip: s.description,
                      selected: _skills.contains(s.name),
                      onSelected: (v) => setState(() => v ? _skills.add(s.name) : _skills.remove(s.name)),
                    ),
                ],
              ),
              if (a != null) ...[
                _section('Standing jobs', jobs.isEmpty ? "None. A job is a file in the server's jobs folder that names this agent." : 'Work it does on its own, from the files in the server\'s jobs folder.'),
                for (final j in jobs)
                  Padding(
                    key: Key('job:${j.name}'),
                    padding: const EdgeInsets.only(bottom: 8),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Icon(j.trigger.isEmpty ? Icons.schedule_rounded : Icons.bolt_rounded, size: 18, color: p.muted),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(j.description.isEmpty ? j.name : j.description, style: const TextStyle(fontWeight: FontWeight.w800)),
                              Text(
                                [
                                  if (j.schedule.isNotEmpty) j.schedule,
                                  if (j.trigger.isNotEmpty) 'when ${j.trigger}',
                                  '${j.steps.length} step${j.steps.length == 1 ? '' : 's'}',
                                  if (j.lastRunAt != null) 'last ran ${ago(j.lastRunAt!)}',
                                ].join(' · '),
                                style: TextStyle(fontSize: 13, color: p.muted),
                              ),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
              _section('Model'),
              SwitchListTile(
                key: const Key('own-model'),
                contentPadding: EdgeInsets.zero,
                title: const Text('Its own model'),
                subtitle: const Text("Off: it uses the worker's model from Settings → Models."),
                value: _ownModel,
                onChanged: (v) => setState(() => _ownModel = v),
              ),
              if (_ownModel) ...[
                SegmentedButton<String>(
                  segments: const [
                    ButtonSegment(value: 'gemini', label: Text('Gemini')),
                    ButtonSegment(value: 'anthropic', label: Text('Anthropic')),
                    ButtonSegment(value: 'openai', label: Text('Other')),
                  ],
                  selected: {_provider},
                  onSelectionChanged: (v) => setState(() => _provider = v.first),
                ),
                const SizedBox(height: 12),
                if (_provider == 'openai')
                  TextField(controller: _base, decoration: const InputDecoration(labelText: 'Endpoint address', hintText: 'https://api.deepseek.com')),
                TextField(controller: _model, decoration: const InputDecoration(labelText: 'Model', hintText: 'claude-sonnet-5-5')),
                TextField(
                  controller: _key,
                  obscureText: true,
                  decoration: const InputDecoration(labelText: 'API key (leave empty to use the saved one)'),
                ),
              ],
              _section('Tasks at once', 'More lets it work on several tasks in parallel, and costs more at once.'),
              Row(
                children: [
                  IconButton(onPressed: _capacity > 1 ? () => setState(() => _capacity--) : null, icon: const Icon(Icons.remove_circle_outline)),
                  Text('$_capacity', key: const Key('capacity'), style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w900)),
                  IconButton(onPressed: _capacity < 5 ? () => setState(() => _capacity++) : null, icon: const Icon(Icons.add_circle_outline)),
                ],
              ),
              _section('Who may give it work', 'Your own requests may go to any agent.'),
              if (pol.delegates.isEmpty && pol.rules.isEmpty) Text('No delegates or rules yet.', style: TextStyle(color: p.muted)),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final d in pol.delegates)
                    FilterChip(
                      avatar: const Icon(Icons.person_rounded, size: 18),
                      label: Text(d.name),
                      selected: _delegates.contains(d.name),
                      onSelected: (v) => setState(() => v ? _delegates.add(d.name) : _delegates.remove(d.name)),
                    ),
                  for (final r in pol.rules)
                    FilterChip(
                      avatar: const Icon(Icons.rule_rounded, size: 18),
                      label: Text('Rule: ${r.name}'),
                      selected: _rules.contains(r.name),
                      onSelected: (v) => setState(() => v ? _rules.add(r.name) : _rules.remove(r.name)),
                    ),
                ],
              ),
              if (!builtin)
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('On'),
                  subtitle: const Text('Off: no new tasks go to it.'),
                  value: _enabled,
                  onChanged: (v) => setState(() => _enabled = v),
                ),
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.only(top: 12),
                  child: Text(_error!, key: const Key('agent-error'), style: TextStyle(color: Theme.of(context).colorScheme.error, fontWeight: FontWeight.w700)),
                ),
              const SizedBox(height: 16),
              FilledButton(
                key: const Key('save-agent'),
                onPressed: _busy ? null : _save,
                child: Text(_busy ? (_ownModel ? 'Trying the model…' : 'Saving…') : 'Save'),
              ),
            ],
          );
        },
      ),
    );
  }
}
