import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../api/session.dart';
import '../pairing.dart';
import '../state.dart';
import 'bit.dart';
import 'theme.dart';

/// First screen on an unpaired device.
class PairScreen extends StatefulWidget {
  const PairScreen({super.key, required this.state});

  final AppState state;

  @override
  State<PairScreen> createState() => _PairScreenState();
}

class _PairScreenState extends State<PairScreen> {
  final _server = TextEditingController();
  final _code = TextEditingController();
  final _name = TextEditingController(text: _defaultName());
  bool _busy = false;
  String? _error;

  static String _defaultName() {
    if (kIsWeb) return 'Browser';
    return switch (defaultTargetPlatform) {
      TargetPlatform.iOS => 'iPhone',
      TargetPlatform.android => 'Android phone',
      TargetPlatform.macOS => 'Mac',
      TargetPlatform.windows => 'Windows PC',
      _ => 'Device',
    };
  }

  /// Cameras are scanned on phones; desktops enter the code by hand.
  bool get _canScan => !kIsWeb && (defaultTargetPlatform == TargetPlatform.iOS || defaultTargetPlatform == TargetPlatform.android);

  @override
  void initState() {
    super.initState();
    _error = widget.state.error;
    // In a browser the app comes from the server itself, and can only talk
    // to the address it was loaded from.
    if (kIsWeb && Uri.base.hasScheme && Uri.base.scheme.startsWith('http')) {
      _server.text = Uri.base.origin;
    }
  }

  @override
  void dispose() {
    _server.dispose();
    _code.dispose();
    _name.dispose();
    super.dispose();
  }

  Future<void> _pair(PairingPayload p) async {
    if (_name.text.trim().isEmpty) {
      setState(() => _error = 'Give this device a name.');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.state.pair(p, _name.text);
    } catch (e) {
      if (mounted) setState(() => _error = describeError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _pairByHand() {
    try {
      final server = parseServer(_server.text);
      if (_code.text.trim().isEmpty) throw const FormatException('Enter the pairing code.');
      _pair(PairingPayload(server, _code.text.trim().toUpperCase()));
    } on FormatException catch (e) {
      setState(() => _error = e.message);
    }
  }

  Future<void> _scan() async {
    final raw = await Navigator.of(context).push<String>(MaterialPageRoute(builder: (_) => const ScanScreen()));
    if (raw == null) return;
    try {
      await _pair(PairingPayload.parse(raw));
    } on FormatException catch (e) {
      setState(() => _error = e.message);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final p = Palette.of(context);
    InputDecoration field(String hint) => InputDecoration(
      hintText: hint,
      hintStyle: TextStyle(color: p.muted.withValues(alpha: .7)),
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 15),
    );
    TextStyle label() => const TextStyle(fontSize: 13, fontWeight: FontWeight.w800);
    return Scaffold(
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 440),
            child: Stack(
              children: [
                const Positioned(left: 20, top: 48, child: _Sparkle(size: 22, color: Color(0xFFFFB38A))),
                const Positioned(right: 28, top: 102, child: _Sparkle(size: 16, color: Color(0xFFB9AEFF), delay: 1.2)),
                ListView(
                  padding: const EdgeInsets.fromLTRB(24, 48, 24, 32),
                  children: [
                    const Center(child: Bit(mood: BitMood.waving, size: 150, shadow: true)),
                    const SizedBox(height: 20),
                    Text('Hi, I\'m Bit!', textAlign: TextAlign.center, style: theme.textTheme.headlineMedium?.copyWith(fontSize: 30)),
                    const SizedBox(height: 6),
                    Text(
                      "Let's meet your heyrobots server. Scan the code from its setup page, or from a device that's already paired.",
                      textAlign: TextAlign.center,
                      style: TextStyle(fontSize: 16, height: 1.45, color: p.muted),
                    ),
                    const SizedBox(height: 20),
                    if (_canScan) ...[
                      _ChunkyButton(onPressed: _busy ? null : _scan, icon: Icons.qr_code_scanner_rounded, label: 'Scan the QR code'),
                      const SizedBox(height: 20),
                      Row(
                        children: [
                          Expanded(child: Container(height: 2, decoration: BoxDecoration(color: p.line, borderRadius: BorderRadius.circular(2)))),
                          Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 12),
                            child: Text('or type it in', style: TextStyle(fontSize: 13, fontWeight: FontWeight.w700, color: p.muted)),
                          ),
                          Expanded(child: Container(height: 2, decoration: BoxDecoration(color: p.line, borderRadius: BorderRadius.circular(2)))),
                        ],
                      ),
                      const SizedBox(height: 20),
                    ],
                    Text('Server address', style: label()),
                    const SizedBox(height: 6),
                    TextField(
                      key: const Key('server'),
                      controller: _server,
                      keyboardType: TextInputType.url,
                      autocorrect: false,
                      style: const TextStyle(fontSize: 15),
                      decoration: field('https://my-mac.tailnet.ts.net:7340'),
                    ),
                    const SizedBox(height: 12),
                    Text('Pairing code', style: label()),
                    const SizedBox(height: 6),
                    TextField(
                      key: const Key('code'),
                      controller: _code,
                      autocorrect: false,
                      textCapitalization: TextCapitalization.characters,
                      style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w700, letterSpacing: 2),
                      decoration: field('XXXXX-XXXXX'),
                      onSubmitted: (_) => _pairByHand(),
                    ),
                    const SizedBox(height: 12),
                    if ((_error ?? widget.state.error) case final err?) ...[
                      Text(err, style: TextStyle(color: theme.colorScheme.error, fontWeight: FontWeight.w700)),
                      const SizedBox(height: 12),
                    ],
                    _canScan
                        ? OutlinedButton(
                            key: const Key('pair'),
                            onPressed: _busy ? null : _pairByHand,
                            child: _busy ? const _Spinner() : const Text('Pair'),
                          )
                        : _ChunkyButton(key: const Key('pair'), onPressed: _busy ? null : _pairByHand, label: 'Pair', busy: _busy),
                    const SizedBox(height: 28),
                    Text(
                      'Plain http only works on your own network. Everywhere else, use https.',
                      textAlign: TextAlign.center,
                      style: TextStyle(fontSize: 12, height: 1.5, color: p.muted),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _Spinner extends StatelessWidget {
  const _Spinner();

  @override
  Widget build(BuildContext context) => const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2));
}

/// The design's primary button: filled, rounded, with a darker bottom edge.
class _ChunkyButton extends StatelessWidget {
  const _ChunkyButton({super.key, required this.onPressed, required this.label, this.icon, this.busy = false});

  final VoidCallback? onPressed;
  final String label;
  final IconData? icon;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return DecoratedBox(
      decoration: BoxDecoration(borderRadius: BorderRadius.circular(20), boxShadow: [BoxShadow(color: p.accentDeep, offset: const Offset(0, 4))]),
      child: Material(
        color: onPressed == null ? p.accent.withValues(alpha: .6) : p.accent,
        borderRadius: BorderRadius.circular(20),
        child: InkWell(
          borderRadius: BorderRadius.circular(20),
          onTap: onPressed,
          child: SizedBox(
            height: 58,
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                if (busy)
                  const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2.5, color: Colors.white))
                else ...[
                  if (icon != null) ...[Icon(icon, color: Colors.white, size: 22), const SizedBox(width: 10)],
                  Text(label, style: const TextStyle(color: Colors.white, fontSize: 17, fontWeight: FontWeight.w800)),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// A four-pointed sparkle that floats and tilts.
class _Sparkle extends StatefulWidget {
  const _Sparkle({required this.size, required this.color, this.delay = 0});

  final double size;
  final Color color;
  final double delay;

  @override
  State<_Sparkle> createState() => _SparkleState();
}

class _SparkleState extends State<_Sparkle> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(milliseconds: 3400));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = !Bit.animate || MediaQuery.maybeDisableAnimationsOf(context) == true;
    if (still) {
      _c.stop();
    } else if (!_c.isAnimating) {
      _c.value = widget.delay / 3.4 % 1;
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
    return ExcludeSemantics(
      child: AnimatedBuilder(
        animation: _c,
        builder: (context, _) {
          final k = (1 - math.cos(_c.value * 2 * math.pi)) / 2;
          return Transform.translate(
            offset: Offset(0, -10 * k),
            child: Transform.rotate(
              angle: 8 * k * math.pi / 180,
              child: CustomPaint(size: Size.square(widget.size), painter: _SparklePainter(widget.color)),
            ),
          );
        },
      ),
    );
  }
}

class _SparklePainter extends CustomPainter {
  _SparklePainter(this.color);

  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final s = size.width / 24;
    final path = Path()
      ..moveTo(12 * s, 2 * s)
      ..lineTo(14 * s, 10 * s)
      ..lineTo(22 * s, 12 * s)
      ..lineTo(14 * s, 14 * s)
      ..lineTo(12 * s, 22 * s)
      ..lineTo(10 * s, 14 * s)
      ..lineTo(2 * s, 12 * s)
      ..lineTo(10 * s, 10 * s)
      ..close();
    canvas.drawPath(path, Paint()..color = color);
  }

  @override
  bool shouldRepaint(_SparklePainter old) => old.color != color;
}

/// Scans a pairing QR code and returns its text.
class ScanScreen extends StatefulWidget {
  const ScanScreen({super.key});

  @override
  State<ScanScreen> createState() => _ScanScreenState();
}

class _ScanScreenState extends State<ScanScreen> {
  bool _done = false;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Scan the pairing code')),
      body: MobileScanner(
        onDetect: (capture) {
          final raw = capture.barcodes.map((b) => b.rawValue).whereType<String>().firstOrNull;
          if (raw == null || _done) return;
          _done = true;
          Navigator.of(context).pop(raw);
        },
      ),
    );
  }
}
