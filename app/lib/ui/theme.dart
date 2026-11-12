import 'package:flutter/material.dart';

/// The app's colors, from the "cute" design: a soft lilac ground, one
/// periwinkle accent for anything pressable, peach for warm touches.
class Palette {
  const Palette._({
    required this.ground,
    required this.card,
    required this.ink,
    required this.muted,
    required this.line,
    required this.accent,
    required this.accentDeep,
    required this.accentSoft,
    required this.peach,
    required this.peachSoft,
    required this.peachInk,
    required this.mint,
    required this.mintSoft,
    required this.mintInk,
    required this.blush,
    required this.hairline,
    required this.track,
    required this.switchOff,
    required this.online,
    required this.danger,
    required this.warn,
  });

  final Color ground, card, ink, muted, line;
  final Color accent, accentDeep, accentSoft;
  final Color peach, peachSoft, peachInk;
  final Color mint, mintSoft, mintInk;
  final Color blush;

  /// Row dividers inside cards; the sunken track of segments and small
  /// buttons; a switch that is off; the "connected" dot; red words such as
  /// Disconnect; orange notes such as new tools to review.
  final Color hairline, track, switchOff, online, danger, warn;

  static const light = Palette._(
    ground: Color(0xFFF7F4FF),
    card: Color(0xFFFFFFFF),
    ink: Color(0xFF2A2540),
    muted: Color(0xFF6B6680),
    line: Color(0xFFE4DEFA),
    accent: Color(0xFF5848E0),
    accentDeep: Color(0xFF3F31B8),
    accentSoft: Color(0xFFECE8FF),
    peach: Color(0xFFFFB38A),
    peachSoft: Color(0xFFFFE9DC),
    peachInk: Color(0xFF5C2408),
    mint: Color(0xFF3BB89A),
    mintSoft: Color(0xFFDDF5EE),
    mintInk: Color(0xFF1E6E5A),
    blush: Color(0xFFFFC9D4),
    hairline: Color(0xFFF0ECFB),
    track: Color(0xFFF3F0FF),
    switchOff: Color(0xFFD7D2EA),
    online: Color(0xFF2FA585),
    danger: Color(0xFFB4274B),
    warn: Color(0xFFB4501E),
  );

  static const dark = Palette._(
    ground: Color(0xFF17142A),
    card: Color(0xFF242040),
    ink: Color(0xFFEDEAFB),
    muted: Color(0xFFADA7C6),
    line: Color(0xFF332D55),
    accent: Color(0xFF8C80F5),
    accentDeep: Color(0xFF5848E0),
    accentSoft: Color(0xFF2E2860),
    peach: Color(0xFFFFB38A),
    peachSoft: Color(0xFF3D2A22),
    peachInk: Color(0xFFFFD9C4),
    mint: Color(0xFF3BB89A),
    mintSoft: Color(0xFF173A32),
    mintInk: Color(0xFFA6E8D6),
    blush: Color(0xFF6B3A4C),
    hairline: Color(0xFF2C2748),
    track: Color(0xFF2A2550),
    switchOff: Color(0xFF4A4468),
    online: Color(0xFF3BB89A),
    danger: Color(0xFFFF8FA6),
    warn: Color(0xFFFFB38A),
  );

  static Palette of(BuildContext context) =>
      Theme.of(context).brightness == Brightness.dark ? dark : light;
}

/// Nunito when it is bundled under assets/fonts, else the platform's font.
const fontFamily = 'Nunito';

ThemeData buildTheme(Brightness brightness) {
  final p = brightness == Brightness.dark ? Palette.dark : Palette.light;
  final scheme = ColorScheme.fromSeed(seedColor: p.accent, brightness: brightness).copyWith(
    primary: p.accent,
    onPrimary: Colors.white,
    surface: p.ground,
    onSurface: p.ink,
    surfaceContainerHighest: p.accentSoft,
    surfaceContainerHigh: p.card,
    error: const Color(0xFFC0392B),
  );
  final base = ThemeData(useMaterial3: true, colorScheme: scheme, fontFamily: fontFamily, brightness: brightness);
  final text = base.textTheme.apply(bodyColor: p.ink, displayColor: p.ink);
  // Pill buttons with the design's solid bottom edge.
  final pill = EdgeBorder(radius: 999, depth: 3, edge: p.accentDeep);
  final softPill = EdgeBorder(radius: 999, depth: 3, edge: p.line);
  return base.copyWith(
    scaffoldBackgroundColor: p.ground,
    textTheme: text.copyWith(
      headlineSmall: text.headlineSmall?.copyWith(fontWeight: FontWeight.w900, letterSpacing: -0.3),
      headlineMedium: text.headlineMedium?.copyWith(fontWeight: FontWeight.w900, letterSpacing: -0.5),
      titleLarge: text.titleLarge?.copyWith(fontWeight: FontWeight.w900),
      titleMedium: text.titleMedium?.copyWith(fontWeight: FontWeight.w800),
      titleSmall: text.titleSmall?.copyWith(fontWeight: FontWeight.w800),
      labelLarge: text.labelLarge?.copyWith(fontWeight: FontWeight.w800),
    ),
    appBarTheme: AppBarTheme(
      backgroundColor: p.ground,
      foregroundColor: p.ink,
      elevation: 0,
      scrolledUnderElevation: 0,
      centerTitle: false,
      titleTextStyle: TextStyle(fontFamily: fontFamily, fontSize: 20, fontWeight: FontWeight.w900, color: p.ink),
    ),
    cardTheme: CardThemeData(
      color: p.card,
      elevation: 0,
      shape: EdgeBorder(radius: 22, depth: 3, edge: p.line),
      margin: const EdgeInsets.symmetric(vertical: 6),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: p.accent,
        foregroundColor: Colors.white,
        minimumSize: const Size(44, 52),
        shape: pill,
        textStyle: const TextStyle(fontFamily: fontFamily, fontSize: 16, fontWeight: FontWeight.w900),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: p.accent,
        backgroundColor: p.card,
        minimumSize: const Size(44, 52),
        side: BorderSide.none,
        shape: softPill,
        textStyle: const TextStyle(fontFamily: fontFamily, fontSize: 15, fontWeight: FontWeight.w900),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        foregroundColor: p.accent,
        minimumSize: const Size(44, 44),
        textStyle: const TextStyle(fontFamily: fontFamily, fontWeight: FontWeight.w800),
      ),
    ),
    floatingActionButtonTheme: FloatingActionButtonThemeData(
      backgroundColor: p.accent,
      foregroundColor: Colors.white,
      shape: const StadiumBorder(),
      extendedTextStyle: const TextStyle(fontFamily: fontFamily, fontSize: 16, fontWeight: FontWeight.w800),
    ),
    chipTheme: ChipThemeData(
      backgroundColor: p.card,
      selectedColor: p.accent,
      disabledColor: p.accentSoft,
      side: BorderSide.none,
      shape: EdgeBorder(radius: 999, depth: 2, edge: p.line),
      showCheckmark: false,
      padding: const EdgeInsets.symmetric(horizontal: 13, vertical: 7),
      labelPadding: EdgeInsets.zero,
      labelStyle: WidgetStateTextStyle.resolveWith(
        (s) => TextStyle(fontFamily: fontFamily, fontSize: 13, fontWeight: FontWeight.w800, color: s.contains(WidgetState.selected) ? Colors.white : p.ink),
      ),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: p.card,
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(16), borderSide: BorderSide(color: p.line, width: 2)),
      enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(16), borderSide: BorderSide(color: p.line, width: 2)),
      focusedBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(16), borderSide: BorderSide(color: p.accent, width: 2)),
      labelStyle: TextStyle(color: p.muted, fontWeight: FontWeight.w700),
    ),
    listTileTheme: ListTileThemeData(iconColor: p.accent, titleTextStyle: TextStyle(fontFamily: fontFamily, fontSize: 16, fontWeight: FontWeight.w700, color: p.ink)),
    snackBarTheme: SnackBarThemeData(
      behavior: SnackBarBehavior.floating,
      backgroundColor: p.ink,
      contentTextStyle: TextStyle(fontFamily: fontFamily, color: p.ground, fontWeight: FontWeight.w700),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
    ),
    dialogTheme: DialogThemeData(backgroundColor: p.card, shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(26))),
    switchTheme: SwitchThemeData(
      trackColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? p.accent : p.switchOff),
      thumbColor: const WidgetStatePropertyAll(Colors.white),
      trackOutlineColor: const WidgetStatePropertyAll(Colors.transparent),
      thumbIcon: const WidgetStatePropertyAll(Icon(null)),
    ),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: p.ground,
      showDragHandle: true,
      shape: const RoundedRectangleBorder(borderRadius: BorderRadius.vertical(top: Radius.circular(26))),
    ),
  );
}

/// A rounded card with the design's soft drop edge.
class SoftCard extends StatelessWidget {
  const SoftCard({super.key, required this.child, this.color, this.onTap, this.padding = const EdgeInsets.all(16)});

  final Widget child;
  final Color? color;
  final VoidCallback? onTap;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final radius = BorderRadius.circular(22);
    return DecoratedBox(
      decoration: BoxDecoration(
        borderRadius: radius,
        boxShadow: color == null ? [BoxShadow(color: p.line, offset: const Offset(0, 3))] : null,
      ),
      child: Material(
        color: color ?? p.card,
        borderRadius: radius,
        child: InkWell(
          borderRadius: radius,
          onTap: onTap,
          child: Padding(padding: padding, child: child),
        ),
      ),
    );
  }
}

/// A small rounded label, such as a task's stage.
class Pill extends StatelessWidget {
  const Pill(this.text, {super.key, required this.background, required this.foreground});

  final String text;
  final Color background, foreground;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
      decoration: BoxDecoration(color: background, borderRadius: BorderRadius.circular(999)),
      child: Text(text, style: TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: foreground)),
    );
  }
}

/// A rounded shape with the design's solid bottom edge: the shape again,
/// [depth] pixels lower, in [edge], showing below it.
class EdgeBorder extends RoundedRectangleBorder {
  EdgeBorder({required this.radius, this.depth = 3, required this.edge, super.side})
    : super(borderRadius: BorderRadius.circular(radius));

  final double radius, depth;
  final Color edge;

  @override
  EdgeBorder copyWith({BorderSide? side, BorderRadiusGeometry? borderRadius}) =>
      EdgeBorder(radius: radius, depth: depth, edge: edge, side: side ?? this.side);

  @override
  ShapeBorder scale(double t) => EdgeBorder(radius: radius * t, depth: depth * t, edge: edge, side: side.scale(t));

  @override
  ShapeBorder? lerpFrom(ShapeBorder? a, double t) => a is EdgeBorder ? (t < .5 ? a : this) : super.lerpFrom(a, t);

  @override
  ShapeBorder? lerpTo(ShapeBorder? b, double t) => b is EdgeBorder ? (t < .5 ? this : b) : super.lerpTo(b, t);

  @override
  void paint(Canvas canvas, Rect rect, {TextDirection? textDirection}) {
    if (depth > 0) {
      final r = Radius.circular(radius.clamp(0, rect.shortestSide / 2).toDouble());
      final shape = Path()..addRRect(RRect.fromRectAndRadius(rect, r));
      final lower = Path()..addRRect(RRect.fromRectAndRadius(rect.shift(Offset(0, depth)), r));
      canvas.drawPath(Path.combine(PathOperation.difference, lower, shape), Paint()..color = edge);
    }
    super.paint(canvas, rect, textDirection: textDirection);
  }

  @override
  bool operator ==(Object other) =>
      other is EdgeBorder && other.radius == radius && other.depth == depth && other.edge == edge && other.side == side;

  @override
  int get hashCode => Object.hash(radius, depth, edge, side);
}

/// The soft background and strong ink of a tile tint, as the server names
/// them for apps and agents: violet, peach, mint, sky, rose, sun or ink.
(Color, Color) tintColors(String tint, Palette p) {
  final dark = p.ground.computeLuminance() < .2;
  (Color, Color) pick((Color, Color) light, (Color, Color) night) => dark ? night : light;
  return switch (tint) {
    'peach' => pick((p.peachSoft, const Color(0xFFC2602E)), (p.peachSoft, p.peachInk)),
    'mint' => pick((p.mintSoft, p.mintInk), (p.mintSoft, p.mintInk)),
    'sky' => pick((const Color(0xFFDDEEFB), const Color(0xFF1F5F99)), (const Color(0xFF1C2E40), const Color(0xFFA9D2F5))),
    'rose' => pick((const Color(0xFFFFE4EA), const Color(0xFFB4274B)), (const Color(0xFF3D2129), const Color(0xFFFFB3C2))),
    'sun' => pick((const Color(0xFFFFF1D1), const Color(0xFFB07A0F)), (const Color(0xFF3A3018), const Color(0xFFF5D38A))),
    'ink' => (p.ink, p.card),
    _ => (p.accentSoft, p.accent),
  };
}
