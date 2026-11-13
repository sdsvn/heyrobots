import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

/// A small stand-in for heyrobotsd that answers the endpoints the app uses
/// and records what it was sent.
class FakeServer {
  final requests = <http.Request>[];
  String token = 'tok_owner';
  bool revoked = false;
  String pairingCode = 'ABCDE-FGHJK';
  String role = 'owner';

  /// The model chats use, as the chat model picker sees it.
  String chatModel = 'deepseek-chat';

  List<Map<String, Object?>> attention = [];
  List<Map<String, Object?>> approvals = [];
  List<Map<String, Object?>> threads = [];
  final Map<String, Map<String, Object?>> threadDetails = {};

  /// Groups of agents, as the server lists them.
  final groups = <Map<String, Object?>>[];

  String? googleClient;
  Map<String, Object?>? connectBody;
  List<Map<String, Object?>> accounts = [];

  /// Each role's model.
  final modelRoles = <String, Map<String, Object?>>{
    for (final r in ['router', 'reader', 'worker'])
      r: {'role': r, 'provider': 'gemini', 'model': r == 'worker' ? 'gemini-3.5-flash' : 'gemini-3.1-flash-lite', 'has_key': true, 'local': false, 'is_default': true},
  };

  /// The team.
  final agents = <Map<String, Object?>>[
    {
      'id': 'ag_general', 'name': 'General', 'purpose': "Handles anything the other agents don't.", 'instructions': '',
      'toolsets': <String>[], 'any_toolset': true, 'skills': <String>[], 'capacity': 3, 'color': 'violet', 'badge': 'star',
      'builtin': true, 'enabled': true, 'delegates': <String>[], 'rules': <String>[],
      'working': [{'ticket_id': 'tk_1', 'thread_id': 'th_1', 'scope': 'Plan Lisbon'}], 'waiting': 0,
    },
  ];

  /// MCP servers, by name.
  final mcpServers = <String, Map<String, Object?>>{};

  /// Whether the owner picked both brains; false shows Setup after pairing.
  bool brainsChosen = true;

  /// What can think, for Setup.
  final brains = <String, List<Map<String, Object?>>>{
    'work': [
      {'id': 'claude_code', 'name': 'Claude Code on this Mac', 'detail': 'Sign in in a terminal first', 'available': false, 'selected': false, 'needs_key': false, 'provider': 'claude_code', 'model': 'claude'},
      {'id': 'cloud', 'name': 'DeepSeek, Claude, Gemini or GPT', 'detail': 'Does the work from their site · needs a key', 'available': true, 'selected': false, 'needs_key': true, 'provider': 'openai', 'model': 'deepseek-chat', 'base_url': 'https://api.deepseek.com'},
      {'id': 'ollama', 'name': 'A model on this Mac', 'detail': 'Ollama is running · free and private, slower', 'available': true, 'selected': false, 'needs_key': false, 'provider': 'openai', 'model': 'qwen3', 'base_url': 'http://localhost:11434/v1'},
    ],
    'quick': [
      {'id': 'ollama_quick', 'name': 'Ollama · qwen3 on this Mac', 'detail': 'Found it · free', 'available': true, 'selected': true, 'needs_key': false, 'provider': 'openai', 'model': 'qwen3', 'base_url': 'http://localhost:11434/v1'},
    ],
  };

  String policyMode = 'observe';

  /// The app catalogue, by id.
  final apps = <String, Map<String, Object?>>{
    for (final a in [
      ('gmail', 'Gmail', 'email', 'google', 'M', 'rose', true, 'ann@…'),
      ('outlook', 'Outlook', 'email', 'mcp', 'O', 'sky', false, ''),
      ('imessage', 'iMessage', 'messaging', 'local', 'iM', 'mint', false, 'On this Mac'),
      ('slack', 'Slack', 'messaging', 'mcp', '#', 'violet', false, ''),
      ('github', 'GitHub', 'code', 'mcp', 'GH', 'ink', true, 'MCP · 12 tools'),
    ])
      a.$1: {
        'id': a.$1, 'name': a.$2, 'category': a.$3, 'kind': a.$4, 'toolset': a.$4 == 'mcp' ? 'mcp:${a.$1}' : a.$1, 'mark': a.$5, 'tint': a.$6, 'connected': a.$7,
        'accounts': a.$7 && a.$4 == 'google' ? ['ann@gmail.com'] : <String>[], 'status': a.$7 ? 'on' : 'off', 'summary': a.$8,
        'new_tools': 0, 'agents': a.$7 ? [{'id': 'ag_general', 'name': 'General', 'color': 'violet', 'badge': 'star'}] : <Object?>[],
      },
  };

  /// Servers the MCP registry finds for any search, by id.
  final registry = <String, Map<String, Object?>>{
    'registry~com.notion~mcp': {
      'id': 'registry~com.notion~mcp', 'name': 'Notion', 'category': 'other', 'kind': 'mcp', 'toolset': '', 'mark': 'N', 'tint': 'ink',
      'connected': false, 'accounts': <String>[], 'status': 'off', 'summary': 'Official Notion MCP server', 'new_tools': 0, 'agents': <Object?>[],
      'publisher': 'com.notion', 'official': true,
    },
    'registry~io.github.acme~notes': {
      'id': 'registry~io.github.acme~notes', 'name': 'Acme Notes', 'category': 'other', 'kind': 'mcp', 'toolset': '', 'mark': 'AN', 'tint': 'ink',
      'connected': false, 'accounts': <String>[], 'status': 'off', 'summary': 'Your notes', 'new_tools': 0, 'agents': <Object?>[],
      'publisher': 'io.github.acme', 'official': false, 'runs_here': true, 'command': 'npx -y @acme/notes@2.0.0',
    },
  };

  /// Each app's tools, by app id.
  final appTools = <String, List<Map<String, Object?>>>{
    'gmail': [
      {'name': 'read_mail', 'title': 'Read mail and search', 'summary': 'Inbox, labels, attachments', 'mode': 'reads', 'switchable': false},
      {'name': 'send_email', 'title': 'Send and reply', 'summary': 'You see every draft first', 'mode': 'after_ok', 'switchable': false},
      {'name': 'archive', 'title': 'Archive and label', 'summary': 'Tidying only your own inbox', 'mode': 'own', 'switchable': true},
      {'name': 'delete', 'title': 'Delete', 'summary': 'Off. Nothing gets deleted.', 'mode': 'off', 'switchable': true},
    ],
  };

  /// Skills to add.
  final catalog = <Map<String, Object?>>[
    {'source': 'anthropics/skills/release-notes', 'name': 'release-notes', 'description': 'Release notes', 'suggested_for': 'General', 'installed': false},
  ];
  final skills = <Map<String, Object?>>[
    {'name': 'trip-planner', 'description': 'Plan trips.', 'has_scripts': false, 'files': 1, 'used_by': ['General']},
  ];

  /// Bodies the server received for decisions, by approval id.
  final decisions = <String, Map<String, Object?>>{};

  http.Client client() => MockClient(_handle);

  Future<http.Response> _handle(http.Request r) async {
    requests.add(r);
    final path = r.url.path;
    if (path == '/api/v1/pair' && r.method == 'POST') {
      final body = jsonDecode(r.body) as Map<String, Object?>;
      if (body['code'] != pairingCode) return _error(401, 'That pairing code is wrong or expired.');
      return _json(200, {'token': token, 'device': _device(body['device_name'] as String)});
    }
    if (r.headers['Authorization'] != 'Bearer $token' || revoked) {
      return _error(401, 'unknown device token');
    }
    switch ((r.method, path)) {
      case ('GET', '/api/v1/me'):
        return _json(200, _device('Test phone'));
      case ('GET', '/api/v1/attention'):
        return _json(200, {'items': attention});
      case ('GET', '/api/v1/threads'):
        return _json(200, {'threads': threads});
      case ('GET', '/api/v1/digests'):
        return _json(200, {'digests': <Object?>[]});
      case ('GET', '/api/v1/approvals'):
        return _json(200, {'approvals': approvals});
      case ('GET', '/api/v1/accounts'):
        return _json(200, {'accounts': accounts, 'google_client_set': googleClient != null, 'google_redirect_url': 'http://127.0.0.1:7340/oauth/google/callback'});
      case ('PUT', '/api/v1/accounts/google/client'):
        final body = jsonDecode(r.body) as Map<String, Object?>;
        if (!(body['client_id'] as String).endsWith('.apps.googleusercontent.com')) {
          return _error(400, 'enter the client ID of a Desktop app client');
        }
        googleClient = body['client_id'] as String;
        return http.Response('', 204);
      case ('POST', '/api/v1/accounts/google/connect'):
        connectBody = jsonDecode(r.body) as Map<String, Object?>;
        return _json(200, {'auth_url': 'https://accounts.google.com/o/oauth2/v2/auth?state=s1', 'redirect_url': 'http://127.0.0.1:7340/oauth/google/callback'});
      case ('POST', '/api/v1/accounts/google/finish'):
        final pasted = (jsonDecode(r.body) as Map<String, Object?>)['redirect_url'] as String;
        if (!pasted.contains('state=s1') || !pasted.contains('code=')) return _error(400, 'that address has no sign-in code');
        final acc = {
          'provider': 'google', 'email': 'ann@gmail.com', 'assistant_address': 'ann+assistant@gmail.com',
          'status': 'connected', 'can_send': connectBody?['allow_sending'] == true, 'connected_at': '2026-11-11T10:00:00Z',
        };
        accounts = [acc];
        return _json(200, acc);
      case ('POST', '/api/v1/messages'):
        final body = jsonDecode(r.body) as Map<String, Object?>;
        final id = (body['thread_id'] as String?) ?? 'th_new';
        final agent = agents.where((a) => a['id'] == body['agent_id']).firstOrNull;
        final group = groups.where((g) => g['id'] == body['group_id']).firstOrNull;
        final chatAgent = agent != null ? agentRef(agent) : (threadDetails[id]?['agent']);
        final chatGroup = group ?? threadDetails[id]?['group'];
        threadDetails[id] = {
          'id': id,
          'created_by': 'app:owner',
          'agent': ?chatAgent,
          'group': ?chatGroup,
          'tickets': <Object?>[],
          'items': [
            ...?(threadDetails[id]?['items'] as List?),
            {'kind': 'message', 'id': 'tr${requests.length}', 'at': '2026-11-11T10:00:00Z', 'from': 'app:owner', 'text': body['text']},
          ],
        };
        return _json(202, {'trigger_id': 'tr1', 'thread_id': id, 'duplicate': false});
    }
    if (path == '/api/v1/agents' && r.method == 'GET') return _json(200, {'agents': agents});
    if (path == '/api/v1/chat-model' && r.method == 'GET') {
      return _json(200, {'provider': 'openai', 'model': chatModel, 'scope': 'default', 'models': ['deepseek-chat', 'deepseek-reasoner']});
    }
    if (path == '/api/v1/chat-model' && r.method == 'PUT') {
      final body = jsonDecode(r.body) as Map<String, Object?>;
      chatModel = body['model'] as String;
      return _json(200, {'provider': 'openai', 'model': chatModel, 'scope': body['agent_id'] == null ? 'default' : 'agent', 'models': ['deepseek-chat', 'deepseek-reasoner']});
    }
    if (path == '/api/v1/groups' && r.method == 'GET') return _json(200, {'groups': groups});
    if (path == '/api/v1/groups' && r.method == 'POST') {
      final body = jsonDecode(r.body) as Map<String, Object?>;
      final members = [for (final id in body['members'] as List) agentRef(agents.firstWhere((a) => a['id'] == id))];
      final g = {'id': 'gr_${groups.length + 1}', 'name': body['name'], 'members': members};
      groups.add(g);
      return _json(201, g);
    }
    final read = RegExp(r'^/api/v1/threads/([^/]+)/read$').firstMatch(path);
    if (read != null && r.method == 'POST') {
      for (final t in threads) {
        if (t['id'] == read[1]) t['unread'] = 0;
      }
      return http.Response('', 204);
    }
    if (path == '/api/v1/agents' && r.method == 'POST') {
      final body = jsonDecode(r.body) as Map<String, Object?>;
      final a = {...body, 'id': 'ag_${agents.length}', 'builtin': false, 'working': <Object?>[], 'waiting': 0,
        'any_toolset': body['any_toolset'] ?? false, 'instructions': body['instructions'] ?? '', 'delegates': body['delegates'] ?? <String>[], 'rules': body['rules'] ?? <String>[]}
        ..remove('key');
      agents.add(a);
      return _json(201, a);
    }
    if (path == '/api/v1/policy' && r.method == 'GET') {
      return _json(200, {'version': 1, 'mode': policyMode, 'toolsets': ['research', 'writing', 'mcp:notes'], 'delegates': [{'name': 'sam', 'toolsets': ['writing']}], 'rules': <Object?>[]});
    }
    if (path == '/api/v1/policy' && r.method == 'PUT') {
      final body = jsonDecode(r.body) as Map<String, Object?>;
      policyMode = body['mode'] as String;
      return _json(200, {...body, 'version': 2});
    }
    if (path == '/api/v1/devices' && r.method == 'GET') {
      return _json(200, {'devices': [_device('Test phone')]});
    }
    if (path == '/api/v1/usage' && r.method == 'GET') {
      return _json(200, {'spent_usd': 4.2, 'budget_usd': 30, 'since': '2026-11-01T00:00:00Z', 'tickets': <Object?>[]});
    }
    if (path == '/api/v1/brains' && r.method == 'GET') {
      return _json(200, {...brains, 'chosen': brainsChosen});
    }
    if (path == '/api/v1/skills' && r.method == 'GET') {
      return _json(200, {'skills': skills});
    }
    if (path == '/api/v1/skills/catalog' && r.method == 'GET') {
      return _json(200, {'skills': catalog});
    }
    if (path == '/api/v1/skills/add' && r.method == 'POST') {
      final source = (jsonDecode(r.body) as Map<String, Object?>)['source'];
      final c = catalog.where((c) => c['source'] == source).firstOrNull;
      if (c == null) return _error(400, 'no SKILL.md there');
      c['installed'] = true;
      final skill = {'name': c['name'], 'description': c['description'], 'has_scripts': false, 'files': 1, 'used_by': <String>[]};
      skills.add(skill);
      return _json(200, skill);
    }
    if (path == '/api/v1/apps' && r.method == 'GET') {
      final q = r.url.queryParameters['q'];
      return _json(200, {
        'apps': [
          ...apps.values.where((a) => q == null || (a['name'] as String).toLowerCase().contains(q)),
          if (q != null) ...registry.values,
        ],
      });
    }
    final app = RegExp(r'^/api/v1/apps/([^/]+)(/connect|/tools/([^/]+))?$').firstMatch(path);
    if (app != null) {
      final id = app[1]!;
      if (app[2] == '/connect' && r.method == 'POST' && registry.containsKey(id)) {
        final body = r.body.isEmpty ? <String, Object?>{} : jsonDecode(r.body) as Map<String, Object?>;
        final reg = registry[id]!;
        if (reg['runs_here'] == true && body['settings'] == null) {
          return _json(200, {
            'app': reg,
            'needs_settings': [{'name': 'NOTES_TOKEN', 'description': 'Your notes token', 'secret': true, 'required': true}],
          });
        }
        final a = apps[id] = {...reg, 'connected': true, 'status': 'on'};
        return _json(200, {'app': a});
      }
      if (app[2] == '/connect' && r.method == 'POST') {
        final body = r.body.isEmpty ? <String, Object?>{} : jsonDecode(r.body) as Map<String, Object?>;
        final a = apps[id] ?? (apps[id] = {...apps['slack']!, 'id': id, 'name': 'Custom', 'mark': 'C'});
        a['connected'] = true;
        a['status'] = 'on';
        if (body['url'] != null) a['summary'] = body['url'];
        return _json(200, {'app': a});
      }
      if (app[3] != null && r.method == 'PUT') {
        final t = appTools[id]!.firstWhere((t) => t['name'] == app[3]);
        t['mode'] = (jsonDecode(r.body) as Map<String, Object?>)['mode'];
        return _json(200, t);
      }
      final a = apps[id];
      if (a == null) return _error(404, 'no such app');
      if (r.method == 'GET') return _json(200, {'app': a, 'tools': appTools[id] ?? <Object?>[], 'recent': [{'at': '2026-11-11T07:30:00Z', 'text': 'Triaged 14 emails'}]});
      if (r.method == 'DELETE') {
        a['connected'] = false;
        return http.Response('', 204);
      }
    }
    if (path == '/api/v1/jobs' && r.method == 'GET') {
      return _json(200, {'jobs': [{'name': 'brief', 'description': 'Morning brief.', 'agent': 'ag_general', 'rule': 'brief', 'toolsets': <String>[], 'schedule': 'weekdays 08:00', 'trigger': '', 'steps': [{'name': 'draft', 'kind': 'model', 'detail': 'Draft it.'}, {'name': 'check', 'kind': 'ask', 'detail': 'Send it?'}], 'content': '---\nname: brief\n---\n'}], 'problems': <String>[]});
    }
    if (path == '/api/v1/models' && r.method == 'GET') {
      return _json(200, {'roles': modelRoles.values.toList()});
    }
    final setModel = RegExp(r'^/api/v1/models/([a-z]+)$').firstMatch(path);
    if (setModel != null && r.method == 'PUT') {
      final body = jsonDecode(r.body) as Map<String, Object?>;
      if (body['model'] == 'chatty') return _error(400, "the model didn't pass the test: it didn't call a tool");
      final local = (body['base_url'] as String? ?? '').contains('localhost');
      final role = {'role': setModel[1], 'provider': body['provider'], 'model': body['model'], 'base_url': ?body['base_url'],
        'has_key': body['key'] != null, 'local': local, 'is_default': false};
      modelRoles[setModel[1]!] = role;
      return _json(200, role);
    }
    if (path == '/api/v1/mcp-servers' && r.method == 'GET') {
      final all = mcpServers.values.toList();
      return _json(200, {'servers': all, 'to_review': all.fold<int>(0, (n, s) => n + (s['to_review'] as int))});
    }
    final review = RegExp(r'^/api/v1/mcp-servers/([^/]+)/tools/([^/]+)$').firstMatch(path);
    if (review != null && r.method == 'PUT') {
      final s = mcpServers[review[1]]!;
      final body = jsonDecode(r.body) as Map<String, Object?>;
      for (final t in (s['tools'] as List).cast<Map<String, Object?>>()) {
        if (t['name'] == review[2]) {
          t['mode'] = body['mode'];
          t['needs_review'] = false;
          if (body['reach'] != null) t['reach'] = body['reach'];
        }
      }
      s['to_review'] = (s['tools'] as List).cast<Map<String, Object?>>().where((t) => t['needs_review'] == true).length;
      return _json(200, s);
    }
    final mcp = RegExp(r'^/api/v1/mcp-servers/([^/]+)$').firstMatch(path);
    if (mcp != null && r.method == 'PATCH') {
      final s = mcpServers[mcp[1]]!;
      s.addAll((jsonDecode(r.body) as Map<String, Object?>)..remove('purpose'));
      return _json(200, s);
    }
    final thread = RegExp(r'^/api/v1/threads/([^/]+)$').firstMatch(path);
    if (thread != null && r.method == 'DELETE') {
      threads.removeWhere((t) => t['id'] == thread[1]);
      return http.Response('', 204);
    }
    if (thread != null && r.method == 'GET') {
      final d = threadDetails[thread[1]];
      return d == null ? _error(404, 'no such conversation') : _json(200, d);
    }
    final decide = RegExp(r'^/api/v1/approvals/([^/]+)$').firstMatch(path);
    if (decide != null && r.method == 'POST') {
      final body = jsonDecode(r.body) as Map<String, Object?>;
      final a = approvals.where((a) => a['id'] == decide[1]).firstOrNull;
      if (a == null) return _error(404, 'no such action');
      if (body['decision'] == 'approve' && (a['reach'] == 'others') && body['user_verified'] != true) {
        return _error(403, 'needs biometrics');
      }
      decisions[decide[1]!] = body;
      approvals.remove(a);
      attention.removeWhere((x) => x['approval_id'] == a['id']);
      return _json(200, {...a, 'status': body['decision'] == 'approve' ? 'pending' : 'rejected'});
    }
    return _error(404, 'not found: ${r.method} $path');
  }

  Map<String, Object?> _device(String name) => {
    'id': 'dv_1',
    'name': name,
    'role': role,
    'created_at': '2026-11-11T09:00:00Z',
  };

  /// An issue tracker with two tools, neither reviewed yet.
  void addMcpServer() {
    Map<String, Object?> tool(String name, String description, bool readOnly) => {
      'name': name, 'title': '', 'description': description, 'mode': 'unreviewed', 'needs_review': true,
      'changed': false, 'read_only_hint': readOnly, 'destructive_hint': !readOnly,
    };
    mcpServers['tracker'] = {
      'name': 'tracker', 'toolset': 'mcp:tracker', 'purpose': 'Our issue tracker', 'transport': 'stdio',
      'command': ['npx', 'tracker-mcp'], 'trusted': false, 'enabled': false, 'has_secrets': true, 'to_review': 2,
      'created_at': '2026-11-11T09:00:00Z',
      'tools': [tool('search', 'Search issues.', true), tool('create_issue', 'Create an issue.', false)],
    };
  }

  /// A task on th_q asks "Window or aisle?" with answers to tap.
  void addQuestion() {
    attention.add({
      'id': 'question:tk_q:as_1', 'kind': 'question', 'thread_id': 'th_q', 'ticket_id': 'tk_q', 'text': 'Window or aisle?',
      'at': '2026-11-11T10:00:00Z', 'dismissable': false, 'scope': 'Trip to Lisbon', 'choices': ['Window', 'Aisle'],
    });
    threadDetails['th_q'] = {
      'id': 'th_q',
      'created_by': 'app:owner',
      'tickets': [
        {'id': 'tk_q', 'scope': 'Trip to Lisbon', 'open': true, 'stage': 'awaiting_answer', 'stopped': false, 'choices': ['Window', 'Aisle']},
      ],
      'items': [
        {'kind': 'reply', 'id': 'ef_1', 'at': '2026-11-11T10:00:00Z', 'text': 'Found two flights. Window or aisle?', 'status': 'sent'},
      ],
    };
  }

  Map<String, Object?> agentRef(Map<String, Object?> a) => {'id': a['id'], 'name': a['name'], 'color': a['color'], 'badge': a['badge']};

  /// A Coder agent, a typing chat with it, and an unread group chat whose
  /// last line came from Coder. Coder's chat holds a job card.
  void addChats() {
    final coder = <String, Object?>{
      'id': 'ag_coder', 'name': 'Coder', 'purpose': 'Works in your codebase only', 'instructions': '',
      'toolsets': ['code'], 'any_toolset': false, 'skills': <String>[], 'capacity': 1, 'color': 'mint', 'badge': 'code',
      'builtin': false, 'enabled': true, 'delegates': <String>[], 'rules': <String>[], 'working': <Object?>[], 'waiting': 0,
    };
    agents.add(coder);
    final group = {'id': 'gr_1', 'name': 'Launch v2', 'members': [agentRef(coder), agentRef(agents.first)], 'thread_id': 'th_group'};
    groups.add(group);
    threads.addAll([
      {
        'id': 'th_group', 'title': 'Launch v2', 'created_by': 'app:owner', 'updated_at': '2026-11-11T09:41:00Z', 'group': group,
        'last': {'from': 'Coder', 'text': 'Opened PR #214 for LIN-88', 'at': '2026-11-11T09:41:00Z'}, 'unread': 3, 'typing': false,
      },
      {
        'id': 'th_coder', 'title': 'Fix the login redirect', 'created_by': 'app:owner', 'updated_at': '2026-11-11T09:38:00Z', 'agent': agentRef(coder),
        'last': {'from': 'you', 'text': 'Fix the login redirect', 'at': '2026-11-11T09:38:00Z', 'seen': true}, 'unread': 0, 'typing': true,
      },
    ]);
    threadDetails['th_coder'] = {
      'id': 'th_coder',
      'created_by': 'app:owner',
      'agent': agentRef(coder),
      'tickets': [
        {'id': 'tk_c', 'scope': 'Fix the login redirect', 'open': true, 'stage': 'ready', 'stopped': false, 'agent': agentRef(coder)},
      ],
      'items': [
        {'kind': 'message', 'id': 'm1', 'at': '2026-11-11T09:30:00Z', 'from': 'app:owner', 'text': 'Run the tests every night'},
        {'kind': 'reply', 'id': 'r1', 'at': '2026-11-11T09:31:00Z', 'ticket_id': 'tk_c', 'text': 'Done. They run at 02:00.', 'status': 'sent'},
        {
          'kind': 'card', 'id': 'c1', 'at': '2026-11-11T09:31:00Z', 'ticket_id': 'tk_c',
          'card': {'kind': 'job', 'title': 'Nightly test run', 'subtitle': 'Every day 02:00 · first one tonight', 'link': 'plans'},
        },
      ],
    };
  }

  void addApproval({String id = 'ef_7', String reach = 'others'}) {
    approvals.add({
      'id': id,
      'thread_id': 'th1',
      'source': 'mail',
      'action': 'send_email',
      'reach': reach,
      'recipients': ['bob@example.com'],
      'body': 'Hi Bob, Thursday works.',
      'findings': ['recipient bob@example.com: named only in an outside message'],
      'status': 'awaiting_approval',
      'approval': 'awaiting',
      'created_at': '2026-11-11T09:30:00Z',
      'scope': 'answer Ann',
      'requested_by': 'the owner',
    });
    attention.add({
      'id': 'approval:7',
      'kind': 'approval',
      'thread_id': 'th1',
      'approval_id': id,
      'text': 'send_email',
      'at': '2026-11-11T09:30:00Z',
      'dismissable': false,
    });
  }
}

http.Response _json(int status, Object body) =>
    http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

http.Response _error(int status, String message) => _json(status, {'error': message});
