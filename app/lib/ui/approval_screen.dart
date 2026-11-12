import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'bit.dart';
import 'common.dart';
import 'theme.dart';

/// Shows one action waiting for approval, with exactly what would be sent.
class ApprovalScreen extends StatefulWidget {
  const ApprovalScreen({super.key, required this.state, required this.approvalId});

  final AppState state;
  final String approvalId;

  @override
  State<ApprovalScreen> createState() => _ApprovalScreenState();
}

class _ApprovalScreenState extends State<ApprovalScreen> {
  bool _busy = false;

  Future<Approval?> _load() async {
    final list = await widget.state.api.listApprovals();
    for (final a in list.approvals) {
      if (a.id == widget.approvalId) return a;
    }
    return null;
  }

  Future<void> _decide(Approval a, bool approve) async {
    String? note;
    if (!approve) {
      note = await _askNote();
      if (note == null) return;
    }
    setState(() => _busy = true);
    final err = await widget.state.decide(a, approve: approve, note: note);
    if (!mounted) return;
    setState(() => _busy = false);
    if (err != null) {
      showMessage(context, err);
      return;
    }
    showMessage(context, approve ? 'Approved. It will be checked again just before sending.' : 'Declined.');
    Navigator.of(context).pop();
  }

  Future<String?> _askNote() {
    final c = TextEditingController();
    return showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Decline this action?'),
        content: TextField(
          controller: c,
          autofocus: true,
          maxLines: 3,
          maxLength: 2000,
          decoration: const InputDecoration(labelText: 'What should happen instead? (optional)'),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(context, c.text), child: const Text('Decline')),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 6),
              child: DecoratedBox(
                decoration: BoxDecoration(borderRadius: BorderRadius.circular(14), boxShadow: [BoxShadow(color: p.line, offset: const Offset(0, 2))]),
                child: Material(
                  color: p.card,
                  borderRadius: BorderRadius.circular(14),
                  child: InkWell(
                    borderRadius: BorderRadius.circular(14),
                    onTap: () => Navigator.of(context).maybePop(),
                    child: SizedBox(width: 44, height: 44, child: Icon(Icons.chevron_left_rounded, size: 28, color: p.ink, semanticLabel: 'Back')),
                  ),
                ),
              ),
            ),
            Expanded(
              child: Loaded<Approval?>(
                load: _load,
                builder: (context, a, reload) {
                  if (a == null) {
                    return const Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [Bit(mood: BitMood.done, size: 96), SizedBox(height: 12), Text('Nothing to approve here any more.')],
                      ),
                    );
                  }
                  return _details(context, a);
                },
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _details(BuildContext context, Approval a) {
    final p = Palette.of(context);
    final strong = needsBiometrics(a.reach);
    final recipients = a.recipients ?? const <String>[];
    Widget row(String label, Widget value) => Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          SizedBox(width: 64, child: Text(label, style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: p.muted))),
          Expanded(child: value),
        ],
      ),
    );
    Widget chips(List<String> values) => Wrap(
      spacing: 6,
      runSpacing: 6,
      children: [for (final v in values) Pill(v, background: p.peachSoft, foreground: const Color(0xFF8A3A12))],
    );
    final who = [
      if (a.scope != null) 'For “${a.scope}”',
      if (a.requestedBy != null) 'asked by ${a.requestedBy == 'the owner' ? 'you' : a.requestedBy}',
      'through ${a.source}',
    ].join(' · ');
    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
            children: [
              Row(
                children: [
                  const Bit(mood: BitMood.needsYou, size: 64),
                  const SizedBox(width: 14),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(a.source == 'mcp' ? 'Run ${actionName(a.action)}?' : 'Send this ${actionName(a.action)}?', style: const TextStyle(fontSize: 24, fontWeight: FontWeight.w900, letterSpacing: -0.3)),
                        Text(who, style: TextStyle(fontSize: 13, color: p.muted)),
                      ],
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 14),
              SoftCard(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (recipients.isNotEmpty) row('To', chips(recipients)),
                    if (a.subject != null) row('Subject', Text(a.subject!, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w700))),
                    if ((a.dates ?? const []).isNotEmpty) row('When', chips(a.dates!)),
                    if ((a.amounts ?? const []).isNotEmpty) row('Amount', chips(a.amounts!)),
                    if ((a.links ?? const []).isNotEmpty) row('Links', chips(a.links!)),
                    Container(height: 2, margin: const EdgeInsets.only(bottom: 10), decoration: BoxDecoration(color: p.line.withValues(alpha: .6), borderRadius: BorderRadius.circular(2))),
                    SelectableText(a.body, style: const TextStyle(fontSize: 15, height: 1.5)),
                  ],
                ),
              ),
              if (a.findings.isNotEmpty) ...[
                const SizedBox(height: 14),
                SoftCard(
                  color: p.peachSoft,
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Icon(Icons.warning_amber_rounded, color: const Color(0xFF8A3A12)),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(a.findings.length == 1 ? 'Check this detail' : 'Check these details',
                                style: TextStyle(fontSize: 15, fontWeight: FontWeight.w800, color: p.peachInk)),
                            const SizedBox(height: 4),
                            for (final f in a.findings)
                              Text(_finding(f), style: TextStyle(fontSize: 13, height: 1.45, color: p.peachInk)),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ],
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(20, 8, 20, 24),
          child: Column(
            children: [
              if (strong) ...[
                _Fingerprint(key: const Key('approve'), onPressed: _busy ? null : () => _decide(a, true)),
                const SizedBox(height: 14),
                const Text('Touch to approve', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w800)),
              ] else
                SizedBox(
                  width: double.infinity,
                  child: FilledButton(key: const Key('approve'), onPressed: _busy ? null : () => _decide(a, true), child: const Text('Approve')),
                ),
              const SizedBox(height: 6),
              TextButton(
                key: const Key('reject'),
                onPressed: _busy ? null : () => _decide(a, false),
                child: const Text('Decline and tell Bit why', style: TextStyle(decoration: TextDecoration.underline, fontSize: 15)),
              ),
            ],
          ),
        ),
      ],
    );
  }

  /// "recipient bob@example.com: this recipient appears in no trusted
  /// source" reads as "bob@example.com doesn't come from you or anyone you
  /// trust."
  static String _finding(String f) {
    final m = RegExp(r'^\w+ (.+?): (.+)$').firstMatch(f);
    if (m == null) return f;
    if (m[2]!.contains('appears in no trusted source')) return "${m[1]} doesn't come from you or anyone you trust.";
    return '${m[1]}: ${m[2]}.';
  }
}

/// The round fingerprint button, with two rings pulsing out of it.
class _Fingerprint extends StatefulWidget {
  const _Fingerprint({super.key, required this.onPressed});

  final VoidCallback? onPressed;

  @override
  State<_Fingerprint> createState() => _FingerprintState();
}

class _FingerprintState extends State<_Fingerprint> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(milliseconds: 1800));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = !Bit.animate || MediaQuery.maybeDisableAnimationsOf(context) == true;
    if (still) {
      _c.stop();
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
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return SizedBox(
      width: 92,
      height: 92,
      child: Stack(
        clipBehavior: Clip.none,
        children: [
          for (final delay in const [0.0, 0.5])
            AnimatedBuilder(
              animation: _c,
              builder: (context, _) {
                final v = (_c.value + delay) % 1;
                return Positioned.fill(
                  child: Transform.scale(
                    scale: .9 + .6 * v,
                    child: DecoratedBox(
                      decoration: BoxDecoration(
                        shape: BoxShape.circle,
                        border: Border.all(color: const Color(0xFF8C80F5).withValues(alpha: _c.isAnimating ? .6 * (1 - v) : 0), width: 3),
                      ),
                    ),
                  ),
                );
              },
            ),
          Positioned.fill(
            child: DecoratedBox(
              decoration: BoxDecoration(shape: BoxShape.circle, boxShadow: [BoxShadow(color: p.accentDeep, offset: const Offset(0, 4))]),
              child: Material(
                color: p.accent,
                shape: const CircleBorder(),
                child: InkWell(
                  customBorder: const CircleBorder(),
                  onTap: widget.onPressed,
                  child: const Icon(Icons.fingerprint_rounded, color: Colors.white, size: 46, semanticLabel: 'Approve with your fingerprint'),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
