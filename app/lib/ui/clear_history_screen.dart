import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'common.dart';
import 'theme.dart';

/// Start fresh: clear every chat and task, keeping the team and its setup.
class ClearHistoryScreen extends StatefulWidget {
  const ClearHistoryScreen({super.key, required this.state});

  final AppState state;

  @override
  State<ClearHistoryScreen> createState() => _ClearHistoryScreenState();
}

class _ClearHistoryScreenState extends State<ClearHistoryScreen> {
  bool _forget = false, _busy = false;
  ClearedHistory? _done;

  Future<void> _clear() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Clear the history?'),
        content: Text(
          'Every chat and task goes, with their files, and actions waiting to be sent are cancelled. '
          '${_forget ? 'The agents also forget what they learned. ' : ''}'
          'A backup is kept on the computer.',
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(key: const Key('clear:confirm'), onPressed: () => Navigator.pop(context, true), child: const Text('Clear history')),
        ],
      ),
    );
    if (ok != true) return;
    setState(() => _busy = true);
    try {
      final done = await widget.state.api.clearHistory(body: ClearHistoryRequest(forgetBrain: _forget));
      await widget.state.refresh();
      if (mounted) setState(() => _done = done);
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final done = _done;
    Widget line(IconData icon, Color color, String text) => Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 18, color: color),
          const SizedBox(width: 10),
          Expanded(child: Text(text, style: TextStyle(fontSize: 14, color: p.ink, height: 1.35))),
        ],
      ),
    );
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const ScreenHeader('Start fresh'),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
                children: [
                  if (done != null)
                    Container(
                      key: const Key('clear:done'),
                      decoration: edged(p.card, edge: p.line),
                      padding: const EdgeInsets.all(16),
                      child: TwoLines(
                        'History cleared',
                        'Removed ${done.chats} chat${done.chats == 1 ? '' : 's'} and ${done.tasks} task${done.tasks == 1 ? '' : 's'}'
                            '${done.actions > 0 ? ', and cancelled ${done.actions} waiting action${done.actions == 1 ? '' : 's'}' : ''}.'
                            '${done.forgotBrain ? ' The agents forgot what they learned.' : ''} A backup is kept on the computer.',
                        titleSize: 20,
                      ),
                    )
                  else ...[
                    Container(
                      decoration: edged(p.card, edge: p.line),
                      padding: const EdgeInsets.all(16),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          Text('Goes', style: TextStyle(fontWeight: FontWeight.w900, color: p.ink)),
                          const SizedBox(height: 8),
                          line(Icons.remove_circle_outline, p.danger, 'Every chat and task, with their files and history'),
                          line(Icons.remove_circle_outline, p.danger, 'Actions waiting for your OK or to be sent; they are cancelled, never sent later'),
                          line(Icons.remove_circle_outline, p.danger, 'Digests, notifications and how the specialists\' checked work went'),
                          const SizedBox(height: 8),
                          Text('Stays', style: TextStyle(fontWeight: FontWeight.w900, color: p.ink)),
                          const SizedBox(height: 8),
                          line(Icons.check_circle_outline, p.mintInk, 'Your agents, groups, add-ons, skills, rules, standing jobs and settings'),
                          line(Icons.check_circle_outline, p.mintInk, 'This month\'s spending, so the budget still counts'),
                          line(Icons.check_circle_outline, p.mintInk, 'Chats with an agent or a group stay in the list, empty'),
                          line(Icons.info_outline, p.muted, 'Other machines in your team keep their own history'),
                        ],
                      ),
                    ),
                    const SizedBox(height: 12),
                    CheckboxListTile(
                      key: const Key('clear:forget'),
                      value: _forget,
                      onChanged: _busy ? null : (v) => setState(() => _forget = v ?? false),
                      title: const Text('Also forget what the agents learned'),
                      subtitle: const Text('Facts about you and your work, and their diaries. Off by default.'),
                      controlAffinity: ListTileControlAffinity.leading,
                      contentPadding: EdgeInsets.zero,
                    ),
                    const SizedBox(height: 12),
                    BigButton(key: const Key('clear:start'), label: 'Clear history', busy: _busy, onPressed: _busy ? null : _clear),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
