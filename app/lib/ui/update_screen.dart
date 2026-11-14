import 'dart:async';

import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../api/session.dart';
import '../pairing.dart';
import '../relaunch.dart' if (dart.library.js_interop) '../relaunch_web.dart';
import '../state.dart';
import 'common.dart';
import 'theme.dart';

/// The release this app was built from, set by the release build.
const appVersion = String.fromEnvironment('HEYROBOTS_VERSION', defaultValue: 'dev');

/// The settings row's second line for an update status.
String updateLine(UpdateStatus u) {
  if (u.state != 'idle' && u.state != 'failed') return 'Updating…';
  if (u.newer) return '${u.latest} is out';
  return '${u.version} · up to date';
}

/// Whether this Mac app runs beside its server, so a server update
/// replaces it too.
bool besideServer(AppState s) => isMacApp && s.server != null && isLoopbackHost(s.server!.host);

/// Whether the Mac app beside the server is older than the server it was
/// updated with, and so should be restarted to finish.
bool needsRelaunch(AppState s, UpdateStatus u) => besideServer(s) && appVersion != 'dev' && u.version != 'dev' && u.version != appVersion && u.state == 'idle';

/// Updates: which version runs, whether a newer one is out, and one button
/// that installs it on the computer (the server and, on a Mac, the app).
class UpdateScreen extends StatefulWidget {
  const UpdateScreen({super.key, required this.state});

  final AppState state;

  @override
  State<UpdateScreen> createState() => _UpdateScreenState();
}

class _UpdateScreenState extends State<UpdateScreen> {
  UpdateStatus? _u;
  bool _checking = false, _lost = false;
  String? _from;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _check(fresh: false);
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _check({bool fresh = true}) async {
    setState(() => _checking = true);
    try {
      final u = await widget.state.api.getUpdate(check: fresh);
      if (!mounted) return;
      setState(() => _u = u);
      if (u.state != 'idle' && u.state != 'failed') _follow();
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    } finally {
      if (mounted) setState(() => _checking = false);
    }
  }

  Future<void> _start() async {
    _from = _u?.version;
    try {
      final u = await widget.state.api.startUpdate();
      if (!mounted) return;
      setState(() => _u = u);
      _follow();
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    }
  }

  /// Follows an update until the server is back as the new version, or it
  /// failed. While it restarts the server doesn't answer; that's expected.
  void _follow() {
    _poll?.cancel();
    _poll = Timer.periodic(const Duration(seconds: 2), (_) async {
      try {
        final u = await widget.state.api.getUpdate();
        if (!mounted) return;
        setState(() {
          _u = u;
          _lost = false;
        });
        if (u.state == 'failed' || (u.state == 'idle' && u.version != _from)) _poll?.cancel();
      } catch (_) {
        if (mounted) setState(() => _lost = true);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final u = _u;
    final owner = widget.state.isOwner;
    final busy = u != null && u.state != 'idle' && u.state != 'failed';
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const ScreenHeader('Updates'),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
                children: [
                  if (u == null)
                    const Padding(
                      padding: EdgeInsets.all(32),
                      child: Center(child: CircularProgressIndicator()),
                    )
                  else ...[
                    Container(
                      decoration: edged(p.card, edge: p.line),
                      padding: const EdgeInsets.all(16),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          TwoLines(
                            busy ? 'Updating to ${u.latest}' : (u.newer ? '${u.latest} is out' : 'Up to date'),
                            'This computer runs ${u.version}${appVersion != u.version ? ' · this app is $appVersion' : ''}',
                            titleSize: 20,
                          ),
                          if (busy) ...[
                            const SizedBox(height: 12),
                            Text(
                              key: const Key('update:progress'),
                              _lost || u.state == 'restarting'
                                  ? 'Restarting. It comes back by itself in a few seconds.'
                                  : u.state == 'waiting'
                                  ? 'Waiting for ${u.working ?? 0} running task${u.working == 1 ? '' : 's'} to finish (up to two minutes).'
                                  : 'Downloading and checking it…',
                              style: TextStyle(color: p.muted),
                            ),
                            const SizedBox(height: 10),
                            const LinearProgressIndicator(),
                          ],
                          if (u.state == 'failed' && u.error != null) ...[
                            const SizedBox(height: 12),
                            Text(
                              "The update didn't go through: ${u.error}",
                              key: const Key('update:error'),
                              style: TextStyle(color: p.danger),
                            ),
                          ],
                          if (u.newer && !busy && u.notes != null && u.notes!.trim().isNotEmpty) ...[
                            const SizedBox(height: 12),
                            Text(
                              "What's new",
                              style: TextStyle(fontWeight: FontWeight.w900, color: p.ink),
                            ),
                            const SizedBox(height: 4),
                            Text(u.notes!.trim(), style: TextStyle(color: p.ink, height: 1.4)),
                          ],
                          if (u.newer && !busy && !u.canUpdate && u.why != null) ...[const SizedBox(height: 12), Text(u.why!, style: TextStyle(color: p.muted))],
                        ],
                      ),
                    ),
                    const SizedBox(height: 16),
                    if (needsRelaunch(widget.state, u)) ...[
                      BigButton(
                        key: const Key('update:relaunch'),
                        label: 'Restart heyrobots to finish',
                        onPressed: () async {
                          if (!await relaunch() && context.mounted) showMessage(context, 'Quit heyrobots and open it again to finish.');
                        },
                      ),
                      const SizedBox(height: 12),
                    ],
                    if (u.canUpdate && owner && !busy) ...[
                      BigButton(key: const Key('update:start'), label: u.state == 'failed' ? 'Try again' : 'Update this computer', onPressed: _start),
                      const SizedBox(height: 8),
                      Text(
                        (u.working ?? 0) > 0
                            ? '${u.working} task${u.working == 1 ? ' is' : 's are'} running; the update waits up to two minutes for them, and anything still going picks up again after.'
                            : 'Updates the server${besideServer(widget.state) ? ' and this app' : ', and the Mac app if it is installed there'}. Takes a minute.',
                        textAlign: TextAlign.center,
                        style: TextStyle(fontSize: 13, color: p.muted),
                      ),
                    ] else if (!busy)
                      BigButton(key: const Key('update:check'), label: 'Check now', primary: false, busy: _checking, onPressed: _check),
                    if (isAndroidApp && u.apkUrl != null && appVersion != 'dev' && appVersion != u.latest) ...[
                      const SizedBox(height: 12),
                      BigButton(
                        key: const Key('update:apk'),
                        label: 'Download the ${u.latest} Android app',
                        primary: false,
                        onPressed: () => launchUrl(Uri.parse(u.apkUrl!), mode: LaunchMode.externalApplication),
                      ),
                    ],
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
