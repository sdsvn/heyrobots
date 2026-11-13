import 'package:flutter/material.dart';

import '../api/session.dart';
import 'icons.dart';
import 'theme.dart';

/// The tab the main screen shows: 0 Chats, 1 Team, 2 Plans. Screens pushed
/// on top set it to send the person to a tab.
final homeTab = ValueNotifier<int>(0);

void showMessage(BuildContext context, String message) {
  ScaffoldMessenger.of(context)
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(content: Text(message)));
}

/// Loads something once and shows it, with a retry on failure.
class Loaded<T> extends StatefulWidget {
  const Loaded({super.key, required this.load, required this.builder});

  final Future<T> Function() load;
  final Widget Function(BuildContext context, T value, VoidCallback reload) builder;

  @override
  State<Loaded<T>> createState() => _LoadedState<T>();
}

class _LoadedState<T> extends State<Loaded<T>> {
  late Future<T> _future = widget.load();

  void _reload() => setState(() { _future = widget.load(); });

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<T>(
      future: _future,
      builder: (context, snap) {
        if (snap.hasError) {
          return ErrorPanel(message: describeError(snap.error!), onRetry: _reload);
        }
        if (!snap.hasData) {
          return const Center(child: CircularProgressIndicator());
        }
        return widget.builder(context, snap.data as T, _reload);
      },
    );
  }
}

class ErrorPanel extends StatelessWidget {
  const ErrorPanel({super.key, required this.message, this.onRetry});

  final String message;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(message, textAlign: TextAlign.center),
            if (onRetry != null) ...[
              const SizedBox(height: 12),
              OutlinedButton(onPressed: onRetry, child: const Text('Try again')),
            ],
          ],
        ),
      ),
    );
  }
}

class SectionHeader extends StatelessWidget {
  const SectionHeader(this.text, {super.key, this.padding = const EdgeInsets.fromLTRB(16, 20, 16, 8)});

  final String text;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: padding,
      child: Text(text, style: theme.textTheme.titleSmall?.copyWith(color: theme.colorScheme.primary)),
    );
  }
}

/// "just now", "5 min ago", "3 h ago", "yesterday", or a date.
String ago(DateTime at, {DateTime? now}) {
  final n = now ?? DateTime.now();
  final d = n.difference(at);
  if (d.inMinutes < 1) return 'just now';
  if (d.inMinutes < 60) return '${d.inMinutes} min ago';
  if (d.inHours < 24) return '${d.inHours} h ago';
  if (d.inDays < 2) return 'yesterday';
  final l = at.toLocal();
  return '${l.year}-${_two(l.month)}-${_two(l.day)}';
}

String clock(DateTime at) {
  final l = at.toLocal();
  return '${_two(l.hour)}:${_two(l.minute)}';
}

String _two(int n) => n.toString().padLeft(2, '0');

String usd(num v) => v < 0.01 && v > 0 ? '< \$0.01' : '\$${v.toStringAsFixed(2)}';

/// Ticket stages in plain words.
String stageLabel(String stage) => switch (stage) {
  'working' => 'Working',
  'waiting' => 'Waiting',
  'awaiting_approval' => 'Needs your approval',
  'awaiting_answer' => 'Has a question',
  'ready' => 'Done',
  'blocked' => 'Stuck',
  'failed' => 'Failed',
  'ended' => 'Ended',
  _ => stage,
};

/// A box with the design's solid bottom edge instead of a blurred shadow.
BoxDecoration edged(Color color, {double radius = 22, double depth = 3, Color? edge, Border? border}) => BoxDecoration(
  color: color,
  borderRadius: BorderRadius.circular(radius),
  border: border,
  boxShadow: edge == null || depth == 0 ? null : [BoxShadow(color: edge, offset: Offset(0, depth))],
);

/// Something pressable drawn with [edged], with an ink splash in its shape.
class EdgedBox extends StatelessWidget {
  const EdgedBox({
    super.key,
    required this.child,
    this.color,
    this.radius = 22,
    this.depth = 3,
    this.edge,
    this.border,
    this.padding = EdgeInsets.zero,
    this.onTap,
    this.width,
    this.height,
  });

  final Widget child;
  final Color? color, edge;
  final double radius, depth;
  final Border? border;
  final EdgeInsets padding;
  final VoidCallback? onTap;
  final double? width, height;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final shape = BorderRadius.circular(radius);
    return Container(
      width: width,
      height: height,
      decoration: edged(color ?? p.card, radius: radius, depth: depth, edge: edge ?? p.line, border: border),
      child: Material(
        type: MaterialType.transparency,
        borderRadius: shape,
        clipBehavior: Clip.antiAlias,
        child: InkWell(onTap: onTap, child: Padding(padding: padding, child: child)),
      ),
    );
  }
}

/// The 44 point back button at the top left of a pushed screen.
class BackTile extends StatelessWidget {
  const BackTile({super.key});

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Semantics(
      button: true,
      label: 'Back',
      child: EdgedBox(
        width: 44,
        height: 44,
        radius: 16,
        depth: 2,
        onTap: () => Navigator.of(context).maybePop(),
        child: Center(child: LineIcon(Glyph.back, size: 20, color: p.ink, stroke: 2.6)),
      ),
    );
  }
}

/// A screen's top row: back button, a 28 point title, and anything after.
class ScreenHeader extends StatelessWidget {
  const ScreenHeader(this.title, {super.key, this.trailing, this.padding = const EdgeInsets.fromLTRB(20, 20, 20, 10)});

  final String? title;
  final Widget? trailing;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: padding,
      child: Row(
        children: [
          const BackTile(),
          const SizedBox(width: 10),
          Expanded(child: title == null ? const SizedBox() : Text(title!, style: const TextStyle(fontSize: 28, fontWeight: FontWeight.w900, letterSpacing: -0.4, height: 1.2))),
          ?trailing,
        ],
      ),
    );
  }
}

/// The white search box under a screen's title.
class SearchBox extends StatelessWidget {
  const SearchBox({super.key, required this.hint, this.controller, this.onChanged, this.onSubmitted, this.fieldKey});

  final String hint;
  final TextEditingController? controller;
  final ValueChanged<String>? onChanged, onSubmitted;
  final Key? fieldKey;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Container(
      height: 46,
      padding: const EdgeInsets.symmetric(horizontal: 14),
      decoration: edged(p.card, radius: 16, depth: 2, edge: p.line),
      child: Row(
        children: [
          LineIcon(Glyph.search, size: 18, color: p.muted, stroke: 2.4),
          const SizedBox(width: 10),
          Expanded(
            child: TextField(
              key: fieldKey,
              controller: controller,
              onChanged: onChanged,
              onSubmitted: onSubmitted,
              textInputAction: TextInputAction.search,
              style: TextStyle(fontSize: 15, color: p.ink),
              decoration: InputDecoration(
                hintText: hint,
                hintStyle: TextStyle(fontSize: 15, color: p.muted, fontWeight: FontWeight.w400),
                filled: false,
                isDense: true,
                border: InputBorder.none,
                enabledBorder: InputBorder.none,
                focusedBorder: InputBorder.none,
                contentPadding: EdgeInsets.zero,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// A small uppercase accent heading over a card, such as INSTALLED.
class CapsLabel extends StatelessWidget {
  const CapsLabel(this.text, {super.key, this.padding = const EdgeInsets.symmetric(horizontal: 6)});

  final String text;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: padding,
      child: Text(
        text.toUpperCase(),
        style: TextStyle(fontSize: 13, fontWeight: FontWeight.w800, letterSpacing: .6, color: Palette.of(context).accent),
      ),
    );
  }
}

/// The wide pill button at the bottom of a screen: accent for the main
/// step, white for a quieter one.
class BigButton extends StatelessWidget {
  const BigButton({super.key, required this.label, this.onPressed, this.leading, this.trailing, this.primary = true, this.busy = false});

  final String label;
  final VoidCallback? onPressed;
  final Glyph? leading, trailing;
  final bool primary, busy;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final fg = primary ? Colors.white : p.accent;
    final enabled = onPressed != null && !busy;
    return Opacity(
      opacity: enabled || busy ? 1 : .5,
      child: EdgedBox(
        height: primary ? 56 : 52,
        radius: 999,
        depth: primary ? 4 : 3,
        color: primary ? p.accent : p.card,
        edge: primary ? p.accentDeep : p.line,
        onTap: enabled ? onPressed : null,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            if (busy)
              SizedBox.square(dimension: 20, child: CircularProgressIndicator(strokeWidth: 2.6, color: fg))
            else ...[
              if (leading != null) ...[LineIcon(leading!, size: 20, color: fg, stroke: 2.4), const SizedBox(width: 10)],
              // Long labels shrink a little rather than lose their end.
              Flexible(
                child: FittedBox(
                  fit: BoxFit.scaleDown,
                  child: Text(label, maxLines: 1, style: TextStyle(color: fg, fontWeight: FontWeight.w900, fontSize: primary ? 16 : 15)),
                ),
              ),
              if (trailing != null) ...[const SizedBox(width: 10), LineIcon(trailing!, size: 20, color: fg, stroke: 2.6)],
            ],
          ],
        ),
      ),
    );
  }
}

/// A 34 point filter chip: white with a soft edge, accent when chosen.
class PillChip extends StatelessWidget {
  const PillChip(this.label, {super.key, required this.selected, required this.onTap, this.fontSize = 13});

  final String label;
  final bool selected;
  final VoidCallback onTap;
  final double fontSize;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Semantics(
      selected: selected,
      button: true,
      child: EdgedBox(
        height: 34,
        radius: 999,
        depth: 2,
        color: selected ? p.accent : p.card,
        edge: selected ? p.accentDeep : p.line,
        onTap: onTap,
        padding: const EdgeInsets.symmetric(horizontal: 13),
        child: Center(
          widthFactor: 1,
          child: Text(label, style: TextStyle(fontSize: fontSize, fontWeight: FontWeight.w800, color: selected ? Colors.white : p.ink)),
        ),
      ),
    );
  }
}

/// A rounded square holding an icon in one of the design's tints.
class IconTile extends StatelessWidget {
  const IconTile(this.glyph, {super.key, required this.tint, this.size = 44, this.radius = 14, this.iconSize = 24});

  final Glyph glyph;
  final String tint;
  final double size, radius, iconSize;

  @override
  Widget build(BuildContext context) {
    final (bg, ink) = tintColors(tint, Palette.of(context));
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(radius)),
      alignment: Alignment.center,
      child: LineIcon(glyph, size: iconSize, color: ink),
    );
  }
}

/// The 46 by 28 switch from the mockups.
class Toggle extends StatelessWidget {
  const Toggle({super.key, required this.value, required this.onChanged, this.label});

  final bool value;
  final ValueChanged<bool>? onChanged;
  final String? label;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Semantics(
      toggled: value,
      label: label,
      child: GestureDetector(
        onTap: onChanged == null ? null : () => onChanged!(!value),
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          width: 46,
          height: 28,
          padding: const EdgeInsets.all(3),
          alignment: value ? Alignment.centerRight : Alignment.centerLeft,
          decoration: BoxDecoration(color: value ? p.accent : p.switchOff, borderRadius: BorderRadius.circular(999)),
          child: const DecoratedBox(
            decoration: BoxDecoration(color: Colors.white, shape: BoxShape.circle),
            child: SizedBox.square(dimension: 22),
          ),
        ),
      ),
    );
  }
}

/// A bold line over a muted one.
class TwoLines extends StatelessWidget {
  const TwoLines(this.title, this.subtitle, {super.key, this.titleSize = 15, this.subtitleSize = 13, this.titleWeight = FontWeight.w900, this.subtitleColor});

  final String title;
  final String? subtitle;
  final double titleSize, subtitleSize;
  final FontWeight titleWeight;
  final Color? subtitleColor;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(title, style: TextStyle(fontSize: titleSize, fontWeight: titleWeight, color: p.ink)),
        if (subtitle != null && subtitle!.isNotEmpty) Text(subtitle!, style: TextStyle(fontSize: subtitleSize, color: subtitleColor ?? p.muted)),
      ],
    );
  }
}

/// A white card of rows split by hairlines.
class RowsCard extends StatelessWidget {
  const RowsCard({super.key, required this.children, this.header});

  final List<Widget> children;
  final Widget? header;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return DecoratedBox(
      decoration: edged(p.card, edge: p.line),
      child: Material(
        type: MaterialType.transparency,
        borderRadius: BorderRadius.circular(22),
        clipBehavior: Clip.antiAlias,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            ?header,
            for (final (i, c) in children.indexed) ...[
              if (i > 0) Divider(height: 1, thickness: 1, color: p.hairline),
              c,
            ],
          ],
        ),
      ),
    );
  }
}

/// A small white ring in an agent's color, standing for the agent.
class AgentRing extends StatelessWidget {
  const AgentRing({super.key, required this.color, this.size = 20});

  final Color color;
  final double size;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: Colors.white, shape: BoxShape.circle, border: Border.all(color: color, width: 2)),
    );
  }
}

/// A sheet with a title, a few lines and fields, and one button. Returns
/// the fields' values, or null when dismissed.
Future<List<String>?> askInSheet(
  BuildContext context, {
  required String title,
  String? body,
  List<String> steps = const [],
  required List<({String label, bool secret, String? initial, Key? key})> fields,
  String action = 'Save',
}) {
  final controllers = [for (final f in fields) TextEditingController(text: f.initial ?? '')];
  return showModalBottomSheet<List<String>>(
    context: context,
    isScrollControlled: true,
    builder: (context) {
      final p = Palette.of(context);
      return Padding(
        padding: EdgeInsets.fromLTRB(20, 0, 20, 20 + MediaQuery.viewInsetsOf(context).bottom),
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(title, style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w900, letterSpacing: -0.3)),
              if (body != null) ...[const SizedBox(height: 6), Text(body, style: TextStyle(fontSize: 14, color: p.muted))],
              for (final (i, s) in steps.indexed)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      SizedBox(width: 22, child: Text('${i + 1}.', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w900, color: p.accent))),
                      Expanded(child: Text(s, style: TextStyle(fontSize: 14, color: p.ink))),
                    ],
                  ),
                ),
              for (final (i, f) in fields.indexed) ...[
                const SizedBox(height: 12),
                TextField(
                  key: f.key,
                  controller: controllers[i],
                  obscureText: f.secret,
                  autofocus: i == 0,
                  decoration: InputDecoration(labelText: f.label),
                ),
              ],
              const SizedBox(height: 18),
              BigButton(
                label: action,
                onPressed: () => Navigator.pop(context, [for (final c in controllers) c.text.trim()]),
              ),
            ],
          ),
        ),
      );
    },
  );
}

/// One choice in a row of two or three, as on a pill track.
class Segment extends StatelessWidget {
  const Segment({super.key, required this.label, required this.selected, required this.onTap});

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Semantics(
      selected: selected,
      button: true,
      child: EdgedBox(
        height: 40,
        radius: 999,
        depth: selected ? 2 : 0,
        color: selected ? p.accent : Colors.transparent,
        edge: p.accentDeep,
        onTap: onTap,
        child: Center(child: Text(label, style: TextStyle(fontSize: 13, fontWeight: FontWeight.w900, color: selected ? Colors.white : p.muted))),
      ),
    );
  }
}
