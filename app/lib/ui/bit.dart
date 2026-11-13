import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

import 'bit_face.dart';
import 'theme.dart';

export 'bit_face.dart';

/// What Bit is showing.
enum BitMood { idle, waving, thinking, needsYou, done, sleeping }

/// Bit, the app's glyph: a small robot face that says what heyrobots is
/// doing. It is drawn on an 80×80 grid, the same geometry as the design.
class Bit extends StatefulWidget {
  const Bit({super.key, this.mood = BitMood.idle, this.size = 52, this.shadow = false, this.face = BitFace.classic});

  final BitMood mood;

  /// Which of Bit's family to draw; the classic face unless an agent has its own.
  final BitFace face;
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
        child: CustomPaint(painter: _BitPainter(widget.mood, _t, p, widget.shadow, widget.face)),
      ),
    );
  }
}

/// Keyframe helpers: a phase in [0, 1) for a period in seconds.
double _phase(double t, double period, [double delay = 0]) => ((t + delay) % period) / period;

/// Ease in and out between 0 and 1 and back, like CSS ease-in-out on 0→50%→100%.
double _wave(double ph) => (1 - math.cos(ph * 2 * math.pi)) / 2;

class _BitPainter extends CustomPainter {
  _BitPainter(this.mood, this.t, this.p, this.shadow, this.face);

  final BitMood mood;
  final double t;
  final Palette p;
  final bool shadow;
  final BitFace face;

  static const _pink = Color(0xFFFF8FB1);
  static const _strap = Color(0xFF4A4463);
  static const _metal = Color(0xFFC9C4DA);
  static const _denim = Color(0xFF5A86E0);
  static const _freckle = Color(0xFFD08A62);

  Paint _line(double w, [Color? c]) => Paint()
    ..color = c ?? p.ink
    ..style = PaintingStyle.stroke
    ..strokeCap = StrokeCap.round
    ..strokeJoin = StrokeJoin.round
    ..strokeWidth = w;

  Paint _fill(Color c) => Paint()..color = c;

  /// Fills a shape in a colour and outlines it in ink.
  void _shape(Canvas canvas, Path path, Color c, [double w = 2.6]) {
    canvas.drawPath(path, _fill(c));
    canvas.drawPath(path, _line(w));
  }

  @override
  void paint(Canvas canvas, Size size) {
    final s = size.width / 80;
    final f = face;
    final r = f.headRect;
    final cx = r.center.dx;
    final top = r.top;

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
      canvas.translate(cx, r.bottom);
      canvas.scale(breathe);
      canvas.translate(-cx, -r.bottom);
    }
    final sleeping = mood == BitMood.sleeping;
    final eyeY = f.eyeY;

    // Headphone band, behind everything.
    if (f.ears == BitEars.headphones) {
      final band = Path()
        ..moveTo(r.left - 3, eyeY - 2)
        ..cubicTo(r.left - 3, top - 13, r.right + 3, top - 13, r.right + 3, eyeY - 2);
      canvas.drawPath(band, _line(7));
      canvas.drawPath(band, _line(3.4, f.gear));
    }

    // Antennas, and where their bulbs sit.
    final bulbs = <Offset>[];
    final lift = f.top == BitTop.cap ? 3.0 : 0.0;
    final stem = _line(3);
    switch (f.antenna) {
      case _ when f.top == BitTop.beanie:
        bulbs.add(Offset(cx, top - 6));
      case BitAntenna.classic || BitAntenna.short:
        final len = (f.antenna == BitAntenna.short ? 5.5 : 9) + lift;
        final tip = sleeping ? Offset(cx - 4, top - len + 1) : Offset(cx, top - len);
        canvas.drawLine(Offset(cx, top), tip, stem);
        bulbs.add(tip.translate(sleeping ? -1 : 0, -1));
      case BitAntenna.spring:
        final len = 10 + lift;
        final tipX = sleeping ? cx - 4 : cx;
        final coil = Path()..moveTo(cx, top);
        for (var i = 1; i <= 5; i++) {
          final k = i / 5;
          final x = cx + (tipX - cx) * k + (i == 5 ? 0 : (i.isOdd ? 2.6 : -2.6));
          coil.lineTo(x, top - len * k);
        }
        canvas.drawPath(coil, _line(2.2));
        bulbs.add(Offset(tipX, top - len - 1));
      case BitAntenna.twin:
        for (final side in const [-1.0, 1.0]) {
          final base = Offset(cx + side * 8, top + 1.5);
          final tip = sleeping ? Offset(cx + side * 15, top - 3) : Offset(cx + side * 13, top - 7 - lift);
          canvas.drawLine(base, tip, stem);
          bulbs.add(tip.translate(side, -1));
        }
      case BitAntenna.bent:
        final tip = sleeping ? Offset(cx + 7, top - 3) : Offset(cx + 7, top - 9 - lift);
        canvas.drawPath(
          Path()
            ..moveTo(cx, top)
            ..quadraticBezierTo(cx - 1, tip.dy - 1, tip.dx, tip.dy),
          stem,
        );
        bulbs.add(tip.translate(.5, -.5));
    }

    // Arms, when waving.
    if (mood == BitMood.waving) {
      final armY = top + r.height * .64;
      final (lx, rx) = (r.left + 2, r.right - 2);
      final arm = _line(3.5);
      canvas.drawPath(
        Path()
          ..moveTo(lx, armY)
          ..quadraticBezierTo(lx - 8, armY + 4, lx - 6, armY + 12),
        arm,
      );
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
      canvas.translate(rx, armY);
      canvas.rotate(deg * math.pi / 180);
      canvas.translate(-rx, -armY);
      canvas.drawPath(
        Path()
          ..moveTo(rx, armY)
          ..quadraticBezierTo(rx + 10, armY - 6, rx + 8, armY - 16),
        _line(3),
      );
      canvas.restore();
    }

    // Ears, behind the head.
    switch (f.ears) {
      case BitEars.bolts:
        for (final x in [r.left - 5, r.right - 2]) {
          final bolt = Path()..addRRect(RRect.fromRectAndRadius(Rect.fromLTWH(x, eyeY - 5, 7, 11), const Radius.circular(3)));
          _shape(canvas, bolt, const Color(0xFFB9AEFF));
        }
      case BitEars.round:
        for (final x in [r.left + 7, r.right - 7]) {
          _shape(canvas, Path()..addOval(Rect.fromCircle(center: Offset(x, top + 4), radius: 7.5)), p.card);
          canvas.drawCircle(Offset(x, top + 3.5), 3.5, _fill(p.blush));
        }
      case BitEars.cat:
        for (final side in const [-1.0, 1.0]) {
          final ex = side < 0 ? r.left : r.right;
          final ear = Path()
            ..moveTo(ex - side * 2, top + 13)
            ..lineTo(ex - side * 3, top - 8)
            ..lineTo(ex - side * 20, top + 2)
            ..close();
          _shape(canvas, ear, p.card);
          final inner = Path()
            ..moveTo(ex - side * 5, top + 5)
            ..lineTo(ex - side * 5.5, top - 2.5)
            ..lineTo(ex - side * 13, top + 2)
            ..close();
          canvas.drawPath(inner, _fill(p.blush));
        }
      case BitEars.none || BitEars.headphones:
        break;
    }

    // Head.
    final head = RRect.fromRectAndRadius(r, Radius.circular(f.headRadius));
    canvas.drawRRect(head, _fill(p.card));
    if (f.overalls) {
      final y = r.bottom - r.height * .17;
      canvas.save();
      canvas.clipRRect(head);
      canvas.drawRect(Rect.fromLTRB(r.left, y, r.right, r.bottom), _fill(_denim));
      canvas.drawLine(Offset(r.left, y), Offset(r.right, y), _line(2.2));
      for (final x in [cx - 9, cx + 9]) {
        canvas.drawCircle(Offset(x, y + 3.6), 1.6, _fill(_strap));
      }
      canvas.restore();
    }
    canvas.drawRRect(head, _line(3));

    // Hair and hats.
    switch (f.top) {
      case BitTop.tuft:
        for (final i in const [-2, -1, 1, 2]) {
          canvas.drawPath(
            Path()
              ..moveTo(cx + i * 2.6, top + .5)
              ..quadraticBezierTo(cx + i * 3.4, top - 4, cx + i * 5.4, top - 7.5 + i.abs() * 1.6),
            _line(1.8),
          );
        }
      case BitTop.quiff:
        canvas.drawPath(
          Path()
            ..moveTo(cx - 13, top + 3.5)
            ..cubicTo(cx - 14, top - 8, cx + 3, top - 10, cx + 7, top - 2)
            ..cubicTo(cx + 2, top - 5, cx - 5, top - 3, cx - 5, top + 2)
            ..close(),
          _fill(p.ink),
        );
      case BitTop.sprout:
        final sx = cx - 11;
        canvas.drawPath(
          Path()
            ..moveTo(sx, top + .5)
            ..quadraticBezierTo(sx - .5, top - 4, sx - 1.5, top - 6.5),
          _line(2),
        );
        for (final (dx, deg) in const [(-5.5, -28.0), (3.5, 24.0)]) {
          canvas.save();
          canvas.translate(sx - 1.5 + dx, top - 8);
          canvas.rotate(deg * math.pi / 180);
          _shape(canvas, Path()..addOval(Rect.fromCenter(center: Offset.zero, width: 9, height: 5)), p.mint, 1.8);
          canvas.restore();
        }
      case BitTop.cap:
        final brim = Path()..addRRect(RRect.fromRectAndRadius(Rect.fromLTRB(cx - 2, top + 5, r.right + 9, top + 11), const Radius.circular(3)));
        _shape(canvas, brim, f.gear);
        final dome = Path()
          ..moveTo(r.left + 3, top + 9)
          ..cubicTo(r.left + 3, top - 7, r.right - 3, top - 7, r.right - 3, top + 9)
          ..close();
        _shape(canvas, dome, f.gear);
        canvas.drawCircle(Offset(cx, top - 3), 2, _fill(p.ink));
      case BitTop.beanie:
        final dome = Path()
          ..moveTo(r.left + 1, top + 10)
          ..cubicTo(r.left + 1, top - 8, r.right - 1, top - 8, r.right - 1, top + 10)
          ..close();
        _shape(canvas, dome, f.gear);
        final cuff = RRect.fromRectAndRadius(Rect.fromLTRB(r.left - 1, top + 4, r.right + 1, top + 11), const Radius.circular(3.5));
        canvas.drawRRect(cuff, _fill(f.gear));
        final rib = _line(1.2, Colors.black.withValues(alpha: .18));
        for (var x = r.left + 4; x < r.right - 1; x += 4.5) {
          canvas.drawLine(Offset(x, top + 6), Offset(x, top + 9), rib);
        }
        canvas.drawRRect(cuff, _line(2.6));
      case BitTop.none:
        break;
    }

    // Bulbs.
    final (bulbColor, bulbR) = switch (mood) {
      BitMood.thinking => (const Color(0xFFB9AEFF), 4.0),
      BitMood.needsYou => (const Color(0xFFFF8A5C), 5.0),
      BitMood.done => (p.mint, 5.0),
      BitMood.sleeping => (const Color(0xFFD7D2EA), 4.0),
      _ => (p.peach, 5.0),
    };
    var glow = 0.0;
    if (mood == BitMood.idle || mood == BitMood.waving) glow = _wave(_phase(t, 1.6));
    if (mood == BitMood.needsYou) glow = _wave(_phase(t, 1.0));
    final grow = mood == BitMood.needsYou ? .45 : .3;
    final pulsing = mood == BitMood.idle || mood == BitMood.waving || mood == BitMood.needsYou;
    final bulbAlpha = pulsing ? .55 + .45 * glow : 1.0;
    final small = bulbs.length > 1 ? .8 : 1.0;
    for (final b in bulbs) {
      if (f.top == BitTop.beanie) canvas.drawCircle(b, bulbR * 1.1 * small, _line(2.4));
      canvas.drawCircle(b, bulbR * small * (1 + grow * glow), _fill(bulbColor.withValues(alpha: bulbAlpha)));
      if (mood == BitMood.thinking) {
        canvas.save();
        canvas.translate(b.dx, b.dy);
        canvas.rotate(_phase(t, 1.4) * 2 * math.pi);
        canvas.drawCircle(Offset(0, -7.5 * small), 2, _fill(p.accent));
        canvas.restore();
      }
    }

    // Goggle strap.
    final lens = f.lens;
    if (f.eyes == BitEyes.goggles || f.eyes == BitEyes.goggle) {
      final band = Rect.fromLTRB(r.left - 1.5, eyeY - 3, r.right + 1.5, eyeY + 3);
      final strap = Path()..addRRect(RRect.fromRectAndRadius(band, const Radius.circular(2)));
      _shape(canvas, strap, _strap, 2);
    }

    // Eyes.
    final centres = f.eyes == BitEyes.one || f.eyes == BitEyes.goggle ? [(Offset(cx, eyeY), 0.0)] : [(Offset(cx - f.eyeGap, eyeY), -1.0), (Offset(cx + f.eyeGap, eyeY), 1.0)];
    for (final (c, side) in centres) {
      if (lens != null && f.eyes != BitEyes.glasses) {
        canvas.drawCircle(c, lens, _fill(_metal));
        canvas.drawCircle(c, lens, _line(2.6));
        canvas.drawCircle(c, lens - 3, _fill(Color.lerp(p.card, const Color(0xFFBFE3FF), .25)!));
        canvas.drawCircle(c, lens - 3, _line(1.6));
        canvas.save();
        canvas.clipPath(Path()..addOval(Rect.fromCircle(center: c, radius: lens - 3.6)));
        _eye(canvas, c, side);
        canvas.restore();
      } else {
        _eye(canvas, c, side);
      }
      if (f.eyes == BitEyes.glasses) {
        canvas.drawCircle(c, lens!, _line(2.2));
      }
    }
    if (f.eyes == BitEyes.glasses) {
      final g = f.eyeGap - lens!;
      canvas.drawPath(
        Path()
          ..moveTo(cx - g, eyeY - 1)
          ..quadraticBezierTo(cx, eyeY - 4, cx + g, eyeY - 1),
        _line(2.2),
      );
    }

    // Brows.
    if (f.brows != BitBrows.none) {
      final reach = lens ?? 6 * f.eyeScale;
      for (final (c, side) in centres) {
        final w = (lens ?? 5.5 * f.eyeScale) * .75;
        var y = c.dy - reach - 3.5;
        if (mood == BitMood.thinking && side <= 0) y -= 2.5;
        if (mood == BitMood.needsYou) y -= 1;
        final inner = side == 0 ? 0.0 : -side;
        final path = switch (f.brows) {
          BitBrows.flat => Path()
            ..moveTo(c.dx - w, y)
            ..lineTo(c.dx + w, y),
          BitBrows.arched => Path()
            ..moveTo(c.dx - w, y + 1)
            ..quadraticBezierTo(c.dx, y - 2.5, c.dx + w, y + 1),
          BitBrows.keen => Path()
            ..moveTo(c.dx - w, y - inner * 1.5)
            ..lineTo(c.dx + w, y + inner * 1.5),
          _ => Path()
            ..moveTo(c.dx - w, y + .5)
            ..quadraticBezierTo(c.dx, y - 2, c.dx + w, y + .5),
        };
        canvas.drawPath(path, _line(f.brows == BitBrows.bushy ? 4.2 : 2.4));
      }
    }

    // Cheeks and freckles.
    final cheekDx = r.width * .33;
    final cheekY = math.max(f.mouthY - 1, eyeY + (lens ?? 6) + 3.5);
    if (f.cheeks && (mood == BitMood.idle || mood == BitMood.waving || mood == BitMood.done)) {
      for (final side in const [-1.0, 1.0]) {
        canvas.drawCircle(Offset(cx + side * cheekDx, cheekY), 4, _fill(p.blush));
      }
    }
    if (f.freckles) {
      for (final side in const [-1.0, 1.0]) {
        for (final (fx, fy) in const [(-2.6, -1.0), (0.0, 1.4), (2.6, -1.0)]) {
          canvas.drawCircle(Offset(cx + side * cheekDx + fx, cheekY + fy), 1, _fill(_freckle));
        }
      }
    }

    // Moustache.
    final my = f.mouthY;
    if (f.moustache) {
      final y = my - 5;
      for (final side in const [-1.0, 1.0]) {
        canvas.drawPath(
          Path()
            ..moveTo(cx, y - 1)
            ..cubicTo(cx + side * 4, y - 4, cx + side * 10, y - 3, cx + side * 11, y + 1.5)
            ..cubicTo(cx + side * 8, y + 3, cx + side * 4, y + 2.5, cx, y + 1)
            ..close(),
          _fill(p.ink),
        );
      }
    }

    // Mouth.
    final mouth = _line(2.8);
    final ink = _fill(p.ink);
    switch (mood) {
      case BitMood.idle || BitMood.waving:
        _mouth(canvas, Offset(cx, my), mouth, ink);
      case BitMood.thinking:
        canvas.drawLine(Offset(cx - 5, my + 1), Offset(cx + 5, my + 1), mouth);
      case BitMood.needsYou:
        canvas.drawOval(Rect.fromCenter(center: Offset(cx, my + 1), width: 7, height: 6), ink);
      case BitMood.done:
        canvas.drawPath(
          Path()
            ..moveTo(cx - 7, my - 2)
            ..quadraticBezierTo(cx, my + 7, cx + 7, my - 2)
            ..close(),
          ink,
        );
      case BitMood.sleeping:
        canvas.drawCircle(Offset(cx, my + 1), 2.5, ink);
    }

    // A bow and headphone cups go over the head.
    if (f.bow) {
      final b = Offset(r.right - r.width * .2, top + r.height * .07);
      for (final side in const [-1.0, 1.0]) {
        final loop = Path()
          ..moveTo(b.dx, b.dy)
          ..lineTo(b.dx + side * 8, b.dy - 5.5)
          ..quadraticBezierTo(b.dx + side * 10.5, b.dy, b.dx + side * 8, b.dy + 5.5)
          ..close();
        _shape(canvas, loop, _pink, 2.2);
      }
      _shape(canvas, Path()..addOval(Rect.fromCircle(center: b, radius: 2.8)), _pink, 2.2);
    }
    if (f.ears == BitEars.headphones) {
      for (final x in [r.left - 6, r.right - 3]) {
        final cup = Path()..addRRect(RRect.fromRectAndRadius(Rect.fromLTWH(x, eyeY - 7, 9, 15), const Radius.circular(4)));
        _shape(canvas, cup, f.gear);
      }
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

  /// How open the eyes are, with a blink every few seconds.
  double get _open {
    final ph = _phase(t, 4.5);
    return ph > .92 && ph < .98 ? (ph < .95 ? 1 - (ph - .92) / .03 * .9 : .1 + (ph - .95) / .03 * .9) : 1.0;
  }

  /// One eye, centred on [c]; [side] is -1 for the left eye, 1 for the right
  /// and 0 for a single eye.
  void _eye(Canvas canvas, Offset c, double side) {
    final k = face.eyeScale;
    final fill = _fill(p.ink);
    switch (mood) {
      case BitMood.idle || BitMood.waving:
        final open = _open;
        switch (face.eyes) {
          case BitEyes.beady:
            canvas.drawOval(Rect.fromCenter(center: c, width: 6.5, height: 7.5 * open), fill);
          case BitEyes.big:
            canvas.drawOval(Rect.fromCenter(center: c, width: 10.5, height: 13 * open), fill);
            if (open > .6) canvas.drawCircle(c.translate(2, -3), 2.2, _fill(p.card));
          case BitEyes.sleepy:
            canvas.drawOval(Rect.fromCenter(center: c, width: 9.5, height: 10 * open), fill);
            canvas.drawRect(Rect.fromLTRB(c.dx - 6, c.dy - 7, c.dx + 6, c.dy - .5), _fill(p.card));
            canvas.drawLine(c.translate(-6, -.5), c.translate(6, -.5), _line(2.6));
          case BitEyes.lashes:
            canvas.drawOval(Rect.fromCenter(center: c, width: 9, height: 12 * open), fill);
            final out = side == 0 ? 1.0 : side;
            final lash = _line(2);
            canvas.drawLine(c.translate(out * 3.5, -4.5), c.translate(out * 6.5, -7), lash);
            canvas.drawLine(c.translate(out * 5, -2), c.translate(out * 8.5, -3), lash);
          default:
            canvas.drawOval(Rect.fromCenter(center: c, width: 9 * k, height: 12 * k * open), fill);
            if (face.eyes == BitEyes.one || face.eyes == BitEyes.goggle) {
              if (open > .6) canvas.drawCircle(c.translate(2 * k, -2.5 * k), 1.8 * k, _fill(p.card));
            }
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
        canvas.drawOval(Rect.fromCenter(center: c.translate((dx + 1) * k, -2 * k), width: 9 * k, height: 11 * k), fill);
      case BitMood.needsYou:
        canvas.drawCircle(c, 6.5 * k, fill);
        canvas.drawCircle(c.translate(2 * k, -2 * k), 2 * k, _fill(p.card));
      case BitMood.done:
        final w = 5 * k;
        canvas.drawPath(
          Path()
            ..moveTo(c.dx - w, c.dy + 2 * k)
            ..quadraticBezierTo(c.dx, c.dy - 5 * k, c.dx + w, c.dy + 2 * k),
          _line(3.2 * math.min(1, k + .1)),
        );
      case BitMood.sleeping:
        final w = 5 * k;
        canvas.drawPath(
          Path()
            ..moveTo(c.dx - w, c.dy)
            ..quadraticBezierTo(c.dx, c.dy + 4 * k, c.dx + w, c.dy),
          _line(3 * math.min(1, k + .1)),
        );
    }
  }

  /// The face's own mouth, for idle and waving.
  void _mouth(Canvas canvas, Offset m, Paint line, Paint ink) {
    final (x, y) = (m.dx, m.dy);
    switch (face.mouth) {
      case BitMouth.smile:
        canvas.drawPath(
          Path()
            ..moveTo(x - 5, y)
            ..quadraticBezierTo(x, y + 5, x + 5, y),
          line,
        );
      case BitMouth.grin:
        final open = Path()
          ..moveTo(x - 7, y - 1.5)
          ..lineTo(x + 7, y - 1.5)
          ..quadraticBezierTo(x + 7, y + 7, x, y + 7)
          ..quadraticBezierTo(x - 7, y + 7, x - 7, y - 1.5)
          ..close();
        canvas.drawPath(open, ink);
        canvas.save();
        canvas.clipPath(open);
        canvas.drawRect(Rect.fromLTRB(x - 7, y - 1.5, x + 7, y + 1.8), _fill(Colors.white));
        canvas.drawLine(Offset(x, y - 1.5), Offset(x, y + 1.8), _line(1.2, const Color(0xFF2A2540)));
        canvas.restore();
        canvas.drawPath(open, _line(2));
      case BitMouth.o:
        canvas.drawOval(Rect.fromCenter(center: m.translate(0, 1.5), width: 6, height: 7), ink);
      case BitMouth.cat:
        canvas.drawPath(
          Path()
            ..moveTo(x - 6, y)
            ..quadraticBezierTo(x - 3, y + 4.5, x, y + .5)
            ..quadraticBezierTo(x + 3, y + 4.5, x + 6, y),
          _line(2.5),
        );
      case BitMouth.tongue:
        final tongue = Path()
          ..moveTo(x - 2, y + 2.5)
          ..lineTo(x - 2, y + 5)
          ..quadraticBezierTo(x + .5, y + 9, x + 3, y + 5)
          ..lineTo(x + 3, y + 2.5);
        canvas.drawPath(tongue, _fill(const Color(0xFFFF7A93)));
        canvas.drawPath(tongue, _line(1.8));
        canvas.drawPath(
          Path()
            ..moveTo(x - 6, y)
            ..quadraticBezierTo(x, y + 5.5, x + 6, y),
          line,
        );
      case BitMouth.bigSmile:
        final open = Path()
          ..moveTo(x - 7.5, y - 1.5)
          ..quadraticBezierTo(x, y + 9, x + 7.5, y - 1.5)
          ..close();
        canvas.drawPath(open, ink);
        canvas.save();
        canvas.clipPath(open);
        canvas.drawCircle(Offset(x, y + 6.5), 4, _fill(const Color(0xFFFF7A93)));
        canvas.restore();
      case BitMouth.smirk:
        canvas.drawPath(
          Path()
            ..moveTo(x - 5, y + 1.5)
            ..quadraticBezierTo(x + 1, y + 4.5, x + 6, y - 1.5),
          line,
        );
      case BitMouth.toothy:
        for (final dx in const [-2.6, .4]) {
          final tooth = Rect.fromLTWH(x + dx, y + 1.5, 2.6, 3.4);
          canvas.drawRect(tooth, _fill(Colors.white));
          canvas.drawRect(tooth, _line(1.2));
        }
        canvas.drawPath(
          Path()
            ..moveTo(x - 6, y)
            ..quadraticBezierTo(x, y + 4, x + 6, y),
          line,
        );
    }
  }

  @override
  bool shouldRepaint(_BitPainter old) => old.t != t || old.mood != mood || old.p != p || old.face != face;
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
