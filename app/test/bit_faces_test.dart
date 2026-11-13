import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/ui/agent_avatar.dart';
import 'package:heyrobots/ui/bit.dart';
import 'package:heyrobots/ui/theme.dart';

const _faces = [BitFace.classic, ...BitFace.family];

void main() {
  test('the family is 24 to 30 faces with their own names', () {
    expect(_faces.length, inInclusiveRange(24, 30));
    expect({for (final f in _faces) f.name}.length, _faces.length);
  });

  test('an agent always gets the same face, and agents get different ones', () {
    const ids = ['ag_general', 'ag_writer', 'ag_coder', 'ag_researcher', 'ag_cos'];
    for (final id in ids) {
      expect(identical(BitFace.of(id), BitFace.of(id)), isTrue);
    }
    expect({for (final id in ids) BitFace.of(id).name}.length, greaterThan(1));
    expect(BitFace.of(null), BitFace.classic);
    expect(BitFace.of('ag_general'), BitFace.classic);
  });

  testWidgets('every face draws in every mood, still and moving', (tester) async {
    for (final animate in [false, true]) {
      Bit.animate = animate;
      for (final brightness in Brightness.values) {
        await tester.pumpWidget(
          MaterialApp(
            theme: buildTheme(brightness),
            home: Wrap(
              children: [
                for (final f in _faces)
                  for (final m in BitMood.values) Bit(face: f, mood: m, size: 40),
              ],
            ),
          ),
        );
        // Walk through blinks, hops, waves and the look-around.
        for (var i = 0; i < 12; i++) {
          await tester.pump(const Duration(milliseconds: 397));
        }
        expect(tester.takeException(), isNull);
      }
    }
    await tester.pumpWidget(const SizedBox());
    Bit.animate = false;
  });

  testWidgets('avatars wear the face their id picks', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: buildTheme(Brightness.light),
        home: const Row(
          children: [
            AgentAvatar(color: 'sky', badge: 'pen', seed: 'ag_writer'),
            AgentAvatar(color: 'violet', badge: 'star'),
          ],
        ),
      ),
    );
    final bits = tester.widgetList<Bit>(find.byType(Bit)).toList();
    expect(bits[0].face, BitFace.of('ag_writer'));
    expect(bits[1].face, BitFace.classic);
  });

  // A contact sheet of every face, written to build/faces.png for a person
  // to look at.
  testWidgets('contact sheet', (tester) async {
    await tester.runAsync(() async {
      final font = FontLoader(fontFamily)..addFont(rootBundle.load('assets/fonts/Nunito.ttf'));
      await font.load();
    });
    const cell = 92.0;
    const colors = ['violet', 'peach', 'mint', 'sky', 'rose', 'sun'];
    final key = GlobalKey();
    tester.view.physicalSize = const Size(cell * 11 + 120, cell * 29);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    Widget row(BitFace f, int i) => SizedBox(
      height: cell,
      child: Row(
        children: [
          SizedBox(
            width: 120,
            child: Text(
              f.name,
              style: const TextStyle(
                fontFamily: fontFamily,
                fontSize: 18,
                fontWeight: FontWeight.w900,
                color: Color(0xFF2A2540),
                decoration: TextDecoration.none,
              ),
            ),
          ),
          for (final m in BitMood.values)
            SizedBox(
              width: cell,
              child: Center(
                child: Bit(face: f, mood: m, size: 76),
              ),
            ),
          for (final s in const [64.0, 40.0, 32.0])
            SizedBox(
              width: cell,
              child: Center(
                child: _Avatar(face: f, color: colors[i % colors.length], size: s),
              ),
            ),
          SizedBox(
            width: cell * 2,
            child: Theme(
              data: buildTheme(Brightness.dark),
              child: Container(
                color: Palette.dark.ground,
                child: Row(
                  children: [
                    SizedBox(
                      width: cell,
                      child: Center(
                        child: Bit(face: f, mood: BitMood.thinking, size: 70),
                      ),
                    ),
                    SizedBox(
                      width: cell,
                      child: Center(
                        child: _Avatar(face: f, color: colors[(i + 3) % colors.length], size: 48),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ],
      ),
    );

    await tester.pumpWidget(
      MaterialApp(
        debugShowCheckedModeBanner: false,
        theme: buildTheme(Brightness.light),
        home: RepaintBoundary(
          key: key,
          child: ColoredBox(
            color: Palette.light.ground,
            child: Column(children: [for (final (i, f) in _faces.indexed) row(f, i)]),
          ),
        ),
      ),
    );
    await tester.runAsync(() async {
      final boundary = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final image = await boundary.toImage(pixelRatio: 1);
      final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
      final file = File('build/faces.png');
      await file.parent.create(recursive: true);
      await file.writeAsBytes(bytes!.buffer.asUint8List());
    });
  });
}

/// An avatar with a given face, to show faces the hash may not reach.
class _Avatar extends StatelessWidget {
  const _Avatar({required this.face, required this.color, required this.size});

  final BitFace face;
  final String color;
  final double size;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final (strong, soft) = agentColors(color, p);
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: soft,
        shape: BoxShape.circle,
        border: Border.all(color: strong, width: size > 44 ? 3 : 2),
      ),
      alignment: Alignment.center,
      child: Bit(face: face, size: size * .74),
    );
  }
}
