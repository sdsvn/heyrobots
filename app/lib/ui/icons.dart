import 'package:flutter/material.dart';

/// The design's line icons, drawn from the mockups' own SVG paths on a
/// 24 by 24 grid, so they match stroke for stroke.
class Glyph {
  const Glyph._(this.paths);

  /// SVG path data, one entry per shape.
  final List<String> paths;

  static String _circle(double cx, double cy, double r) =>
      'M${cx - r} ${cy}a$r $r 0 1 0 ${2 * r} 0a$r $r 0 1 0 ${-2 * r} 0';

  static String _rect(double x, double y, double w, double h, double r) =>
      'M${x + r} ${y}h${w - 2 * r}a$r $r 0 0 1 $r ${r}v${h - 2 * r}a$r $r 0 0 1 ${-r} ${r}h${-(w - 2 * r)}a$r $r 0 0 1 ${-r} ${-r}v${-(h - 2 * r)}a$r $r 0 0 1 $r ${-r}z';

  static final search = Glyph._([_circle(11, 11, 6.5), 'm20 20-4-4']);
  static const back = Glyph._(['m15 6-6 6 6 6']);
  static const chevron = Glyph._(['m9 6 6 6-6 6']);
  static const check = Glyph._(['m5 12 5 5L20 7']);
  static const arrow = Glyph._(['M5 12h14M13 6l6 6-6 6']);
  static const plus = Glyph._(['M12 5v14M5 12h14']);
  static const chat = Glyph._(['M20 12a8 8 0 0 1-11.8 7L4 20l1-4.2A8 8 0 1 1 20 12z']);
  static final team = Glyph._([_circle(9, 8, 3.5), 'M2.5 20a6.5 6.5 0 0 1 13 0', _circle(17, 9, 2.5), 'M16 14.2a5 5 0 0 1 5.5 4.8']);
  /// An agent: Bit's head, with its antenna and two eyes.
  static final agent = Glyph._([_rect(4, 8, 16, 12, 4.5), 'M12 8V5.5', _circle(12, 4, 1.5), _circle(9.5, 14, .9), _circle(14.5, 14, .9)]);
  static final plans = Glyph._([_rect(4, 5, 16, 16, 3), 'M8 3v4M16 3v4M4 10h16M8 14h3M8 17h6']);
  static final terminal = Glyph._([_rect(3, 4, 18, 16, 3), 'm7 9 3 3-3 3M12 15h5']);
  static const cloud = Glyph._(['M12 3a9 9 0 1 0 9 9', 'M12 8v4l3 2']);
  static final chip = Glyph._([_rect(4, 4, 16, 16, 3), 'M9 9h6v6H9z']);
  static const bolt = Glyph._(['M13 2 4 14h7l-1 8 9-12h-7z']);
  static const sparkle = Glyph._(['M12 3l2.6 6.4L21 12l-6.4 2.6L12 21l-2.6-6.4L3 12l6.4-2.6z']);
  static const puzzle = Glyph._(['M10 4h4v3a2 2 0 0 0 4 0V4h2v6h-3a2 2 0 0 0 0 4h3v6H4v-6h3a2 2 0 0 0 0-4H4V4h2v3a2 2 0 0 0 4 0z']);
  static const book = Glyph._(['M4 19.5A2.5 2.5 0 0 1 6.5 17H20', 'M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z']);
  static final clock = Glyph._([_circle(12, 12, 9), 'M12 7v5l3 2']);
  static final phone = Glyph._([_rect(7, 2, 10, 20, 2.5), 'M11 18h2']);
  static const train = Glyph._(['M3 11h18M5 11l2-6h10l2 6M6 11v8h12v-8']);
  static const code = Glyph._(['m8 7-5 5 5 5M16 7l5 5-5 5']);
  static final mail = Glyph._([_rect(3, 5, 18, 14, 3), 'm3 7 9 6 9-6']);
  static final note = Glyph._([_rect(4, 4, 16, 16, 3), 'M8 9h8M8 13h5']);
  static const heart = Glyph._(['M12 20s-7-4.4-7-10a4 4 0 0 1 7-2.6A4 4 0 0 1 19 10c0 5.6-7 10-7 10z']);

  Path toPath() {
    final out = Path();
    for (final d in paths) {
      out.addPath(_parse(d), Offset.zero);
    }
    return out;
  }
}

final _tokens = RegExp(r'[A-Za-z]|-?(?:\d+\.?\d*|\.\d+)(?:e-?\d+)?');

/// Enough of SVG path syntax for the icons above: M L H V C S Q A Z, in
/// either case.
Path _parse(String d) {
  final t = _tokens.allMatches(d).map((m) => m[0]!).toList();
  final path = Path();
  var i = 0, cmd = 'M';
  var x = 0.0, y = 0.0, sx = 0.0, sy = 0.0;
  Offset? lastCtrl;
  bool isNum(int k) => k < t.length && !RegExp('[A-Za-z]').hasMatch(t[k]);
  double n() => double.parse(t[i++]);
  while (i < t.length) {
    if (!isNum(i)) cmd = t[i++];
    final rel = cmd == cmd.toLowerCase();
    double ax(double v) => rel ? x + v : v;
    double ay(double v) => rel ? y + v : v;
    Offset? ctrl;
    switch (cmd.toUpperCase()) {
      case 'M':
        x = ax(n());
        y = ay(n());
        path.moveTo(x, y);
        sx = x;
        sy = y;
        cmd = rel ? 'l' : 'L';
      case 'L':
        x = ax(n());
        y = ay(n());
        path.lineTo(x, y);
      case 'H':
        x = rel ? x + n() : n();
        path.lineTo(x, y);
      case 'V':
        y = rel ? y + n() : n();
        path.lineTo(x, y);
      case 'C':
        final c1 = Offset(ax(n()), ay(n()));
        final c2 = Offset(ax(n()), ay(n()));
        final e = Offset(ax(n()), ay(n()));
        path.cubicTo(c1.dx, c1.dy, c2.dx, c2.dy, e.dx, e.dy);
        ctrl = c2;
        x = e.dx;
        y = e.dy;
      case 'S':
        final c1 = lastCtrl == null ? Offset(x, y) : Offset(2 * x - lastCtrl.dx, 2 * y - lastCtrl.dy);
        final c2 = Offset(ax(n()), ay(n()));
        final e = Offset(ax(n()), ay(n()));
        path.cubicTo(c1.dx, c1.dy, c2.dx, c2.dy, e.dx, e.dy);
        ctrl = c2;
        x = e.dx;
        y = e.dy;
      case 'Q':
        final c = Offset(ax(n()), ay(n()));
        final e = Offset(ax(n()), ay(n()));
        path.quadraticBezierTo(c.dx, c.dy, e.dx, e.dy);
        x = e.dx;
        y = e.dy;
      case 'A':
        final rx = n(), ry = n(), rot = n(), large = n() != 0, sweep = n() != 0;
        x = ax(n());
        y = ay(n());
        path.arcToPoint(Offset(x, y), radius: Radius.elliptical(rx, ry), rotation: rot, largeArc: large, clockwise: sweep);
      case 'Z':
        path.close();
        x = sx;
        y = sy;
      default:
        i++;
    }
    lastCtrl = ctrl;
  }
  return path;
}

/// One of the design's line icons.
class LineIcon extends StatelessWidget {
  const LineIcon(this.glyph, {super.key, this.size = 22, this.color, this.stroke = 2.2});

  final Glyph glyph;
  final double size;
  final Color? color;

  /// The stroke width on the 24 by 24 grid, as the mockups give it.
  final double stroke;

  @override
  Widget build(BuildContext context) {
    return SizedBox.square(
      dimension: size,
      child: CustomPaint(painter: _GlyphPainter(glyph, color ?? IconTheme.of(context).color ?? Colors.black, stroke)),
    );
  }
}

class _GlyphPainter extends CustomPainter {
  _GlyphPainter(this.glyph, this.color, this.stroke);

  final Glyph glyph;
  final Color color;
  final double stroke;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.scale(size.width / 24);
    canvas.drawPath(
      glyph.toPath(),
      Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = stroke
        ..strokeCap = StrokeCap.round
        ..strokeJoin = StrokeJoin.round
        ..color = color,
    );
  }

  @override
  bool shouldRepaint(_GlyphPainter old) => old.glyph != glyph || old.color != color || old.stroke != stroke;
}
