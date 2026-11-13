import 'package:flutter/material.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../api/session.dart';
import '../pairing.dart';
import '../state.dart';
import 'common.dart';
import 'connect_apps_screen.dart';
import 'icons.dart';
import 'machines_screen.dart';
import 'setup_screen.dart';
import 'skills_screen.dart';
import 'theme.dart';

/// Settings: how much the assistant does on its own, and six rows. Anything
/// else (digest time, quiet hours, budgets) is asked for in a chat.
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key, required this.state});

  final AppState state;

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  PolicyView? _policy;

  /// Each row's second line, filled in as it loads.
  final _lines = <String, String>{};

  @override
  void initState() {
    super.initState();
    if (widget.state.isOwner) _load();
  }

  void _load() {
    final api = widget.state.api;
    void line(String row, Future<String> f) => f.then((v) {
      if (mounted) setState(() => _lines[row] = v);
    }, onError: (_) {});
    api.getPolicy().then((p) {
      if (mounted) setState(() => _policy = p);
    }, onError: (_) {});
    line('brain', api.listBrains().then((b) {
      final work = b.work.where((x) => x.selected).firstOrNull?.name;
      final quick = b.quick.where((x) => x.selected).firstOrNull?.name;
      return [?work, if (quick != null) '$quick for quick reads'].join(' · ');
    }));
    line('apps', api.listApps().then((l) {
      final names = l.apps.where((a) => a.connected).map((a) => a.name).toList();
      final suggested = l.apps.where((a) => !a.connected && a.status.toJson() == 'found').length;
      final on = names.isEmpty ? 'Nothing connected yet' : names.take(4).join(', ');
      return suggested == 0 ? on : '$on · $suggested more suggested';
    }));
    line('skills', api.listSkills().then((l) => '${l.skills.length} installed'));
    line('people', api.listDevices().then((l) {
      final live = l.devices.where((d) => d.revokedAt == null).toList();
      final people = {for (final d in live) if (d.delegateName != null && d.delegateName!.isNotEmpty) d.delegateName!}.toList();
      return '${['You', ...people].join(' and ')} · ${live.length} device${live.length == 1 ? '' : 's'}';
    }));
    line('spending', api.getUsage().then((u) => '${usd(u.spentUsd)} this month · stops at \$${u.budgetUsd.toStringAsFixed(u.budgetUsd % 1 == 0 ? 0 : 2)}'));
  }

  Future<void> _setMode(AcceptMode mode) async {
    final p = _policy;
    if (p == null || p.mode == mode) return;
    setState(() => _policy = PolicyView(version: p.version, mode: mode, toolsets: p.toolsets, delegates: p.delegates, rules: p.rules, limits: p.limits));
    try {
      final saved = await widget.state.api.updatePolicy(body: updateFrom(p, mode: mode));
      if (mounted) setState(() => _policy = saved);
    } catch (e) {
      if (!mounted) return;
      setState(() => _policy = p);
      showMessage(context, describeError(e));
    }
  }

  Future<void> _open(Widget screen) async {
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => screen));
    if (mounted && widget.state.isOwner) _load();
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final s = widget.state;
    final owner = s.isOwner;
    final mode = _policy?.mode;
    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const ScreenHeader('Settings'),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.fromLTRB(16, 4, 16, 0),
                children: [
                  if (owner) ...[
                    Container(
                      decoration: edged(p.card, edge: p.line),
                      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          const TwoLines('How much I do on my own', 'Anything that reaches other people always waits for your OK.'),
                          const SizedBox(height: 10),
                          Container(
                            padding: const EdgeInsets.all(4),
                            decoration: BoxDecoration(color: p.track, borderRadius: BorderRadius.circular(999)),
                            child: Row(
                              children: [
                                for (final (i, (m, label)) in [(AcceptMode.off, 'Ask me'), (AcceptMode.observe, 'Suggest'), (AcceptMode.auto, 'Just do it')].indexed) ...[
                                  if (i > 0) const SizedBox(width: 4),
                                  Expanded(child: Segment(key: Key('mode:${m.toJson()}'), label: label, selected: mode == m, onTap: () => _setMode(m))),
                                ],
                              ],
                            ),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(height: 14),
                  ],
                  RowsCard(
                    children: [
                      if (owner) ...[
                        _Row(key: const Key('row:brain'), glyph: Glyph.sparkle, tint: 'peach', title: 'Brain', line: _lines['brain'], onTap: () => _open(SetupScreen(state: s))),
                        _Row(key: const Key('row:apps'), glyph: Glyph.puzzle, tint: 'sky', title: 'Add-ons', line: _lines['apps'], onTap: () => _open(ConnectAppsScreen(state: s))),
                        _Row(key: const Key('row:skills'), glyph: Glyph.book, tint: 'mint', title: 'Skills', line: _lines['skills'], onTap: () => _open(SkillsScreen(state: s))),
                      ],
                      _Row(key: const Key('row:people'), glyph: Glyph.team, tint: 'violet', title: 'People', line: owner ? _lines['people'] : s.me?.name, onTap: () => _open(DevicesScreen(state: s))),
                      if (owner) ...[
                        _Row(key: const Key('row:spending'), glyph: Glyph.clock, tint: 'sun', title: 'Spending', line: _lines['spending'], onTap: () => _open(UsageScreen(state: s))),
                        _Row(key: const Key('row:machines'), glyph: Glyph.chip, tint: 'mint', title: 'Your machines', line: 'One team across them', onTap: () => _open(MachinesScreen(state: s))),
                        _Row(
                          key: const Key('row:phone'),
                          glyph: Glyph.phone,
                          tint: 'rose',
                          title: 'Add a phone',
                          line: 'Show a code to scan',
                          onTap: () => _open(PairingCodeScreen(state: s, request: const PairingCodeRequest(role: Role.owner))),
                        ),
                      ],
                    ],
                  ),
                  const SizedBox(height: 14),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 6),
                    child: Text(
                      'Digest time, quiet hours, budgets: just tell me in a chat. "Send the digest at 7" works.',
                      style: TextStyle(fontSize: 13, color: p.muted),
                    ),
                  ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 12, 20, 28),
              child: Text('heyrobots 0.3 · this Mac · all data stays here', textAlign: TextAlign.center, style: TextStyle(fontSize: 12, color: p.muted)),
            ),
          ],
        ),
      ),
    );
  }
}


class _Row extends StatelessWidget {
  const _Row({super.key, required this.glyph, required this.tint, required this.title, required this.line, required this.onTap});

  final Glyph glyph;
  final String tint, title;
  final String? line;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        child: Row(
          children: [
            IconTile(glyph, tint: tint, size: 40, radius: 13, iconSize: 22),
            const SizedBox(width: 12),
            Expanded(child: TwoLines(title, line ?? ' ')),
            const SizedBox(width: 12),
            LineIcon(Glyph.chevron, size: 20, color: p.muted, stroke: 2.5),
          ],
        ),
      ),
    );
  }
}

/// People: the paired devices, inviting a delegate, unpairing this one.
class DevicesScreen extends StatelessWidget {
  const DevicesScreen({super.key, required this.state});

  final AppState state;

  void _open(BuildContext context, Widget screen) {
    Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => screen));
  }

  Future<String?> _askName(BuildContext context) {
    final c = TextEditingController();
    return showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Invite a delegate'),
        content: TextField(
          controller: c,
          autofocus: true,
          maxLength: 80,
          decoration: const InputDecoration(labelText: 'Their name'),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(context, c.text.trim().isEmpty ? null : c.text.trim()), child: const Text('Create code')),
        ],
      ),
    );
  }

  Future<void> _unpair(BuildContext context) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Unpair this device?'),
        content: const Text('It is removed from your server and forgets its token. You can pair it again with a new code.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('Unpair')),
        ],
      ),
    );
    if (ok != true) return;
    final id = state.me?.id;
    if (id != null && state.isOwner) {
      try {
        await state.api.revokeDevice(deviceId: id);
      } catch (_) {
        // Forget locally anyway; the owner can revoke it from another device.
      }
    }
    await state.forget();
    if (context.mounted) Navigator.of(context).popUntil((r) => r.isFirst);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('People')),
      body: Loaded<DeviceList>(
        load: () => state.api.listDevices(),
        builder: (context, list, reload) => ListView(
          children: [
            for (final d in list.devices)
              ListTile(
                leading: Icon(d.role == Role.owner ? Icons.smartphone : Icons.person_outline),
                title: Text(d.id == state.me?.id ? '${d.name} (this device)' : d.name),
                subtitle: Text([
                  d.role == Role.owner ? 'Owner' : 'Delegate ${d.delegateName ?? ''}',
                  if (d.platform != null) d.platform!,
                  if (d.revokedAt != null) 'Removed ${ago(d.revokedAt!)}' else if (d.lastSeenAt != null) 'Seen ${ago(d.lastSeenAt!)}',
                ].join(' · ')),
                trailing: d.revokedAt != null || d.id == state.me?.id
                    ? null
                    : IconButton(
                        tooltip: 'Remove',
                        icon: const Icon(Icons.delete_outline),
                        onPressed: () async {
                          try {
                            await state.api.revokeDevice(deviceId: d.id);
                            reload();
                          } catch (e) {
                            if (context.mounted) showMessage(context, describeError(e));
                          }
                        },
                      ),
              ),
            const Divider(height: 32),
            ListTile(
              leading: const Icon(Icons.person_add_alt),
              title: const Text('Invite someone'),
              subtitle: const Text('They may ask for work, but never approve'),
              onTap: () async {
                final name = await _askName(context);
                if (name != null && context.mounted) {
                  _open(context, PairingCodeScreen(state: state, request: PairingCodeRequest(role: Role.delegate, delegateName: name)));
                }
              },
            ),
            ListTile(
              leading: Icon(Icons.link_off, color: Theme.of(context).colorScheme.error),
              title: const Text('Unpair this device'),
              onTap: () => _unpair(context),
            ),
          ],
        ),
      ),
    );
  }
}


/// Shows a one-time code as a QR code for another device to scan.
class PairingCodeScreen extends StatelessWidget {
  const PairingCodeScreen({super.key, required this.state, required this.request});

  final AppState state;
  final PairingCodeRequest request;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final delegate = request.role == Role.delegate;
    return Scaffold(
      appBar: AppBar(title: Text(delegate ? 'Invite ${request.delegateName}' : 'Pair another device')),
      body: Loaded<PairingCode>(
        load: () => state.api.createPairingCode(body: request),
        builder: (context, code, reload) {
          final servers = [for (final u in code.servers ?? [code.serverUrl]) Uri.parse(u)];
          final payload = PairingPayload(servers.first, code.code, servers: servers);
          return ListView(
            padding: const EdgeInsets.all(24),
            children: [
              Text(
                delegate
                    ? 'Ask ${request.delegateName} to install heyrobots and scan this code. They can ask for work within the toolsets you allow them, but never approve actions.'
                    : 'Open heyrobots on the other device and scan this code.',
              ),
              const SizedBox(height: 16),
              Center(
                child: Container(
                  color: Colors.white,
                  padding: const EdgeInsets.all(12),
                  child: QrImageView(data: payload.encode(), size: 240, backgroundColor: Colors.white),
                ),
              ),
              const SizedBox(height: 16),
              Text('Or enter by hand:', style: theme.textTheme.labelLarge),
              SelectableText(code.code, style: theme.textTheme.headlineSmall?.copyWith(fontFamily: 'monospace')),
              for (final u in code.servers ?? [code.serverUrl]) SelectableText(u),
              const SizedBox(height: 8),
              Text('Works once, until ${clock(code.expiresAt)}.', style: theme.textTheme.bodySmall),
              const SizedBox(height: 8),
              Align(alignment: Alignment.centerLeft, child: TextButton(onPressed: reload, child: const Text('New code'))),
            ],
          );
        },
      ),
    );
  }
}


class UsageScreen extends StatefulWidget {
  const UsageScreen({super.key, required this.state});

  final AppState state;

  @override
  State<UsageScreen> createState() => _UsageScreenState();
}

class _UsageScreenState extends State<UsageScreen> {
  Future<(UsageView, PolicyView)> _load() async =>
      (await widget.state.api.getUsage(), await widget.state.api.getPolicy());

  Future<void> _editLimits(PolicyView p, VoidCallback reload) async {
    final l = p.limits;
    final budget = TextEditingController(text: (l?.monthlyBudgetUsd ?? 20).toStringAsFixed(2));
    final perHour = TextEditingController(text: '${l?.sendsPerHour ?? 10}');
    final perDay = TextEditingController(text: '${l?.sendsPerDay ?? 50}');
    final saved = await showDialog<Limits>(
      context: context,
      builder: (context) => Builder(
        builder: (context) => AlertDialog(
          title: const Text('Budget and limits'),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(controller: budget, keyboardType: const TextInputType.numberWithOptions(decimal: true), decoration: const InputDecoration(labelText: 'Monthly budget (USD)', prefixText: '\$')),
                TextField(controller: perHour, keyboardType: TextInputType.number, decoration: const InputDecoration(labelText: 'Sends to others per hour, per account')),
                TextField(controller: perDay, keyboardType: TextInputType.number, decoration: const InputDecoration(labelText: 'Sends to others per day, per account')),
              ],
            ),
          ),
          actions: [
            TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
            FilledButton(
              onPressed: () => Navigator.pop(
                context,
                Limits(
                  monthlyBudgetUsd: double.tryParse(budget.text.trim()),
                  sendsPerHour: int.tryParse(perHour.text.trim()),
                  sendsPerDay: int.tryParse(perDay.text.trim()),
                  // Digest time and time zone are set by asking in a chat.
                  digestHour: l?.digestHour,
                  timezone: l?.timezone,
                ),
              ),
              child: const Text('Save'),
            ),
          ],
        ),
      ),
    );
    if (saved == null) return;
    try {
      await widget.state.api.updatePolicy(body: updateFrom(p, limits: saved));
      reload();
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(title: const Text('Spending')),
      body: Loaded<(UsageView, PolicyView)>(
        load: _load,
        builder: (context, v, reload) {
          final (u, p) = v;
          final share = u.budgetUsd <= 0 ? 0.0 : (u.spentUsd / u.budgetUsd).clamp(0.0, 1.0).toDouble();
          final l = p.limits;
          return ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Text('${usd(u.spentUsd)} of ${usd(u.budgetUsd)} this month', style: theme.textTheme.titleLarge),
              const SizedBox(height: 8),
              LinearProgressIndicator(value: share, minHeight: 8),
              const SizedBox(height: 8),
              Text('A task that reaches the budget stops and asks you first.', style: theme.textTheme.bodySmall),
              const SizedBox(height: 16),
              ListTile(
                contentPadding: EdgeInsets.zero,
                title: const Text('Limits'),
                subtitle: Text(
                  'Up to ${l?.sendsPerHour ?? 10} sends an hour and ${l?.sendsPerDay ?? 50} a day to other people, per account.',
                ),
                trailing: TextButton(onPressed: () => _editLimits(p, reload), child: const Text('Change')),
              ),
              if (u.tickets.isNotEmpty) const SectionHeader('Spending by task'),
              for (final t in u.tickets)
                ListTile(dense: true, title: Text(t.scope, maxLines: 1, overflow: TextOverflow.ellipsis), trailing: Text(usd(t.spentUsd))),
            ],
          );
        },
      ),
    );
  }
}

/// A policy update that keeps everything but what is given.
PolicyUpdate updateFrom(PolicyView p, {AcceptMode? mode, List<RulePolicy>? rules, Limits? limits}) => PolicyUpdate(
  mode: mode ?? p.mode,
  toolsets: p.toolsets,
  delegates: p.delegates,
  rules: rules ?? p.rules,
  limits: limits ?? p.limits,
);
