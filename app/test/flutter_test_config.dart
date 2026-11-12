import 'dart:async';

import 'package:heyrobots/ui/bit.dart';

/// Bit and its friends animate forever, which keeps pumpAndSettle from
/// ever settling. Tests see them standing still.
Future<void> testExecutable(FutureOr<void> Function() testMain) async {
  Bit.animate = false;
  await testMain();
}
