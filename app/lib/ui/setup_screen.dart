import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'bit.dart';
import 'common.dart';
import 'icons.dart';
import 'theme.dart';

/// "Pick my brain": who does the work, and who makes the quick calls. Shown
/// once after pairing, and again from Settings to change it.
class SetupScreen extends StatefulWidget {
  const SetupScreen({super.key, required this.state, this.onDone});

  final AppState state;

  /// Called after both choices passed their test. Without it the screen
  /// was pushed to change the brain, and pops instead.
  final VoidCallback? onDone;

  @override
  State<SetupScreen> createState() => _SetupScreenState();
}

/// A brain with what the person typed for it in the key sheet.
typedef _Pick = ({Brain brain, String? key, String? baseUrl, String? model});

class _SetupScreenState extends State<SetupScreen> {
  late Future<BrainList> _brains = widget.state.api.listBrains();
  _Pick? _work, _quick;
  bool _busy = false;
  String? _error;

  void _init(BrainList b) {
    _Pick? first(List<Brain> list) {
      final chosen = list.where((x) => x.available && x.selected).firstOrNull ?? list.where((x) => x.available && !x.needsKey).firstOrNull;
      return chosen == null ? null : (brain: chosen, key: null, baseUrl: null, model: null);
    }

    _work ??= first(b.work);
    _quick ??= first(b.quick);
  }

  /// OpenAI-compatible endpoints need an address and a model name too.
  static bool _isCompatible(Brain b) => b.id.contains('compat') || b.id == 'custom' || (b.provider.toJson() == 'openai' && (b.baseUrl ?? '').isEmpty);

  Future<void> _pick(Brain b, bool work) async {
    if (!b.available) return;
    _Pick pick = (brain: b, key: null, baseUrl: null, model: null);
    if (b.needsKey) {
      final compat = _isCompatible(b);
      final v = await askInSheet(
        context,
        title: 'Key for ${b.name}',
        body: 'It goes to the vault on your server and never comes back to this phone.',
        fields: [
          (label: 'Key', secret: true, initial: null, key: const Key('brain-key')),
          if (compat) (label: 'Address', secret: false, initial: b.baseUrl, key: const Key('brain-url')),
          if (compat) (label: 'Model', secret: false, initial: b.model, key: const Key('brain-model')),
        ],
      );
      if (v == null || v.first.isEmpty) return;
      pick = (brain: b, key: v[0], baseUrl: compat ? v[1] : null, model: compat ? v[2] : null);
    }
    setState(() {
      _error = null;
      if (work) {
        _work = pick;
      } else {
        _quick = pick;
      }
    });
  }

  ModelRoleUpdate _update(_Pick p) => ModelRoleUpdate(
    provider: ModelRoleUpdateProvider.fromJson(p.brain.provider.toJson()),
    model: (p.model ?? '').isNotEmpty ? p.model! : p.brain.model,
    baseUrl: (p.baseUrl ?? '').isNotEmpty ? p.baseUrl : p.brain.baseUrl,
    key: p.key,
  );

  Future<void> _go() async {
    final work = _work, quick = _quick;
    if (work == null || quick == null) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final api = widget.state.api;
      await api.setModel(role: SetModelRole.worker, body: _update(work));
      await api.setModel(role: SetModelRole.router, body: _update(quick));
      await api.setModel(role: SetModelRole.reader, body: _update(quick));
    } catch (e) {
      if (mounted) {
        setState(() {
          _busy = false;
          _error = describeError(e);
        });
      }
      return;
    }
    if (!mounted) return;
    setState(() => _busy = false);
    if (widget.onDone != null) {
      widget.onDone!();
    } else {
      Navigator.of(context).pop(true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Scaffold(
      body: SafeArea(
        child: FutureBuilder<BrainList>(
          future: _brains,
          builder: (context, snap) {
            if (snap.hasError) {
              return ErrorPanel(
                message: describeError(snap.error!),
                onRetry: () => setState(() {
                  _brains = widget.state.api.listBrains();
                }),
              );
            }
            if (!snap.hasData) return const Center(child: CircularProgressIndicator());
            final b = snap.data!;
            _init(b);
            final muted = TextStyle(fontSize: 13, color: p.muted);
            return Column(
              children: [
                Expanded(
                  child: ListView(
                    padding: EdgeInsets.zero,
                    children: [
                      Stack(
                        children: [
                          Padding(
                            padding: const EdgeInsets.fromLTRB(24, 44, 24, 12),
                            child: Column(
                              children: [
                                const Bit(size: 84),
                                const SizedBox(height: 14),
                                const Text('Pick my brain', style: TextStyle(fontSize: 26, fontWeight: FontWeight.w900, letterSpacing: -0.4)),
                                const SizedBox(height: 6),
                                Text(
                                  'Who does the work, and who makes the quick calls. Both can change later by asking me.',
                                  textAlign: TextAlign.center,
                                  style: TextStyle(fontSize: 14, color: p.muted),
                                ),
                              ],
                            ),
                          ),
                          if (widget.onDone == null) const Positioned(left: 20, top: 20, child: BackTile()),
                        ],
                      ),
                      const SizedBox(height: 14),
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 20),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            for (final x in b.work) ...[
                              _BrainCard(brain: x, glyph: _glyph(x, quick: false), selected: _work?.brain.id == x.id, onTap: () => _pick(x, true)),
                              const SizedBox(height: 10),
                            ],
                            const CapsLabel('Quick calls', padding: EdgeInsets.fromLTRB(4, 10, 4, 0)),
                            const SizedBox(height: 10),
                            Padding(
                              padding: const EdgeInsets.symmetric(horizontal: 4),
                              child: Text(
                                'Sorting messages and reading outside text happen many times a day, so they go to a small, fast model.',
                                style: muted,
                              ),
                            ),
                            for (final x in b.quick) ...[
                              const SizedBox(height: 10),
                              _BrainCard(brain: x, glyph: _glyph(x, quick: true), selected: _quick?.brain.id == x.id, onTap: () => _pick(x, false)),
                            ],
                            const SizedBox(height: 14),
                            Padding(
                              padding: const EdgeInsets.symmetric(horizontal: 4),
                              child: Text('I try both once before we go on.', style: muted),
                            ),
                            if (_error != null)
                              Padding(
                                padding: const EdgeInsets.fromLTRB(4, 8, 4, 0),
                                child: Text(
                                  _error!,
                                  key: const Key('setup-error'),
                                  style: TextStyle(fontSize: 13, fontWeight: FontWeight.w700, color: p.danger),
                                ),
                              ),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.fromLTRB(20, 16, 20, 28),
                  child: BigButton(
                    key: const Key('go'),
                    label: "Let's go",
                    trailing: Glyph.arrow,
                    busy: _busy,
                    onPressed: _work == null || _quick == null ? null : _go,
                  ),
                ),
              ],
            );
          },
        ),
      ),
    );
  }
}

/// Claude Code is a terminal; a model on this machine is a chip, or a bolt
/// for quick calls; a provider's site is a clock face.
(Glyph, String) _glyph(Brain b, {required bool quick}) {
  final local = (b.baseUrl ?? '').contains('localhost') || (b.baseUrl ?? '').contains('127.0.0.1');
  if (b.provider.toJson() == 'claude_code') return (Glyph.terminal, 'peach');
  if (quick) return (Glyph.bolt, local ? 'mint' : 'sky');
  return local ? (Glyph.chip, 'mint') : (Glyph.cloud, 'sky');
}

class _BrainCard extends StatelessWidget {
  const _BrainCard({required this.brain, required this.glyph, required this.selected, required this.onTap});

  final Brain brain;
  final (Glyph, String) glyph;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final (g, tint) = glyph;
    return Opacity(
      opacity: brain.available ? 1 : .5,
      child: Semantics(
        selected: selected,
        enabled: brain.available,
        child: EdgedBox(
          key: Key('brain:${brain.id}'),
          radius: 20,
          color: selected ? p.track : p.card,
          border: Border.all(color: selected ? p.accent : Colors.transparent, width: 2.5),
          padding: const EdgeInsets.all(14),
          onTap: brain.available ? onTap : null,
          child: Row(
            children: [
              IconTile(g, tint: tint),
              const SizedBox(width: 12),
              Expanded(child: TwoLines(brain.name, brain.detail, titleSize: 16)),
              const SizedBox(width: 12),
              if (selected) LineIcon(Glyph.check, size: 22, color: p.accent, stroke: 3) else LineIcon(Glyph.chevron, size: 20, color: p.muted, stroke: 2.5),
            ],
          ),
        ),
      ),
    );
  }
}
