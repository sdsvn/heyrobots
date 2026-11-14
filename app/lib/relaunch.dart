import 'dart:io';

/// Whether this is the Mac app, which a server update replaces too.
bool get isMacApp => Platform.isMacOS;

/// Whether this is the Android app, which updates from a download.
bool get isAndroidApp => Platform.isAndroid;

/// Opens a fresh copy of the Mac app from its (new) bundle and quits. It
/// quits only once the copy has started, and otherwise returns false.
Future<bool> relaunch() async {
  // .../heyrobots.app/Contents/MacOS/heyrobots
  final bundle = File(Platform.resolvedExecutable).parent.parent.parent.path;
  try {
    final r = await Process.run('/usr/bin/open', ['-n', bundle]);
    if (r.exitCode != 0) return false;
  } on ProcessException {
    return false;
  }
  exit(0);
}
