import 'package:a2ui_core/a2ui_core.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:heyrobots/ui/rich_reply.dart';
import 'package:heyrobots/ui/theme.dart';

Widget _wrap(Widget child) => MaterialApp(theme: buildTheme(Brightness.light), home: Scaffold(body: SingleChildScrollView(child: child)));

void main() {
  test('a link that says where it goes needs no warning', () {
    expect(linkSaysWhereItGoes('go.dev', 'https://go.dev/'), isTrue);
    expect(linkSaysWhereItGoes('https://www.Example.com', 'https://example.com'), isTrue);
    expect(linkSaysWhereItGoes('your bank', 'https://evil.example'), isFalse);
    expect(linkSaysWhereItGoes('bank.com', 'https://bank.com.evil.example'), isFalse);
  });

  test('a preview shows the words without the markdown', () {
    expect(plainText('## Go 1.27.4 is out\n\nReleased **2026-11-10**, see [notes](https://go.dev).'), 'Go 1.27.4 is out Released 2026-11-10, see notes.');
  });

  testWidgets('a reply keeps its single line breaks', (tester) async {
    await tester.pumpWidget(_wrap(const ReplyText('Name: Ann\nDate: Friday\n\n```\na\nb\n```')));
    final text = tester.widgetList<SelectableText>(find.byType(SelectableText)).map((t) => t.textSpan?.toPlainText() ?? t.data).join('|');
    expect(text, contains('Ann\nDate: Friday'));
    expect(text, contains('a\nb'));
    expect(text, isNot(contains('b  ')));
  });

  testWidgets('a reply shows its markdown formatted, not raw', (tester) async {
    await tester.pumpWidget(_wrap(const ReplyText('## Plan\n\n- **one**\n- two\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n`code`')));
    final text = tester.widgetList<SelectableText>(find.byType(SelectableText)).map((t) => t.textSpan?.toPlainText() ?? t.data).join('|');
    expect(text, contains('Plan'));
    expect(text, contains('one'));
    expect(text, isNot(contains('**')));
    expect(text, isNot(contains('##')));
    expect(find.byType(Table), findsOneWidget);
  });

  test("the chat's catalog has no video or audio players", () {
    expect(chatCatalog.components.keys, containsAll(['Button', 'Card', 'Text', 'Image']));
    expect(chatCatalog.components.keys, isNot(contains('Video')));
    expect(chatCatalog.components.keys, isNot(contains('AudioPlayer')));
  });

  testWidgets("a surface's button sends its action back", (tester) async {
    String? sent;
    final messages = <Object?>[
      {'version': '1.0', 'createSurface': {'surfaceId': 's1', 'catalogId': chatCatalog.id}},
      {
        'version': '1.0',
        'updateComponents': {
          'surfaceId': 's1',
          'components': [
            {'id': 'root', 'component': 'Column', 'children': ['q', 'b']},
            {'id': 'q', 'component': 'Text', 'text': 'Book the 7pm table?'},
            {'id': 'b', 'component': 'Button', 'child': 'bl', 'action': {'event': {'name': 'book', 'context': {'time': '19:00'}}}},
            {'id': 'bl', 'component': 'Text', 'text': '**Book** it'},
          ],
        },
      },
    ];
    await tester.pumpWidget(_wrap(UiSurface(messages: messages, onAction: (m) => sent = m, fallback: const Text('fallback'))));
    await tester.pumpAndSettle();
    expect(find.text('fallback'), findsNothing);
    await tester.tap(find.text('Book it'));
    await tester.pumpAndSettle();
    // The label, as the person saw it; the time the agent wrote into the
    // button it already knows.
    expect(sent, 'Book it');
  });

  test('a tap sends what the person filled in, not what the agent wrote', () {
    final messages = [
      {
        'updateComponents': {
          'components': [
            {'id': 'b', 'component': 'Button', 'child': 'l', 'action': {'event': {'name': 'book', 'context': {'time': '19:00', 'name': {'path': '/name'}}}}},
            {'id': 'l', 'component': 'Text', 'text': 'Book it'},
          ],
        },
      },
    ];
    final a = A2uiRendererAction(name: 'book', surfaceId: 's', sourceComponentId: 'b', timestamp: DateTime.now(), context: {'time': '19:00', 'name': 'Ann'});
    expect(actionMessage(a, messages), 'Book it (name: Ann)');
  });

  testWidgets("a surface that can't be drawn shows its text", (tester) async {
    await tester.pumpWidget(_wrap(UiSurface(messages: const [{'nonsense': true}], onAction: (_) {}, fallback: const Text('fallback'))));
    expect(find.text('fallback'), findsOneWidget);
  });
}
