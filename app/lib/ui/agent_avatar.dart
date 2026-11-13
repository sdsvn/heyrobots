import 'package:flutter/material.dart';

import 'bit.dart';
import 'theme.dart';

/// Each agent's color, as a strong and a soft shade.
(Color, Color) agentColors(String name, Palette p) {
  final dark = p.ground.computeLuminance() < .2;
  Color soft(Color light, Color darkSoft) => dark ? darkSoft : light;
  return switch (name) {
    'peach' => (const Color(0xFFE07A45), soft(const Color(0xFFFFE9DC), const Color(0xFF3D2A22))),
    'mint' => (const Color(0xFF2FA585), soft(const Color(0xFFDDF5EE), const Color(0xFF1C3631))),
    'sky' => (const Color(0xFF3B8FD9), soft(const Color(0xFFDDEEFB), const Color(0xFF1C2E40))),
    'rose' => (const Color(0xFFD9566F), soft(const Color(0xFFFFE0E6), const Color(0xFF3D2129))),
    'sun' => (const Color(0xFFD99A1E), soft(const Color(0xFFFFF1D1), const Color(0xFF3A3018))),
    _ => (p.accent, p.accentSoft),
  };
}

/// The small icon an agent wears.
IconData agentBadgeIcon(String badge) => switch (badge) {
  'book' => Icons.menu_book_rounded,
  'pen' => Icons.edit_rounded,
  'mail' => Icons.mail_rounded,
  'calendar' => Icons.event_rounded,
  'code' => Icons.code_rounded,
  'search' => Icons.search_rounded,
  'bolt' => Icons.bolt_rounded,
  _ => Icons.star_rounded,
};

/// Bit in an agent's color, wearing its badge, so you can tell at a glance
/// who is working.
class AgentAvatar extends StatelessWidget {
  const AgentAvatar({super.key, required this.color, required this.badge, this.size = 40, this.mood = BitMood.idle, this.name, this.showBadge = true, this.fill, this.seed});

  final String color;
  final String badge;
  final double size;
  final BitMood mood;

  /// Read out by screen readers.
  final String? name;

  /// The small badge in the corner; group pictures leave it off.
  final bool showBadge;

  /// The circle's fill, when not the agent's soft shade.
  final Color? fill;

  /// The agent's id, which picks its face; without one it is Bit's own face.
  final String? seed;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final (strong, soft) = agentColors(color, p);
    final dot = size * .42;
    return Semantics(
      label: name,
      child: SizedBox(
        width: size,
        height: size,
        child: Stack(
          clipBehavior: Clip.none,
          children: [
            Container(
              width: size,
              height: size,
              decoration: BoxDecoration(color: fill ?? soft, shape: BoxShape.circle, border: Border.all(color: strong, width: size > 44 ? 3 : 2)),
              alignment: Alignment.center,
              child: Bit(mood: mood, size: size * .74, face: BitFace.of(seed)),
            ),
            if (showBadge) Positioned(
              right: -size * .06,
              bottom: -size * .06,
              child: Container(
                width: dot,
                height: dot,
                decoration: BoxDecoration(color: strong, shape: BoxShape.circle, border: Border.all(color: p.card, width: 2)),
                child: Icon(agentBadgeIcon(badge), size: dot * .62, color: Colors.white),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
