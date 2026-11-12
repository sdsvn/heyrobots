import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'common.dart';

/// Your machines: the servers that share one team. Another machine joins
/// with the code this one shows, or this one joins another's.
class MachinesScreen extends StatelessWidget {
  const MachinesScreen({super.key, required this.state});

  final AppState state;

  Future<void> _showCode(BuildContext context) async {
    try {
      final c = await state.api.createMachineCode();
      if (!context.mounted) return;
      await showDialog<void>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Add a machine'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('On the other machine, choose "Join a machine" and type:'),
              const SizedBox(height: 12),
              if (c.address != null) SelectableText('Address  ${c.address}', key: const Key('machine:address')),
              SelectableText('Code  ${c.code}', key: const Key('machine:code'), style: const TextStyle(fontSize: 20, fontWeight: FontWeight.w900, letterSpacing: 2)),
              const SizedBox(height: 8),
              Text('Works once, for ten minutes.', style: Theme.of(context).textTheme.bodySmall),
            ],
          ),
          actions: [FilledButton(onPressed: () => Navigator.pop(context), child: const Text('Done'))],
        ),
      );
    } catch (e) {
      if (context.mounted) showMessage(context, describeError(e));
    }
  }

  Future<void> _join(BuildContext context, VoidCallback reload) async {
    final addr = TextEditingController(), code = TextEditingController();
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Join a machine'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(key: const Key('join:address'), controller: addr, decoration: const InputDecoration(labelText: 'Address', hintText: 'host:port')),
            TextField(key: const Key('join:code'), controller: code, decoration: const InputDecoration(labelText: 'Code')),
          ],
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('Join')),
        ],
      ),
    );
    if (ok != true) return;
    try {
      final m = await state.api.joinMachine(body: JoinMachineRequest(address: addr.text.trim(), code: code.text.trim()));
      if (context.mounted) showMessage(context, 'Joined ${m.name}. Its agents are on your team now.');
      reload();
    } catch (e) {
      if (context.mounted) showMessage(context, describeError(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Your machines')),
      body: Loaded<MachineList>(
        load: () => state.api.listMachines(),
        builder: (context, list, reload) => ListView(
          children: [
            for (final m in list.machines)
              ListTile(
                key: Key('machine:${m.name}'),
                leading: Icon(Icons.circle, size: 12, color: m.online ? Colors.green : Theme.of(context).disabledColor),
                title: Text(m.self ? '${m.name} (this one)' : m.name),
                subtitle: Text([
                  '${m.agents} agent${m.agents == 1 ? '' : 's'}',
                  if (!m.self) m.online ? 'Available' : m.lastSeen == null ? 'Unavailable' : 'Unavailable since ${ago(m.lastSeen!)}',
                ].join(' · ')),
                trailing: m.self
                    ? null
                    : IconButton(
                        tooltip: 'Remove',
                        icon: const Icon(Icons.delete_outline),
                        onPressed: () async {
                          try {
                            await state.api.removeMachine(machineId: m.id);
                            reload();
                          } catch (e) {
                            if (context.mounted) showMessage(context, describeError(e));
                          }
                        },
                      ),
              ),
            const Divider(height: 32),
            ListTile(
              key: const Key('machines:add'),
              leading: const Icon(Icons.qr_code_2),
              title: const Text('Add a machine'),
              subtitle: const Text('Show a code another machine types'),
              onTap: () => _showCode(context),
            ),
            ListTile(
              key: const Key('machines:join'),
              leading: const Icon(Icons.link),
              title: const Text('Join a machine'),
              subtitle: const Text("Type the code another machine shows"),
              onTap: () => _join(context, reload),
            ),
          ],
        ),
      ),
    );
  }
}
