import 'dart:convert';

import 'package:http/http.dart' as http;

import 'gen/heyrobots_api.dart';

export 'gen/heyrobots_api.dart';

/// An error answer from the server, with the server's own message.
class ApiError implements Exception {
  ApiError(this.status, this.message);

  final int status;
  final String message;

  /// The device token was revoked or is unknown.
  bool get unauthorized => status == 401;

  @override
  String toString() => message;
}

/// Adds the device token to every request, and turns error answers into
/// [ApiError] with the server's message. The generated client would
/// otherwise drop the message and report only the status code.
class _SessionClient extends http.BaseClient {
  _SessionClient(this._inner, this._token);

  final http.Client _inner;
  final String? _token;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final token = _token;
    if (token != null) {
      request.headers['Authorization'] = 'Bearer $token';
    }
    final response = await _inner.send(request);
    if (response.statusCode < 400) {
      return response;
    }
    final body = await response.stream.bytesToString();
    var message = 'The server answered ${response.statusCode}.';
    try {
      final decoded = jsonDecode(body);
      if (decoded is Map && decoded['error'] is String) {
        message = decoded['error'] as String;
      }
    } on FormatException {
      // Not JSON, for example from a proxy in front of the server.
    }
    throw ApiError(response.statusCode, message);
  }

  @override
  void close() => _inner.close();
}

/// Opens the API of one server, as one device.
ApiClient openApi(Uri server, {String? token, http.Client? inner}) {
  return HeyrobotsApi(
    _SessionClient(inner ?? http.Client(), token),
    baseUrl: server,
  ).api;
}

/// A short message for any error, to show to the person.
String describeError(Object error) {
  if (error is ApiError) return error.message;
  if (error is http.ClientException) {
    return "Can't reach your heyrobots server. Check that it's running and that this device can reach it.";
  }
  return 'Something went wrong: $error';
}
