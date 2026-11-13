import 'dart:convert';

/// What a pairing QR code holds: the server's addresses and a one-time
/// code. The setup page and the "pair another device" screen both show it as
/// JSON: {"v":1,"server":"https://…","servers":["https://…",…],"code":"ABCDE-FGHJK"}.
/// `servers` lists every address the server can be reached at, best first
/// (its tailnet address, then its network ones); `server` is the first.
class PairingPayload {
  PairingPayload(this.server, this.code, {List<Uri>? servers}) : servers = servers == null || servers.isEmpty ? [server] : servers;

  final Uri server;
  final String code;

  /// Where to try pairing, best first.
  final List<Uri> servers;

  /// Reads a scanned QR code.
  static PairingPayload parse(String raw) {
    Object? decoded;
    try {
      decoded = jsonDecode(raw.trim());
    } on FormatException {
      throw const FormatException("That isn't a heyrobots pairing code.");
    }
    if (decoded is! Map || decoded['v'] != 1 || decoded['server'] is! String || decoded['code'] is! String) {
      throw const FormatException("That isn't a heyrobots pairing code.");
    }
    final servers = <Uri>[];
    for (final s in decoded['servers'] is List ? decoded['servers'] as List : const []) {
      if (s is! String) continue;
      try {
        final u = parseServer(s);
        if (!servers.contains(u)) servers.add(u);
      } on FormatException {
        // An address this device may not use, such as plain http on the internet.
      }
    }
    final first = parseServer(decoded['server'] as String);
    return PairingPayload(servers.firstOrNull ?? first, decoded['code'] as String, servers: servers.isEmpty ? [first] : servers);
  }

  /// The QR code text for this payload.
  String encode() => jsonEncode({'v': 1, 'server': server.toString(), 'servers': [for (final s in servers) s.toString()], 'code': code});
}

/// Checks a server address typed or scanned by the person.
///
/// Plain http is accepted only for addresses on this machine or a private
/// network, such as a home network or Tailscale. Anywhere else, the device
/// token would cross the internet unencrypted.
Uri parseServer(String input) {
  var text = input.trim();
  if (text.isEmpty) {
    throw const FormatException('Enter the server address.');
  }
  if (!text.contains('://')) {
    text = 'https://$text';
  }
  final uri = Uri.tryParse(text);
  if (uri == null || uri.host.isEmpty || (uri.scheme != 'http' && uri.scheme != 'https')) {
    throw const FormatException('Enter an address like https://myserver:7340.');
  }
  if (uri.userInfo.isNotEmpty || uri.hasQuery || uri.hasFragment) {
    throw const FormatException('Enter just the server address.');
  }
  if (uri.scheme == 'http' && !isPrivateHost(uri.host)) {
    throw const FormatException(
      'This address needs https. Plain http is only allowed on your own network.',
    );
  }
  final path = uri.path.endsWith('/') ? uri.path.substring(0, uri.path.length - 1) : uri.path;
  return uri.replace(path: path);
}

/// Reports whether a host is this machine or on a private network.
bool isPrivateHost(String host) {
  var h = host.toLowerCase();
  if (h == 'localhost' || h.endsWith('.local') || h.endsWith('.localhost')) {
    return true;
  }
  if (h.startsWith('[') && h.endsWith(']')) {
    h = h.substring(1, h.length - 1);
  }
  try {
    final b = Uri.parseIPv4Address(h);
    return b[0] == 127 ||
        b[0] == 10 ||
        (b[0] == 172 && b[1] >= 16 && b[1] <= 31) ||
        (b[0] == 192 && b[1] == 168) ||
        (b[0] == 169 && b[1] == 254) ||
        // 100.64.0.0/10, used by Tailscale and other private networks.
        (b[0] == 100 && b[1] >= 64 && b[1] <= 127);
  } on FormatException {
    // Not IPv4.
  }
  try {
    final b = Uri.parseIPv6Address(h);
    final loopback = b.sublist(0, 15).every((x) => x == 0) && b[15] == 1;
    // Unique local fc00::/7 and link-local fe80::/10.
    return loopback || (b[0] & 0xfe) == 0xfc || (b[0] == 0xfe && (b[1] & 0xc0) == 0x80);
  } on FormatException {
    return false;
  }
}

/// Whether a host is this machine. Only such a server may sign the app in
/// from a link without asking: a web page can open heyrobots:// links too,
/// and must never be able to point the app at a server of its own.
bool isLoopbackHost(String host) {
  var h = host.toLowerCase();
  if (h.startsWith('[') && h.endsWith(']')) h = h.substring(1, h.length - 1);
  if (h == 'localhost' || h == '::1') return true;
  try {
    return Uri.parseIPv4Address(h)[0] == 127;
  } on FormatException {
    return false;
  }
}

/// Reads a sign-in link the installer opens on this machine:
/// heyrobots://pair?server=http://127.0.0.1:7340&code=ABCDE-FGHJK. Null
/// when it isn't one, or names a server on another machine.
PairingPayload? localPairingLink(Uri uri) {
  if (uri.scheme != 'heyrobots' || uri.host != 'pair') return null;
  final code = uri.queryParameters['code'] ?? '';
  final server = Uri.tryParse(uri.queryParameters['server'] ?? '');
  if (code.isEmpty || server == null || !isLoopbackHost(server.host) || (server.scheme != 'http' && server.scheme != 'https')) return null;
  return PairingPayload(server.replace(path: ''), code);
}
