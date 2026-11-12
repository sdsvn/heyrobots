import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

import 'theme.dart';

/// What Bit is showing.
enum BitMood { idle, waving, thinking, needsYou, done, sleeping }

/// Bit, the app's glyph: a small robot face that says what heyrobots is
/// doing. It is drawn on an 80×80 grid, the same geometry as the design.
class Bit extends StatefulWidget {
  const Bit({super.key, this.mood = BitMood.idle, this.size = 52, this.shadow = false});

  final BitMood mood;
  final double size;

  /// A soft shadow under Bit, for the large pairing Bit.
  final bool shadow;

  /// Turned off in tests, where an endless animation never settles.
  static bool animate = true;

  @override
  State<Bit> createState() => _BitState();
}

class _BitState extends State<Bit> with SingleTickerProviderStateMixin {
  late final Ticker _ticker = createTicker((d) => setState(() => _t = d.inMicroseconds / 1e6));
  double _t = 0;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = !Bit.animate || MediaQuery.maybeDisableAnimationsOf(context) == true;
    if (still && _ticker.isActive) {
      _ticker.stop();
      _t = 0;
    } else if (!still && !_ticker.isActive) {
      _ticker.start();
    }
  }

  @override
  void dispose() {
    _ticker.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return ExcludeSemantics(
      child: SizedBox(
        width: widget.size,
        height: widget.size * (widget.shadow ? 1.15 : 1),
        child: CustomPaint(painter: _BitPainter(widget.mood, _t, p, widget.shadow)),
      ),
    );
  }
}

/// Keyframe helpers: a phase in [0, 1) for a period in seconds.
double _phase(double t, double period, [double delay = 0]) => ((t + delay) % period) / period;

/// Ease in and out between 0 and 1 and back, like CSS ease-in-out on 0→50%→100%.
double _wave(double ph) => (1 - math.cos(ph * 2 * math.pi)) / 2;

class _BitPainter extends CustomPainter {
  _BitPainter(this.mood, this.t, this.p, this.shadow);

  final BitMood mood;
  final double t;
  final Palette p;
  final bool shadow;

  @override
  void paint(Canvas canvas, Size size) {
    final s = size.width / 80;
    final ink = Paint()
      ..color = p.ink
      ..style = PaintingStyle.stroke
      ..strokeCap = StrokeCap.round
      ..strokeWidth = 3;
    final fill = Paint()..color = p.ink;

    // Bob, hop or breathe.
    var dy = 0.0;
    var breathe = 1.0;
    switch (mood) {
      case BitMood.idle || BitMood.waving || BitMood.thinking:
        dy = -6 * _wave(_phase(t, 2.8));
      case BitMood.needsYou:
        dy = -6 * _wave(_phase(t, 2.0));
      case BitMood.done:
        final ph = _phase(t, 1.8);
        dy = ph < .3
            ? -14 * Curves.easeOut.transform(ph / .3)
            : ph < .5
            ? -14 * (1 - Curves.easeIn.transform((ph - .3) / .2))
            : ph < .6
            ? -5 * Curves.easeOut.transform((ph - .5) / .1)
            : -5 * (1 - Curves.easeIn.transform(math.min(1, (ph - .6) / .4)));
      case BitMood.sleeping:
        breathe = 1 + .03 * _wave(_phase(t, 3.6));
    }

    if (shadow) {
      final k = _wave(_phase(t, 2.8));
      final sh = Paint()..color = p.ink.withValues(alpha: .35 - .15 * k);
      canvas.save();
      canvas.scale(s);
      canvas.drawOval(Rect.fromCenter(center: const Offset(40, 86), width: 72 * (1 - .2 * k), height: 10), sh);
      canvas.restore();
    }

    canvas.save();
    canvas.scale(s);
    canvas.translate(0, dy);
    if (breathe != 1) {
      canvas.translate(40, 62);
      canvas.scale(breathe);
      canvas.translate(-40, -62);
    }

    // Antenna and bulb.
    final sleeping = mood == BitMood.sleeping;
    canvas.drawLine(const Offset(40, 18), sleeping ? const Offset(36, 10) : const Offset(40, 9), ink);
    final (bulbColor, bulbAt, bulbR) = switch (mood) {
      BitMood.thinking => (const Color(0xFFB9AEFF), const Offset(40, 9), 4.0),
      BitMood.needsYou => (const Color(0xFFFF8A5C), const Offset(40, 8), 5.0),
      BitMood.done => (p.mint, const Offset(40, 8), 5.0),
      BitMood.sleeping => (const Color(0xFFD7D2EA), const Offset(35, 9), 4.0),
      _ => (p.peach, const Offset(40, 8), 5.0),
    };
    var glow = 0.0;
    if (mood == BitMood.idle || mood == BitMood.waving) glow = _wave(_phase(t, 1.6));
    if (mood == BitMood.needsYou) glow = _wave(_phase(t, 1.0));
    final grow = mood == BitMood.needsYou ? .45 : .3;
    final pulsing = mood == BitMood.idle || mood == BitMood.waving || mood == BitMood.needsYou;
    final bulbAlpha = pulsing ? .55 + .45 * glow : 1.0;
    canvas.drawCircle(bulbAt, bulbR * (1 + grow * glow), Paint()..color = bulbColor.withValues(alpha: bulbAlpha));
    if (mood == BitMood.thinking) {
      canvas.save();
      canvas.translate(40, 9);
      canvas.rotate(_phase(t, 1.4) * 2 * math.pi);
      canvas.drawCircle(const Offset(0, -7.5), 2, Paint()..color = p.accent);
      canvas.restore();
    }

    // Arms, when waving.
    if (mood == BitMood.waving) {
      final left = Path()
        ..moveTo(16, 46)
        ..quadraticBezierTo(8, 50, 10, 58);
      canvas.drawPath(left, ink..strokeWidth = 3.5);
      final ph = _phase(t, 2.4);
      final deg = ph < .1
          ? -24 * ph / .1
          : ph < .2
          ? -24 + 38 * (ph - .1) / .1
          : ph < .3
          ? 14 - 38 * (ph - .2) / .1
          : ph < .4
          ? -24 + 38 * (ph - .3) / .1
          : ph < .6
          ? 14 * (1 - (ph - .4) / .2)
          : 0.0;
      canvas.save();
      canvas.translate(64, 46);
      canvas.rotate(deg * math.pi / 180);
      canvas.translate(-64, -46);
      final right = Path()
        ..moveTo(64, 46)
        ..quadraticBezierTo(74, 40, 72, 30);
      canvas.drawPath(right, ink);
      canvas.restore();
      ink.strokeWidth = 3;
    }

    // Head.
    final head = RRect.fromRectAndRadius(const Rect.fromLTWH(14, 18, 52, 44), const Radius.circular(18));
    canvas.drawRRect(head, Paint()..color = p.card);
    canvas.drawRRect(head, ink);

    // Eyes.
    switch (mood) {
      case BitMood.idle || BitMood.waving:
        final ph = _phase(t, 4.5);
        final open = ph > .92 && ph < .98 ? (ph < .95 ? 1 - (ph - .92) / .03 * .9 : .1 + (ph - .95) / .03 * .9) : 1.0;
        for (final x in const [31.0, 49.0]) {
          canvas.drawOval(Rect.fromCenter(center: Offset(x, 38), width: 9, height: 12 * open), fill);
        }
      case BitMood.thinking:
        final ph = _phase(t, 3.0);
        final dx = ph < .4
            ? 0.0
            : ph < .5
            ? 4 * (ph - .4) / .1
            : ph < .7
            ? 4.0
            : ph < .8
            ? 4 - 7 * (ph - .7) / .1
            : -3 * (1 - (ph - .8) / .2);
        for (final x in const [32.0, 50.0]) {
          canvas.drawOval(Rect.fromCenter(center: Offset(x + dx, 36), width: 9, height: 11), fill);
        }
      case BitMood.needsYou:
        final white = Paint()..color = p.card;
        for (final x in const [31.0, 49.0]) {
          canvas.drawCircle(Offset(x, 38), 6.5, fill);
          canvas.drawCircle(Offset(x + 2, 36), 2, white);
        }
      case BitMood.done:
        final arcs = ink..strokeWidth = 3.2;
        canvas.drawPath(Path()..moveTo(26, 40)..quadraticBezierTo(31, 33, 36, 40), arcs);
        canvas.drawPath(Path()..moveTo(44, 40)..quadraticBezierTo(49, 33, 54, 40), arcs);
        ink.strokeWidth = 3;
      case BitMood.sleeping:
        canvas.drawPath(Path()..moveTo(26, 38)..quadraticBezierTo(31, 42, 36, 38), ink);
        canvas.drawPath(Path()..moveTo(44, 38)..quadraticBezierTo(49, 42, 54, 38), ink);
    }

    // Cheeks.
    if (mood == BitMood.idle || mood == BitMood.waving || mood == BitMood.done) {
      final blush = Paint()..color = p.blush;
      canvas.drawCircle(const Offset(23, 49), 4, blush);
      canvas.drawCircle(const Offset(57, 49), 4, blush);
    }

    // Mouth.
    final mouth = ink..strokeWidth = 2.8;
    switch (mood) {
      case BitMood.idle || BitMood.waving:
        canvas.drawPath(Path()..moveTo(35, 49)..quadraticBezierTo(40, 54, 45, 49), mouth);
      case BitMood.thinking:
        canvas.drawLine(const Offset(35, 51), const Offset(45, 51), mouth);
      case BitMood.needsYou:
        canvas.drawOval(Rect.fromCenter(center: const Offset(40, 51), width: 7, height: 6), fill);
      case BitMood.done:
        canvas.drawPath(Path()..moveTo(33, 48)..quadraticBezierTo(40, 57, 47, 48)..close(), fill);
      case BitMood.sleeping:
        canvas.drawCircle(const Offset(40, 51), 2.5, fill);
    }
    canvas.restore();

    // Floating z's while asleep.
    if (sleeping) {
      for (final (i, (x, y, fs)) in const [(60.0, 20.0, 12.0), (64.0, 14.0, 9.0)].indexed) {
        final ph = _phase(t, 2.6, i * 1.3);
        final a = ph < .3 ? ph / .3 : 1 - (ph - .3) / .7;
        final tp = TextPainter(
          text: TextSpan(text: 'z', style: TextStyle(fontFamily: fontFamily, fontWeight: FontWeight.w900, fontSize: fs * s, color: p.accent.withValues(alpha: a.clamp(0, 1)))),
          textDirection: TextDirection.ltr,
        )..layout();
        tp.paint(canvas, Offset((x + 10 * ph) * s, (y - 22 * ph) * s - tp.height));
      }
    }
  }

  @override
  bool shouldRepaint(_BitPainter old) => old.t != t || old.mood != mood || old.p != p;
}

/// Three bouncing dots, shown while a task works.
class TypingDots extends StatefulWidget {
  const TypingDots({super.key});

  @override
  State<TypingDots> createState() => _TypingDotsState();
}

class _TypingDotsState extends State<TypingDots> with SingleTickerProviderStateMixin {
  late final Ticker _ticker = createTicker((d) => setState(() => _t = d.inMicroseconds / 1e6));
  double _t = 0;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = !Bit.animate || MediaQuery.maybeDisableAnimationsOf(context) == true;
    if (!still && !_ticker.isActive) _ticker.start();
    if (still && _ticker.isActive) _ticker.stop();
  }

  @override
  void dispose() {
    _ticker.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Semantics(
      label: 'Bit is working',
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (var i = 0; i < 3; i++)
            Builder(builder: (_) {
              final ph = _phase(_t, 1.2, -i * .15);
              final k = ph < .4 ? ph / .4 : ph < .8 ? 1 - (ph - .4) / .4 : 0.0;
              return Padding(
                padding: const EdgeInsets.symmetric(horizontal: 3),
                child: Transform.translate(
                  offset: Offset(0, -5 * k),
                  child: Container(
                    width: 8,
                    height: 8,
                    decoration: BoxDecoration(color: p.accent.withValues(alpha: .35 + .65 * k), shape: BoxShape.circle),
                  ),
                ),
              );
            }),
        ],
      ),
    );
  }
}
