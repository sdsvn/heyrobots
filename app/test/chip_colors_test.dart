import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/ui/theme.dart';

void main() {
  testWidgets('a chip label is readable whether or not it is picked', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: buildTheme(Brightness.light),
        home: Scaffold(
          body: Row(
            children: [
              FilterChip(label: const Text('off'), selected: false, onSelected: (_) {}),
              FilterChip(label: const Text('on'), selected: true, onSelected: (_) {}),
            ],
          ),
        ),
      ),
    );
    Color colorOf(String t) => DefaultTextStyle.of(tester.element(find.text(t))).style.merge(tester.widget<Text>(find.text(t)).style).color!;
    final off = colorOf('off'), on = colorOf('on');
    expect(off, isNot(Colors.white), reason: 'an unpicked chip is white');
    expect(on, Colors.white, reason: 'a picked chip is the accent colour');
  });
}
