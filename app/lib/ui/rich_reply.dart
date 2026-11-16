import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:a2ui_core/a2ui_core.dart';
import 'package:a2ui_flutter/a2ui_flutter.dart';
import 'package:a2ui_flutter/basic_catalog.dart';
import 'package:flutter/material.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
import 'package:http/http.dart' as http;
import 'package:markdown/markdown.dart' as md;
import 'package:pdfrx/pdfrx.dart';
import 'package:url_launcher/url_launcher.dart';

import '../api/session.dart';
import '../state.dart';
import 'chat_kit.dart';
import 'common.dart';
import 'theme.dart';

/// An agent's reply text, rendered as markdown in the chat's own style.
class ReplyText extends StatelessWidget {
  const ReplyText(this.text, {super.key, this.color, this.selectable = true, this.style});

  final String text;
  final Color? color;

  /// The base style, when something else sets it, such as a UI's heading.
  final TextStyle? style;

  /// Off inside UI, where selecting would swallow a button's tap.
  final bool selectable;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final ink = color ?? p.ink;
    final body = TextStyle(fontFamily: fontFamily, fontSize: 15, height: 1.4, color: ink).merge(style);
    final code = TextStyle(fontFamily: 'monospace', fontFamilyFallback: const ['Menlo', 'Courier'], fontSize: 13.5, color: ink);
    final sheet = MarkdownStyleSheet(
      p: body,
      a: body.copyWith(color: p.accent, fontWeight: FontWeight.w800, decoration: TextDecoration.underline, decorationColor: p.accent),
      h1: body.copyWith(fontSize: 21, fontWeight: FontWeight.w900, height: 1.25),
      h2: body.copyWith(fontSize: 18, fontWeight: FontWeight.w900, height: 1.25),
      h3: body.copyWith(fontSize: 16, fontWeight: FontWeight.w900),
      h4: body.copyWith(fontWeight: FontWeight.w900),
      h5: body.copyWith(fontWeight: FontWeight.w900),
      h6: body.copyWith(fontWeight: FontWeight.w800, color: p.muted),
      strong: const TextStyle(fontWeight: FontWeight.w900),
      em: const TextStyle(fontStyle: FontStyle.italic),
      del: const TextStyle(decoration: TextDecoration.lineThrough),
      listBullet: body,
      blockSpacing: 8,
      listIndent: 22,
      code: code.copyWith(backgroundColor: p.ground),
      codeblockPadding: const EdgeInsets.all(10),
      codeblockDecoration: BoxDecoration(color: p.ground, borderRadius: BorderRadius.circular(12), border: Border.all(color: p.line)),
      blockquote: body.copyWith(color: p.muted),
      blockquotePadding: const EdgeInsets.fromLTRB(12, 4, 8, 4),
      blockquoteDecoration: BoxDecoration(border: Border(left: BorderSide(color: p.accentSoft, width: 4))),
      tableHead: body.copyWith(fontWeight: FontWeight.w900),
      tableBody: body,
      tableBorder: TableBorder.all(color: p.line),
      tableCellsPadding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      tableColumnWidth: const IntrinsicColumnWidth(),
      horizontalRuleDecoration: BoxDecoration(border: Border(top: BorderSide(color: p.line, width: 2))),
    );
    return MarkdownBody(
      data: hardBreaks(text),
      selectable: selectable,
      styleSheet: sheet,
      onTapLink: (label, href, _) {
        if (href != null) openLink(context, label, href);
      },
      // ignore: deprecated_member_use
      imageBuilder: (uri, _, alt) => uri.scheme == 'https'
          ? InlineImage(url: uri.toString(), label: alt, fallback: _altLink(context, uri, alt))
          : _altLink(context, uri, alt),
    );
  }

  Widget _altLink(BuildContext context, Uri uri, String? alt) {
    final p = Palette.of(context);
    final label = (alt ?? '').trim().isEmpty ? uri.toString() : alt!;
    return InkWell(
      onTap: () => openLink(context, label, uri.toString()),
      child: Text(label, style: TextStyle(fontSize: 15, color: p.accent, fontWeight: FontWeight.w800, decoration: TextDecoration.underline, decorationColor: p.accent)),
    );
  }
}

/// Keeps a reply's single line breaks, as plain text showed them, by making
/// each one a markdown hard break; code blocks stay as they are.
String hardBreaks(String text) {
  final lines = text.split('\n');
  var fence = false;
  for (var i = 0; i < lines.length - 1; i++) {
    final t = lines[i].trimLeft();
    if (t.startsWith('```') || t.startsWith('~~~')) fence = !fence;
    if (!fence && t.isNotEmpty && lines[i + 1].trim().isNotEmpty && !t.startsWith('```') && !t.startsWith('~~~')) lines[i] = '${lines[i]}  ';
  }
  return lines.join('\n');
}

/// A reply's words without its markdown, for one-line previews.
String plainText(String text) {
  final out = StringBuffer();
  const blocks = {'p', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'li', 'blockquote', 'pre', 'th', 'td', 'br'};
  void walk(md.Node n) {
    if (n is md.Text) {
      out.write(n.text);
    } else if (n is md.Element) {
      if (n.tag == 'img') out.write(n.attributes['alt'] ?? '');
      n.children?.forEach(walk);
      if (blocks.contains(n.tag)) out.write(' ');
    }
  }

  md.Document(extensionSet: md.ExtensionSet.gitHubFlavored).parse(text).forEach(walk);
  return out.toString().replaceAll(RegExp(r'\s+'), ' ').trim();
}

/// Opens a link from a reply in the browser. Replies can carry text from web
/// pages, so a link whose text says something other than where it goes shows
/// the real address first.
Future<void> openLink(BuildContext context, String label, String href) async {
  final uri = Uri.tryParse(href);
  if (uri == null || !const {'http', 'https', 'mailto'}.contains(uri.scheme)) {
    showMessage(context, "This link can't be opened: $href");
    return;
  }
  if (!linkSaysWhereItGoes(label, href)) {
    final p = Palette.of(context);
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Open this link?'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('It says “$label” but goes to:', style: TextStyle(color: p.muted)),
            const SizedBox(height: 8),
            SelectableText(href, key: const Key('link-target'), style: TextStyle(fontWeight: FontWeight.w800, color: p.ink)),
          ],
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(key: const Key('open-link'), onPressed: () => Navigator.pop(context, true), child: const Text('Open')),
        ],
      ),
    );
    if (ok != true) return;
  }
  if (!await launchUrl(uri, mode: LaunchMode.externalApplication) && context.mounted) showMessage(context, 'Open $href in your browser.');
}

/// Whether a link's text is its own address, give or take the scheme,
/// "www." and a trailing slash, so opening it needs no warning.
bool linkSaysWhereItGoes(String label, String href) {
  String bare(String s) {
    var t = s.trim().toLowerCase();
    for (final pre in const ['https://', 'http://', 'mailto:']) {
      if (t.startsWith(pre)) t = t.substring(pre.length);
    }
    if (t.startsWith('www.')) t = t.substring(4);
    while (t.endsWith('/')) {
      t = t.substring(0, t.length - 1);
    }
    return t;
  }

  return bare(label) == bare(href);
}

/// A picture in a reply: at most the bubble's width and 320 points tall, and
/// full screen on a tap. [fallback] shows when it doesn't load.
class InlineImage extends StatelessWidget {
  const InlineImage({super.key, required this.url, required this.fallback, this.label, this.width});

  final String url;
  final String? label;
  final Widget fallback;
  final double? width;

  @override
  Widget build(BuildContext context) {
    return LimitedBox(
      maxWidth: 360,
      child: GestureDetector(
        onTap: () => showImage(context, Image.network(url, webHtmlElementStrategy: WebHtmlElementStrategy.fallback), label),
        child: MouseRegion(
          cursor: SystemMouseCursors.click,
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 320),
            child: ClipRRect(
              borderRadius: BorderRadius.circular(14),
              child: Image.network(
                url,
                // In the browser app, a site without CORS headers still shows
                // its picture, drawn by the page instead of fetched.
                webHtmlElementStrategy: WebHtmlElementStrategy.fallback,
                width: width,
                fit: BoxFit.contain,
                alignment: Alignment.centerLeft,
                semanticLabel: label,
                errorBuilder: (_, _, _) => fallback,
                loadingBuilder: (context, child, progress) => progress == null ? child : SizedBox(width: width ?? 200, height: 120, child: const Center(child: CircularProgressIndicator(strokeWidth: 2))),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Shows a picture full screen, with pinch and wheel zoom.
void showImage(BuildContext context, Widget image, String? label) {
  Navigator.of(context).push(
    MaterialPageRoute<void>(
      fullscreenDialog: true,
      builder: (context) => Scaffold(
        backgroundColor: Colors.black,
        appBar: AppBar(
          backgroundColor: Colors.black,
          foregroundColor: Colors.white,
          title: Text(label ?? '', style: const TextStyle(color: Colors.white)),
        ),
        body: InteractiveViewer(maxScale: 8, child: Center(child: image)),
      ),
    ),
  );
}

/// The short-lived links to shared files, kept for a while so redrawing the
/// chat doesn't ask the server again.
final _links = <String, ({Uri url, DateTime until})>{};

Future<Uri> fileUrl(AppState s, SharedFile f) async {
  final now = DateTime.now();
  final had = _links[f.id];
  if (had != null && now.isBefore(had.until)) return had.url;
  final link = await s.api.createFileLink(fileId: f.id);
  final url = s.server!.resolve(link.url);
  // A minute short of the server's end, so a link never runs out mid-load.
  _links[f.id] = (url: url, until: link.expiresAt.toLocal().subtract(const Duration(minutes: 1)));
  return url;
}

bool isImage(SharedFile f) => const {'image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/bmp'}.contains(f.type);
bool isPdf(SharedFile f) => f.type == 'application/pdf';

/// A shared file as the chat shows it: a picture, a PDF's first page, or
/// the plain tile, with the Download button under it.
class SharedFileView extends StatefulWidget {
  const SharedFileView({super.key, required this.file, required this.state, required this.onDownload, required this.width});

  final SharedFile file;
  final AppState state;
  final VoidCallback onDownload;
  final double width;

  @override
  State<SharedFileView> createState() => _SharedFileViewState();
}

class _SharedFileViewState extends State<SharedFileView> {
  Future<Uri>? _url;
  Future<_Pdf>? _pdf;

  @override
  void initState() {
    super.initState();
    final f = widget.file;
    if (isImage(f) || isPdf(f)) _url = fileUrl(widget.state, f);
    if (isPdf(f)) _pdf = _url!.then((u) => _Pdf.load(u, (widget.width * 2).round()));
  }

  @override
  void dispose() {
    _pdf?.then((d) => d.dispose(), onError: (_) {});
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final f = widget.file;
    final tile = FileTile(file: f, onDownload: widget.onDownload);
    Widget? preview;
    if (isImage(f)) {
      preview = FutureBuilder<Uri>(
        future: _url,
        builder: (context, snap) => switch (snap) {
          AsyncSnapshot(hasData: true, :final data?) => InlineImage(
            key: Key('image:${f.id}'),
            url: data.toString(),
            label: f.name,
            width: widget.width,
            fallback: const SizedBox.shrink(),
          ),
          AsyncSnapshot(hasError: true) => const SizedBox.shrink(),
          _ => SizedBox(width: widget.width, height: 120, child: const Center(child: CircularProgressIndicator(strokeWidth: 2))),
        },
      );
    } else if (isPdf(f)) {
      preview = FutureBuilder<_Pdf>(
        future: _pdf,
        builder: (context, snap) => switch (snap) {
          AsyncSnapshot(hasData: true, :final data?) => _PdfPreview(key: Key('pdf:${f.id}'), pdf: data, name: f.name, width: widget.width),
          AsyncSnapshot(hasError: true) => const SizedBox.shrink(),
          _ => SizedBox(width: widget.width, height: 160, child: const Center(child: CircularProgressIndicator(strokeWidth: 2))),
        },
      );
    }
    if (preview == null) return tile;
    return Column(crossAxisAlignment: CrossAxisAlignment.start, mainAxisSize: MainAxisSize.min, children: [preview, const SizedBox(height: 6), tile]);
  }
}

/// A shared PDF, downloaded once: its bytes for the viewer, and its first
/// page drawn for the chat.
class _Pdf {
  _Pdf(this.bytes, this.pages, this.cover);

  final Uint8List bytes;
  final int pages;
  final ui.Image cover;

  static Future<_Pdf> load(Uri url, int pixelWidth) async {
    final res = await http.get(url);
    if (res.statusCode != 200) throw Exception('download failed: ${res.statusCode}');
    final bytes = res.bodyBytes;
    await pdfrxFlutterInitialize();
    final doc = await PdfDocument.openData(Uint8List.fromList(bytes), sourceName: url.path);
    try {
      final page = doc.pages.first;
      final w = pixelWidth.toDouble();
      final image = await page.renderForDisplay(fullWidth: w, fullHeight: w * page.height / page.width, backgroundColor: 0xFFFFFFFF);
      if (image == null) throw Exception("the first page didn't render");
      return _Pdf(bytes, doc.pages.length, image);
    } finally {
      await doc.dispose();
    }
  }

  void dispose() => cover.dispose();
}

class _PdfPreview extends StatelessWidget {
  const _PdfPreview({super.key, required this.pdf, required this.name, required this.width});

  final _Pdf pdf;
  final String name;
  final double width;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final w = width.clamp(0, 320).toDouble();
    return GestureDetector(
      onTap: () => Navigator.of(context).push(
        MaterialPageRoute<void>(
          fullscreenDialog: true,
          builder: (context) => Scaffold(
            appBar: AppBar(title: Text(name)),
            body: PdfViewer.data(pdf.bytes, sourceName: name),
          ),
        ),
      ),
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        child: Stack(
          children: [
            Container(
              width: w,
              constraints: const BoxConstraints(maxHeight: 320),
              decoration: BoxDecoration(borderRadius: BorderRadius.circular(14), border: Border.all(color: p.line)),
              clipBehavior: Clip.antiAlias,
              child: RawImage(image: pdf.cover, width: w, fit: BoxFit.cover, alignment: Alignment.topCenter),
            ),
            Positioned(
              right: 8,
              bottom: 8,
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                decoration: BoxDecoration(color: Colors.black.withValues(alpha: 0.6), borderRadius: BorderRadius.circular(999)),
                child: Text(pdf.pages == 1 ? '1 page' : '${pdf.pages} pages', style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: Colors.white)),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The basic A2UI catalog as the chat allows it: no video or audio players,
/// and pictures only from https addresses.
final CatalogImplementation chatCatalog = () {
  final base = basicCatalog();
  final image = BasicComponents.image;
  final safeImage = ComponentImplementation(
    name: image.name,
    schema: image.schema,
    allowedParents: image.allowedParents,
    allowedChildren: image.allowedChildren,
    builder: (context, node, props, buildChild) =>
        (props.string('url') ?? '').startsWith('https://') ? image.builder(context, node, props, buildChild) : const Icon(Icons.broken_image),
  );
  return base.copyWith(
    components: [
      for (final c in base.components.values)
        if (c.name == 'Image') safeImage else if (c.name != 'Video' && c.name != 'AudioPlayer') c,
    ],
  );
}();

/// An A2UI surface an agent sent. Its buttons' actions come back through
/// [onAction]; [fallback] shows when the surface can't be drawn.
class UiSurface extends StatefulWidget {
  const UiSurface({super.key, required this.messages, required this.onAction, required this.fallback});

  final List<Object?> messages;
  final void Function(A2uiRendererAction) onAction;
  final Widget fallback;

  @override
  State<UiSurface> createState() => _UiSurfaceState();
}

class _UiSurfaceState extends State<UiSurface> {
  late final MessageProcessor<ComponentImplementation> _processor = MessageProcessor(catalogs: [chatCatalog]);
  SurfaceModel<ComponentImplementation>? _surface;
  bool _failed = false;
  final _subs = <void Function()>[];

  @override
  void initState() {
    super.initState();
    _subs.add(_processor.model.onSurfaceCreated.subscribe((s) => _surface ??= s).unsubscribe);
    _subs.add(_processor.model.onAction.subscribe(widget.onAction).unsubscribe);
    try {
      final errors = _processor.processMessagesWithErrors(jsonDecode(jsonEncode(widget.messages)));
      _failed = errors.isNotEmpty || _surface == null;
    } catch (_) {
      _failed = true;
    }
  }

  @override
  void dispose() {
    for (final u in _subs) {
      u();
    }
    _processor.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final surface = _surface;
    if (_failed || surface == null) return widget.fallback;
    return A2uiMarkdown(
      builder: (context, markdown, style) => ReplyText(markdown, style: style, selectable: false),
      child: A2uiSurface(surface: surface),
    );
  }
}

/// What a tapped button sends back to the agent, as a message in the chat.
String actionMessage(A2uiRendererAction a) {
  final ctx = a.context.entries.where((e) => e.value != null && '${e.value}'.isNotEmpty).map((e) => '${e.key}: ${e.value}').join(', ');
  return a.userMessage ?? (ctx.isEmpty ? a.name : '${a.name} ($ctx)');
}
