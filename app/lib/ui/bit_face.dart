import 'dart:ui';

/// The shape of a face's head.
enum BitHead { classic, capsule, round, wide, square }

/// How a face's eyes look when it is idle. Every style still shows the
/// mood eyes (looking around, sleeping, done) in the same places.
enum BitEyes { classic, beady, big, lashes, sleepy, one, goggles, goggle, glasses }

/// The mouth a face wears when idle or waving; other moods use the mood mouth.
enum BitMouth { smile, grin, o, cat, tongue, bigSmile, smirk, toothy }

enum BitBrows { none, flat, arched, keen, bushy }

enum BitAntenna { classic, short, spring, twin, bent }

/// Hair or a hat on top of the head.
enum BitTop { none, tuft, quiff, sprout, cap, beanie }

/// What sits on the sides of the head.
enum BitEars { none, bolts, round, cat, headphones }

/// One of Bit's family: a hand-picked combination of features. Agents are
/// given one from their id, so the same agent looks the same everywhere.
class BitFace {
  const BitFace(
    this.name, {
    this.head = BitHead.classic,
    this.eyes = BitEyes.classic,
    this.mouth = BitMouth.smile,
    this.brows = BitBrows.none,
    this.antenna = BitAntenna.classic,
    this.top = BitTop.none,
    this.ears = BitEars.none,
    this.cheeks = true,
    this.freckles = false,
    this.moustache = false,
    this.bow = false,
    this.overalls = false,
    this.gear = const Color(0xFF5BA3E8),
  });

  final String name;
  final BitHead head;
  final BitEyes eyes;
  final BitMouth mouth;
  final BitBrows brows;
  final BitAntenna antenna;
  final BitTop top;
  final BitEars ears;
  final bool cheeks, freckles, moustache, bow, overalls;

  /// The colour of the cap, beanie or headphones.
  final Color gear;

  /// The app's own face.
  static const classic = BitFace('Bit');

  /// The faces agents are given. The classic face is kept for Bit itself.
  static const family = <BitFace>[
    BitFace('Gogo', head: BitHead.capsule, eyes: BitEyes.goggles, mouth: BitMouth.grin, top: BitTop.tuft, overalls: true, cheeks: false),
    BitFace('Stu', head: BitHead.capsule, eyes: BitEyes.goggle, mouth: BitMouth.toothy, top: BitTop.tuft, overalls: true, cheeks: false),
    BitFace('Pip', head: BitHead.round, eyes: BitEyes.big, mouth: BitMouth.cat, bow: true),
    BitFace('Dot', head: BitHead.round, eyes: BitEyes.one, mouth: BitMouth.o, antenna: BitAntenna.twin),
    BitFace('Hoot', eyes: BitEyes.glasses, mouth: BitMouth.smile, brows: BitBrows.bushy, moustache: true, cheeks: false),
    BitFace('Biscuit', head: BitHead.wide, eyes: BitEyes.beady, mouth: BitMouth.tongue, ears: BitEars.round, freckles: true),
    BitFace('Sprig', head: BitHead.square, mouth: BitMouth.smile, top: BitTop.sprout, brows: BitBrows.arched),
    BitFace('Tuk', eyes: BitEyes.sleepy, mouth: BitMouth.cat, top: BitTop.beanie, gear: Color(0xFF4FB7A0), freckles: true),
    BitFace('Scout', eyes: BitEyes.big, mouth: BitMouth.grin, top: BitTop.cap, gear: Color(0xFFE8604F), cheeks: false),
    BitFace('Bass', head: BitHead.wide, eyes: BitEyes.classic, mouth: BitMouth.smirk, ears: BitEars.headphones, brows: BitBrows.keen, gear: Color(0xFF7A6CF0)),
    BitFace('Nova', head: BitHead.round, eyes: BitEyes.lashes, mouth: BitMouth.smile, bow: true, antenna: BitAntenna.bent),
    BitFace('Bolt', head: BitHead.square, eyes: BitEyes.beady, mouth: BitMouth.o, ears: BitEars.bolts, brows: BitBrows.flat, antenna: BitAntenna.spring),
    BitFace('Mo', head: BitHead.capsule, eyes: BitEyes.goggles, mouth: BitMouth.smile, moustache: true, cheeks: false, antenna: BitAntenna.short),
    BitFace('Tilly', head: BitHead.capsule, eyes: BitEyes.glasses, mouth: BitMouth.toothy, top: BitTop.quiff),
    BitFace('Ziggy', head: BitHead.square, eyes: BitEyes.goggle, mouth: BitMouth.bigSmile, antenna: BitAntenna.spring, cheeks: false),
    BitFace('Momo', head: BitHead.round, eyes: BitEyes.beady, mouth: BitMouth.cat, ears: BitEars.round),
    BitFace(
      'Rex',
      head: BitHead.wide,
      eyes: BitEyes.classic,
      mouth: BitMouth.grin,
      ears: BitEars.bolts,
      brows: BitBrows.keen,
      antenna: BitAntenna.short,
      cheeks: false,
    ),
    BitFace('Juno', head: BitHead.square, eyes: BitEyes.big, mouth: BitMouth.tongue, freckles: true, antenna: BitAntenna.bent, cheeks: false),
    BitFace('Wren', head: BitHead.round, eyes: BitEyes.glasses, mouth: BitMouth.cat, top: BitTop.quiff),
    BitFace('Kit', eyes: BitEyes.lashes, mouth: BitMouth.cat, ears: BitEars.cat),
    BitFace(
      'Gus',
      head: BitHead.capsule,
      eyes: BitEyes.beady,
      mouth: BitMouth.smile,
      moustache: true,
      top: BitTop.beanie,
      gear: Color(0xFFE8604F),
      cheeks: false,
    ),
    BitFace('Lulu', head: BitHead.wide, eyes: BitEyes.lashes, mouth: BitMouth.bigSmile, bow: true),
    BitFace('Ollie', head: BitHead.round, eyes: BitEyes.one, mouth: BitMouth.grin, top: BitTop.cap, gear: Color(0xFF4FB7A0), cheeks: false),
    BitFace('Fizz', head: BitHead.capsule, eyes: BitEyes.goggles, mouth: BitMouth.o, antenna: BitAntenna.twin, overalls: true, cheeks: false),
    BitFace('Pico', head: BitHead.square, eyes: BitEyes.beady, mouth: BitMouth.tongue, ears: BitEars.headphones, gear: Color(0xFFFF8FB1)),
    BitFace('Sunny', eyes: BitEyes.big, mouth: BitMouth.bigSmile, top: BitTop.tuft, freckles: true),
    BitFace('Zen', head: BitHead.round, eyes: BitEyes.sleepy, mouth: BitMouth.smile, antenna: BitAntenna.twin, brows: BitBrows.arched),
  ];

  /// The face for an agent, picked from its id. The general agent is Bit
  /// itself, and so is anything without an id.
  static BitFace of(String? id) {
    if (id == null || id.isEmpty || id == 'ag_general') return classic;
    // A small hash that stays under 2^31, so web and native agree.
    var h = 7;
    for (final c in id.codeUnits) {
      h = (h * 31 + c) & 0x3fffffff;
    }
    return family[h % family.length];
  }

  // Geometry on the 80×80 grid.

  Rect get headRect => switch (head) {
    BitHead.classic => const Rect.fromLTWH(14, 18, 52, 44),
    BitHead.capsule => const Rect.fromLTWH(19, 15, 42, 51),
    BitHead.round => const Rect.fromLTWH(16, 17, 48, 47),
    BitHead.wide => const Rect.fromLTWH(9, 22, 62, 39),
    BitHead.square => const Rect.fromLTWH(16, 17, 48, 45),
  };

  double get headRadius => switch (head) {
    BitHead.classic => 18,
    BitHead.capsule => 21,
    BitHead.round => 23.5,
    BitHead.wide => 17,
    BitHead.square => 11,
  };

  double get eyeY {
    final r = headRect;
    return r.top + r.height * (head == BitHead.capsule ? .39 : .46);
  }

  double get mouthY {
    final r = headRect;
    final y = r.top + r.height * (head == BitHead.capsule ? .64 : .72) + (moustache ? 3 : 0);
    // Keep the mouth clear of a big goggle.
    return eyes == BitEyes.goggle ? (y > eyeY + 15.5 ? y : eyeY + 15.5) : y;
  }

  /// Half the distance between two eyes.
  double get eyeGap => switch (eyes) {
    BitEyes.goggles || BitEyes.glasses => 9.6,
    _ => head == BitHead.wide ? 11 : (head == BitHead.capsule ? 8.5 : 9),
  };

  /// How big the mood eyes are drawn, against the classic eye.
  double get eyeScale => switch (eyes) {
    BitEyes.beady => .72,
    BitEyes.big => 1.15,
    BitEyes.one => 1.45,
    BitEyes.goggles => .74,
    BitEyes.goggle => .95,
    BitEyes.glasses => .78,
    _ => 1,
  };

  /// The radius of a goggle or glasses lens, when there is one.
  double? get lens => switch (eyes) {
    BitEyes.goggles => 9.2,
    BitEyes.goggle => 11.5,
    BitEyes.glasses => 8,
    _ => null,
  };
}
