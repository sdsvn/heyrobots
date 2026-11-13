import 'dart:math' as math;
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api/session.dart';
import '../state.dart';
import 'agent_avatar.dart';
import 'agent_profile_screen.dart';
import 'app_screen.dart';
import 'approval_screen.dart';
import 'bit.dart';
import 'chat_kit.dart';
import 'common.dart';
import 'connect_apps_screen.dart';
import 'icons.dart';
import 'plans_tab.dart';
import 'theme.dart';

/// A chat with one agent or with a group of them.
class ChatScreen extends StatefulWidget {
  const ChatScreen({super.key, required this.state, this.threadId, this.agent, this.group, this.draft, this.embedded = false});

  final AppState state;

  /// Shown in the main window beside the chat list, with no way back.
  final bool embedded;

  /// Null until the first message of a new chat is sent.
  final String? threadId;
  final AgentRef? agent;
  final Group? group;

  /// Text to start the composer with, such as "Give you a job: ".
  final String? draft;

  @override
  State<ChatScreen> createState() => _ChatScreenState();
}

enum _Tick { sent, takenOn, seen }

/// One thing drawn in the chat: my bubble, their bubble (a reply with any
/// cards that follow it), an action to approve, or a note in the middle.
class _Entry {
  _Entry.mine(this.item) : kind = 'mine', agent = null, parts = const [];
  _Entry.theirs(this.agent, ThreadItem first) : kind = 'theirs', item = first, parts = [first];
  _Entry.action(this.item, this.agent) : kind = 'action', parts = const [];
  _Entry.note(this.item, this.agent) : kind = 'note', parts = const [];

  final String kind;
  final ThreadItem item;
  final AgentRef? agent;
  final List<ThreadItem> parts;
}

class _ChatScreenState extends State<ChatScreen> {
  late final _input = TextEditingController(text: widget.draft ?? '');
  final _scroll = ScrollController();
  String? _threadId;
  ThreadDetail? _detail;
  Agent? _agent;
  int _jobs = 0;
  String? _error;
  bool _sending = false;
  int _readItems = -1;
  final _hidden = <String>{};
  final _confirmed = <String>{};
  StreamSubscription<String>? _changes;

  /// The agent whose profile shows beside the chat, on a wide screen.
  String? _panelAgent;
  bool _wide = false;

  /// The model this chat uses, and the others its provider offers.
  ChatModel? _model;

  AppState get s => widget.state;

  @override
  void initState() {
    super.initState();
    _threadId = widget.threadId ?? widget.group?.threadId;
    _changes = s.threadChanges.listen((id) {
      if (id == _threadId) _load();
    });
    _input.addListener(() => setState(() {}));
    if (_threadId != null) _load();
    _loadAgent();
    _loadModel();
  }

  @override
  void dispose() {
    _changes?.cancel();
    _input.dispose();
    _scroll.dispose();
    super.dispose();
  }

  AgentRef? get _chatAgent => _detail?.agent ?? widget.agent;
  Group? get _group => _detail?.group ?? widget.group;
  bool get _isGroup => _group != null;

  /// The id of the agent whose model this chat switches: the agent of a
  /// direct chat; none for a group or Bit, which use the default.
  String? get _modelAgent => _isGroup ? null : (widget.agent ?? _detail?.agent)?.id;

  Future<void> _loadModel() async {
    if (!s.isOwner) return;
    try {
      final m = await s.api.getChatModel(agentId: _modelAgent);
      if (mounted) setState(() => _model = m);
    } catch (_) {
      // No picker; the chat works as before.
    }
  }

  Future<void> _pickModel(ChatModel m) async {
    final p = Palette.of(context);
    final picked = await showModalBottomSheet<String>(
      context: context,
      backgroundColor: p.card,
      shape: const RoundedRectangleBorder(borderRadius: BorderRadius.vertical(top: Radius.circular(28))),
      builder: (sheet) => SafeArea(
        child: ConstrainedBox(
          constraints: BoxConstraints(maxHeight: MediaQuery.sizeOf(sheet).height * .7),
          child: ListView(
            shrinkWrap: true,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 20, 20, 4),
                child: SectionLabel(
                  m.scope == ChatModelScope.agent ? 'Model for ${widget.agent?.name ?? _chatAgent?.name ?? 'this agent'}' : 'Model for every agent',
                  size: 13,
                  weight: FontWeight.w800,
                ),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 0, 20, 8),
                child: Text(
                  m.scope == ChatModelScope.agent || _modelAgent != null
                      ? 'Only this agent changes. Others keep theirs.'
                      : 'Agents without a model of their own use this one.',
                  style: TextStyle(fontSize: 13, color: p.muted),
                ),
              ),
              for (final id in m.models)
                ListTile(
                  key: Key('model:$id'),
                  title: Text(id, style: TextStyle(fontWeight: id == m.model ? FontWeight.w900 : FontWeight.w600)),
                  trailing: id == m.model ? Icon(Icons.check_rounded, color: p.accent) : null,
                  onTap: () => Navigator.of(sheet).pop(id),
                ),
              if (m.note != null)
                Padding(
                  padding: const EdgeInsets.fromLTRB(20, 4, 20, 12),
                  child: Text(m.note!, style: TextStyle(fontSize: 13, color: p.muted)),
                ),
              const SizedBox(height: 8),
            ],
          ),
        ),
      ),
    );
    if (picked == null || picked == m.model || !mounted) return;
    try {
      final now = await s.api.setChatModel(
        body: ChatModelUpdate(agentId: _modelAgent, model: picked),
      );
      if (!mounted) return;
      setState(() => _model = now);
      showMessage(context, 'Now using $picked');
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    }
  }

  Widget _modelChip(Palette p, ChatModel m) {
    final choosable = m.models.length > 1;
    final label = m.provider == 'claude_code' ? 'Your agent' : m.model;
    return Tooltip(
      message: choosable ? 'Change the model' : (m.note ?? m.model),
      child: Material(
        color: p.ground,
        shape: const StadiumBorder(),
        child: InkWell(
          key: const Key('model-picker'),
          customBorder: const StadiumBorder(),
          onTap: choosable ? () => _pickModel(m) : null,
          child: Container(
            height: 44,
            constraints: const BoxConstraints(maxWidth: 180),
            padding: const EdgeInsets.symmetric(horizontal: 12),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.auto_awesome_rounded, size: 16, color: p.accent),
                const SizedBox(width: 6),
                Flexible(
                  child: Text(
                    label,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(fontSize: 13, fontWeight: FontWeight.w800, color: p.ink),
                  ),
                ),
                if (choosable) ...[const SizedBox(width: 2), Icon(Icons.expand_more_rounded, size: 18, color: p.muted)],
              ],
            ),
          ),
        ),
      ),
    );
  }

  /// The agent's reach and jobs for the header line. Owner only.
  Future<void> _loadAgent() async {
    final id = widget.agent?.id;
    if (id == null || !s.isOwner) return;
    try {
      final r = await Future.wait([s.api.listAgents(), s.api.listJobs()]);
      if (!mounted) return;
      setState(() {
        _agent = (r[0] as AgentList).agents.where((a) => a.id == id).firstOrNull;
        _jobs = (r[1] as JobList).jobs.where((j) => j.agent == id).length;
      });
    } catch (_) {
      // The header falls back to the agent's name alone.
    }
  }

  Future<void> _load() async {
    final id = _threadId;
    if (id == null) return;
    try {
      final d = await s.api.getThread(threadId: id);
      if (!mounted) return;
      setState(() {
        _detail = d;
        _error = null;
      });
      _toEnd();
      if (d.items.length != _readItems) {
        _readItems = d.items.length;
        try {
          await s.api.markThreadRead(threadId: id);
          unawaited(s.refresh());
        } catch (_) {
          // Unread counts catch up on the next refresh.
        }
      }
    } catch (e) {
      if (mounted) setState(() => _error = describeError(e));
    }
  }

  /// Shows the newest message. The list measures itself lazily, so the end
  /// can move once the rows below are laid out: follow it a few frames.
  void _toEnd([int tries = 5]) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scroll.hasClients) return;
      final end = _scroll.position.maxScrollExtent;
      _scroll.jumpTo(end);
      if (tries > 1 && end != _scroll.position.maxScrollExtent || tries > 3) _toEnd(tries - 1);
    });
  }

  Future<void> _send([String? answer]) async {
    final text = (answer ?? _input.text).trim();
    if (text.isEmpty || _sending) return;
    setState(() => _sending = true);
    try {
      final res = _threadId != null
          ? await s.send(text, threadId: _threadId)
          : await s.send(text, agentId: _group == null ? widget.agent?.id : null, groupId: _group?.id);
      if (answer == null) _input.clear();
      _threadId = res.threadId;
      await _load();
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  bool _mine(ThreadItem it) => it.from == 'app:owner' && s.isOwner || it.from == 'app:delegate:${s.me?.delegateName}';

  TicketView? get _workingTicket => (_detail?.tickets ?? const <TicketView>[]).where((t) => t.open && !t.stopped && t.stage == 'working').lastOrNull;

  bool get _typing => _sending || _workingTicket != null || _awaitingReply || (_detail == null && s.threads.any((t) => t.id == _threadId && t.typing));

  /// My last message has no answer yet: the agent is thinking from the
  /// moment it's sent until its reply lands, with no gap in between.
  bool get _awaitingReply {
    final items = _detail?.items ?? const <ThreadItem>[];
    for (final it in items.reversed) {
      if (it.kind == ThreadItemKind.reply || it.kind == ThreadItemKind.card || it.kind == ThreadItemKind.action) return false;
      if (it.kind == ThreadItemKind.decision && (it.decision == 'refused' || it.decision == 'unavailable' || it.decision == 'stop')) return false;
      if (it.kind == ThreadItemKind.message) return _mine(it) && DateTime.now().difference(it.at.toLocal()) < const Duration(minutes: 10);
    }
    return false;
  }

  AgentRef? get _typingAgent => _workingTicket?.agent ?? _chatAgent ?? _group?.members.firstOrNull;

  ({List<String> choices, AgentRef? agent}) get _choices {
    for (final t in (_detail?.tickets ?? const <TicketView>[]).reversed) {
      if (t.open && !t.stopped && t.stage == 'awaiting_answer' && (t.choices ?? const []).isNotEmpty) {
        return (choices: t.choices!, agent: t.agent ?? _chatAgent);
      }
    }
    return (choices: const [], agent: null);
  }

  List<_Entry> _entries() {
    final d = _detail;
    if (d == null) return const [];
    final byTicket = {
      for (final t in d.tickets)
        if (t.agent != null) t.id: t.agent!,
    };
    AgentRef? who(ThreadItem it) => it.agent ?? byTicket[it.ticketId] ?? d.agent;
    final out = <_Entry>[];
    for (final it in d.items) {
      switch (it.kind) {
        case ThreadItemKind.message:
          out.add(_mine(it) ? _Entry.mine(it) : _Entry.theirs(null, it));
        case ThreadItemKind.reply:
          out.add(_Entry.theirs(who(it), it));
        case ThreadItemKind.card when it.card != null:
          final prev = out.lastOrNull;
          if (prev != null && prev.kind == 'theirs' && prev.agent?.id == who(it)?.id && prev.item.kind != ThreadItemKind.message) {
            prev.parts.add(it);
          } else {
            out.add(_Entry.theirs(who(it), it));
          }
        case ThreadItemKind.action when !_hidden.contains(it.id):
          out.add(_Entry.action(it, who(it)));
        case ThreadItemKind.decision when _decisionNote(it, who(it)) != null:
          out.add(_Entry.note(it, who(it)));
        default:
          break;
      }
    }
    return out;
  }

  String? _decisionNote(ThreadItem it, AgentRef? agent) => switch (it.decision) {
    'accept' || 'attach' => _isGroup && agent != null ? '${agent.name} is leading this task' : null,
    'stop' => 'Stopped the task',
    'refused' => (it.reason ?? '').isEmpty ? "Couldn't take this on" : "Couldn't take this on: ${it.reason}",
    'unavailable' => "Can't think right now; it will try again.",
    'would_accept' => 'Would have started a task (observe mode)',
    _ => null,
  };

  _Tick _tickFor(List<_Entry> entries, int i) {
    if (entries[i].item.status == 'seen') return _Tick.seen;
    var taken = false;
    for (final e in entries.skip(i + 1)) {
      if (e.kind == 'theirs' && e.item.kind != ThreadItemKind.message) return _Tick.seen;
      if (e.kind == 'note') taken = true;
    }
    return taken || _typing ? _Tick.takenOn : _Tick.sent;
  }

  void _push(Widget w) => Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => w));

  Future<void> _openCard(ChatCard c) async {
    final link = c.link ?? '';
    if (link.startsWith('approval:')) {
      final id = link.substring(9);
      await Navigator.of(context).push(
        MaterialPageRoute<void>(
          builder: (_) => ApprovalScreen(state: s, approvalId: id),
        ),
      );
      await s.refresh();
      if (mounted && !s.attention.any((a) => a.approvalId == id)) setState(() => _confirmed.add(id));
    } else if (link == 'plans') {
      _push(Scaffold(body: PlansTab(state: s, pushed: true)));
    } else if (link.startsWith('app:')) {
      _push(AppScreen(state: s, appId: link.substring(4)));
    } else if (c.kind == ChatCardKind.connect) {
      _push(ConnectAppsScreen(state: s));
    } else if (link.isNotEmpty) {
      await Clipboard.setData(ClipboardData(text: link));
      if (mounted) showMessage(context, 'Link copied');
    }
  }

  /// An agent's profile: beside the chat on a wide screen, else its own
  /// screen.
  void _openAgent(String id) {
    if (_wide) {
      setState(() => _panelAgent = _panelAgent == id ? null : id);
    } else {
      _push(AgentProfileScreen(state: s, agentId: id));
    }
  }

  void _profile() {
    final a = _chatAgent;
    if (a != null && !_isGroup) {
      _openAgent(a.id);
      return;
    }
    final g = _group;
    if (g == null) return;
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: Palette.of(context).card,
      shape: const RoundedRectangleBorder(borderRadius: BorderRadius.vertical(top: Radius.circular(28))),
      builder: (sheet) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 20, 20, 8),
              child: SectionLabel('Members · ${g.members.length}', size: 13, weight: FontWeight.w800),
            ),
            for (final m in g.members)
              ListTile(
                leading: AgentAvatar(color: m.color, badge: m.badge, size: 40, name: m.name),
                title: Text(m.name, style: const TextStyle(fontWeight: FontWeight.w900)),
                onTap: s.isOwner
                    ? () {
                        Navigator.of(sheet).pop();
                        _openAgent(m.id);
                      }
                    : null,
              ),
            const SizedBox(height: 12),
          ],
        ),
      ),
    );
  }

  /// Width from which an agent's profile opens beside the chat.
  static const _panelFrom = 1000.0;
  static const _panelWidth = 380.0;

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, box) {
      _wide = s.isOwner && box.maxWidth >= _panelFrom;
      final id = _panelAgent;
      if (!_wide || id == null) return _build(context, box.maxWidth);
      return Row(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Expanded(child: _build(context, box.maxWidth - _panelWidth - 2)),
          Container(width: 2, color: Palette.of(context).line),
          SizedBox(
            width: _panelWidth,
            child: AgentProfileScreen(key: ValueKey('panel:$id'), state: s, agentId: id, onClose: () => setState(() => _panelAgent = null)),
          ),
        ],
      );
    },
  );

  /// The widest the conversation gets; wider screens centre it.
  static const _column = 760.0;

  Widget _build(BuildContext context, double width) {
    final p = Palette.of(context);
    final entries = _entries();
    final choices = _choices;
    final name = _group?.name ?? _chatAgent?.name ?? 'Bit';
    final column = math.min(width, _column);
    final side = math.max(0.0, (width - column) / 2);
    final maxBubble = (column - 28) * .8;
    final children = <Widget>[];
    DateTime? lastDay;
    for (var i = 0; i < entries.length; i++) {
      final e = entries[i];
      final day = DateTime(e.item.at.toLocal().year, e.item.at.toLocal().month, e.item.at.toLocal().day);
      if (lastDay != day) {
        lastDay = day;
        children.add(_DayPill(dayLabel(e.item.at)));
      }
      final prev = i > 0 ? entries[i - 1] : null;
      final next = i + 1 < entries.length ? entries[i + 1] : null;
      bool same(_Entry? o) => o != null && o.kind == 'theirs' && o.agent?.id == e.agent?.id;
      switch (e.kind) {
        case 'mine':
          children.add(
            Padding(
              padding: EdgeInsets.only(top: prev == null || prev.kind == 'mine' ? 0 : 6),
              child: _MyBubble(item: e.item, tick: _tickFor(entries, i), maxWidth: maxBubble),
            ),
          );
        case 'theirs':
          children.add(
            Padding(
              padding: EdgeInsets.only(top: same(prev) || prev == null ? 0 : 6),
              child: _TheirBubble(
                entry: e,
                group: _isGroup,
                showName: _isGroup && !same(prev),
                lastOfRun: !same(next),
                maxWidth: maxBubble,
                cardState: (c) => _cardState(c),
                onCard: _openCard,
              ),
            ),
          );
        case 'action':
          children.add(
            _ActionCard(
              item: e.item,
              owner: s.isOwner,
              onReview: () async {
                await Navigator.of(context).push(
                  MaterialPageRoute<void>(
                    builder: (_) => ApprovalScreen(state: s, approvalId: e.item.id),
                  ),
                );
                await _load();
              },
              onNotNow: () => setState(() => _hidden.add(e.item.id)),
            ),
          );
        case 'note':
          children.add(_Note(text: _decisionNote(e.item, e.agent)!, lead: e.item.decision == 'accept' || e.item.decision == 'attach'));
      }
    }
    if (choices.choices.isNotEmpty && !_sending) {
      final c = choices.agent?.color ?? 'violet';
      children.add(
        Padding(
          padding: const EdgeInsets.fromLTRB(40, 6, 0, 0),
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [for (final ch in choices.choices) _ChoiceChip(label: ch, color: c, onTap: () => _send(ch))],
          ),
        ),
      );
    }
    if (_typing && _threadId != null) {
      children.add(
        Padding(
          padding: const EdgeInsets.only(top: 8),
          child: ThinkingBubble(agent: _typingAgent, working: _workingTicket != null),
        ),
      );
    }

    return Scaffold(
      backgroundColor: p.ground,
      body: Column(
        children: [
          _header(p, name),
          Expanded(
            child: _error != null && _detail == null
                ? ErrorPanel(message: _error!, onRetry: _load)
                : _threadId == null && !_sending
                ? _intro(p, name)
                : _detail == null
                ? const Center(child: Bit(mood: BitMood.thinking, size: 64))
                : RefreshIndicator(
                    onRefresh: _load,
                    child: ListView(
                      controller: _scroll,
                      padding: EdgeInsets.fromLTRB(14 + side, 12, 14 + side, 12),
                      children: [for (final c in children) Padding(padding: const EdgeInsets.only(bottom: 4), child: c)],
                    ),
                  ),
          ),
          _composer(p, name, side),
        ],
      ),
    );
  }

  ({String? action, bool confirmed}) _cardState(ChatCard c) {
    final link = c.link ?? '';
    if (link.startsWith('approval:')) {
      final id = link.substring(9);
      final pending = !_confirmed.contains(id) && (s.attention.any((a) => a.approvalId == id) || (c.action ?? '').isNotEmpty && !s.isOwner);
      return pending ? (action: (c.action ?? '').isEmpty ? 'Confirm' : c.action, confirmed: false) : (action: null, confirmed: true);
    }
    return (action: (c.action ?? '').isEmpty ? null : c.action, confirmed: false);
  }

  Widget _intro(Palette p, String name) {
    final a = _chatAgent;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (_group != null)
              GroupAvatar(members: _group!.members, width: 84, height: 80, face: 56)
            else if (a != null)
              AgentAvatar(color: a.color, badge: a.badge, size: 72, name: a.name)
            else
              const Bit(size: 72),
            const SizedBox(height: 14),
            Text(_group != null ? 'Say hi to the group' : 'Say hi to $name', style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w900)),
            const SizedBox(height: 4),
            Text(
              _group != null
                  ? 'One member picks it up and can pass work to the others.'
                  : 'Ask for anything it can reach. You can stop a task anytime by saying so here.',
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 14, color: p.muted, height: 1.4),
            ),
          ],
        ),
      ),
    );
  }

  Widget _header(Palette p, String name) {
    final a = _chatAgent;
    final g = _group;
    final typingWho = _typingAgent;
    Widget sub;
    if (_typing && _threadId != null) {
      const mint = Color(0xFF2FA585);
      sub = g != null
          ? Row(
              children: [
                const Dots(color: mint),
                const SizedBox(width: 3),
                Flexible(
                  child: Text(
                    '${typingWho?.name ?? 'Someone'} is thinking…',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w800, color: mint),
                  ),
                ),
              ],
            )
          : const Text(
              'thinking…',
              style: TextStyle(fontSize: 13, fontWeight: FontWeight.w800, color: mint),
            );
    } else {
      final line = g != null ? g.members.map((m) => m.name).join(', ') : (_agent == null ? '' : reachLine(_agent!, _jobs));
      sub = line.isEmpty
          ? const SizedBox.shrink()
          : Text(
              line,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(fontSize: 13, color: p.muted),
            );
    }
    return Container(
      decoration: BoxDecoration(
        color: p.card,
        boxShadow: [BoxShadow(color: hairline(p), offset: const Offset(0, 2))],
      ),
      child: SafeArea(
        bottom: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 16, 14, 12),
          child: Row(
            children: [
              if (!widget.embedded) ...[
                SizedBox(
                  width: 40,
                  height: 44,
                  child: IconButton(
                    padding: EdgeInsets.zero,
                    tooltip: 'Back to chats',
                    icon: LineIcon(Glyph.back, size: 24, color: p.ink, stroke: 2.6),
                    onPressed: () => Navigator.of(context).maybePop(),
                  ),
                ),
                const SizedBox(width: 10),
              ],
              if (g != null)
                GroupAvatar(members: g.members, width: 46, height: 42, face: 30, ring: 2)
              else if (a != null)
                AgentAvatar(color: a.color, badge: a.badge, size: 42, name: a.name, mood: _typing ? BitMood.thinking : BitMood.idle)
              else
                const AgentAvatar(color: 'violet', badge: 'star', size: 42, showBadge: false),
              const SizedBox(width: 10),
              Expanded(
                child: Semantics(
                  button: s.isOwner,
                  label: s.isOwner ? 'About $name' : null,
                  child: GestureDetector(
                    key: const Key('header-name'),
                    behavior: HitTestBehavior.opaque,
                    onTap: s.isOwner ? _profile : null,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          name,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w900),
                        ),
                        sub,
                      ],
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 10),
              if (a != null && s.isOwner || g != null)
                SquareButton(
                  key: const Key('profile'),
                  icon: Icon(g != null ? Icons.group_outlined : Icons.person_outline_rounded, size: 22, color: p.ink),
                  label: g != null ? 'Group members' : 'About the ${a!.name}',
                  radius: 14,
                  edge: 0,
                  color: p.ground,
                  onPressed: _profile,
                ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _composer(Palette p, String name, double side) {
    final border = OutlineInputBorder(
      borderRadius: BorderRadius.circular(999),
      borderSide: BorderSide(color: p.line, width: 2),
    );
    final canSend = _input.text.trim().isNotEmpty && !_sending;
    return Container(
      decoration: BoxDecoration(
        color: p.card,
        boxShadow: [BoxShadow(color: hairline(p), offset: const Offset(0, -2))],
      ),
      child: SafeArea(
        top: false,
        minimum: const EdgeInsets.only(bottom: 16),
        child: Padding(
          padding: EdgeInsets.fromLTRB(12 + side, 10, 12 + side, 10),
          child: Row(
            children: [
              if (s.isOwner && _model != null) ...[_modelChip(p, _model!), const SizedBox(width: 8)],
              Expanded(
                child: TextField(
                  key: const Key('message'),
                  controller: _input,
                  minLines: 1,
                  maxLines: 5,
                  maxLength: 8000,
                  textInputAction: TextInputAction.send,
                  onSubmitted: (_) => _send(),
                  style: TextStyle(fontSize: 15, color: p.ink),
                  decoration: InputDecoration(
                    hintText: _isGroup ? 'Message the group…' : 'Message $name…',
                    hintStyle: TextStyle(color: p.muted),
                    counterText: '',
                    filled: true,
                    fillColor: p.ground,
                    isDense: true,
                    contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 13),
                    border: border,
                    enabledBorder: border,
                    focusedBorder: border.copyWith(borderSide: BorderSide(color: p.accent, width: 2)),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              DecoratedBox(
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  boxShadow: [BoxShadow(color: p.accentDeep, offset: const Offset(0, 3))],
                ),
                child: Material(
                  color: canSend ? p.accent : p.accent.withValues(alpha: .85),
                  shape: const CircleBorder(),
                  child: InkWell(
                    key: const Key('send'),
                    customBorder: const CircleBorder(),
                    onTap: _sending ? null : _send,
                    child: const SizedBox(
                      width: 48,
                      height: 48,
                      child: Center(child: LineIcon(Glyph.arrow, size: 22, color: Colors.white, stroke: 2.6)),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _DayPill extends StatelessWidget {
  const _DayPill(this.text);

  final String text;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Padding(
      padding: const EdgeInsets.only(bottom: 6),
      child: Center(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 5),
          decoration: edgeBox(p, radius: 999, edge: 1),
          child: Text(
            text,
            style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: p.muted),
          ),
        ),
      ),
    );
  }
}

class _Note extends StatelessWidget {
  const _Note({required this.text, required this.lead});

  final String text;
  final bool lead;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Center(
        child: lead
            ? Container(
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 5),
                decoration: BoxDecoration(color: p.accentSoft, borderRadius: BorderRadius.circular(999)),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.flag_rounded, size: 14, color: p.accent),
                    const SizedBox(width: 6),
                    Flexible(
                      child: Text(
                        text,
                        style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: p.accent),
                      ),
                    ),
                  ],
                ),
              )
            : Text(
                text,
                textAlign: TextAlign.center,
                style: TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: p.muted),
              ),
      ),
    );
  }
}

class _MyBubble extends StatelessWidget {
  const _MyBubble({required this.item, required this.tick, required this.maxWidth});

  final ThreadItem item;
  final _Tick tick;
  final double maxWidth;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        constraints: BoxConstraints(maxWidth: maxWidth),
        padding: const EdgeInsets.fromLTRB(14, 10, 14, 6),
        decoration: BoxDecoration(
          color: p.accent,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(20),
            topRight: Radius.circular(20),
            bottomLeft: Radius.circular(20),
            bottomRight: Radius.circular(6),
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Align(
              alignment: Alignment.centerLeft,
              child: SelectableText(item.text ?? '', style: const TextStyle(fontSize: 15, height: 1.4, color: Colors.white)),
            ),
            const SizedBox(height: 2),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(hhmm(item.at), style: const TextStyle(fontSize: 11, color: Color(0xFFD9D3FF))),
                const SizedBox(width: 4),
                Ticks(
                  color: tick == _Tick.seen ? Colors.white : const Color(0xFFD9D3FF),
                  double: tick != _Tick.sent,
                  label: switch (tick) {
                    _Tick.seen => 'Seen',
                    _Tick.takenOn => 'Taken on',
                    _Tick.sent => 'Sent',
                  },
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

const _theirRadius = BorderRadius.only(
  topLeft: Radius.circular(20),
  topRight: Radius.circular(20),
  bottomLeft: Radius.circular(6),
  bottomRight: Radius.circular(20),
);

class _Face extends StatelessWidget {
  const _Face(this.agent);

  final AgentRef? agent;

  @override
  Widget build(BuildContext context) {
    final a = agent;
    return a == null || a.id == 'ag_general' && a.name == 'General'
        ? AgentAvatar(color: a?.color ?? 'violet', badge: 'star', size: 32, showBadge: false, name: a?.name)
        : AgentAvatar(color: a.color, badge: a.badge, size: 32, showBadge: false, name: a.name);
  }
}

class _TheirBubble extends StatelessWidget {
  const _TheirBubble({
    required this.entry,
    required this.group,
    required this.showName,
    required this.lastOfRun,
    required this.maxWidth,
    required this.cardState,
    required this.onCard,
  });

  final _Entry entry;
  final bool group, showName, lastOfRun;
  final double maxWidth;
  final ({String? action, bool confirmed}) Function(ChatCard) cardState;
  final void Function(ChatCard) onCard;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final a = entry.agent;
    final from = entry.item.kind == ThreadItemKind.message ? entry.item.from?.split(':').last : null;
    final texts = <Widget>[];
    for (final part in entry.parts) {
      if (part.kind == ThreadItemKind.card) {
        texts.add(_CardTile(card: part.card!, state: cardState(part.card!), onTap: () => onCard(part.card!)));
      } else if ((part.text ?? '').isNotEmpty) {
        final failed = part.status == 'failed' || part.status == 'uncertain';
        texts.add(SelectableText(part.text!, style: TextStyle(fontSize: 15, height: 1.4, color: p.ink)));
        if (failed) {
          texts.add(
            Text(
              part.lastError ?? 'Not delivered',
              style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: Color(0xFFC0392B)),
            ),
          );
        }
        if (part.status == 'held') texts.add(Text('Held for the daily digest', style: TextStyle(fontSize: 12, color: p.muted)));
      }
    }
    final bubble = Container(
      constraints: BoxConstraints(maxWidth: maxWidth),
      padding: EdgeInsets.fromLTRB(14, showName ? 8 : 10, 14, lastOfRun ? 6 : 10),
      decoration: BoxDecoration(
        color: p.card,
        borderRadius: _theirRadius,
        boxShadow: [BoxShadow(color: p.line, offset: const Offset(0, 2))],
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          if (showName && (a != null || from != null))
            Text(
              a?.name ?? from!,
              style: TextStyle(fontSize: 13, fontWeight: FontWeight.w900, color: a == null ? p.muted : agentInk(a.color, p)),
            ),
          for (var i = 0; i < texts.length; i++) ...[if (i > 0) const SizedBox(height: 8), texts[i]],
          if (lastOfRun)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Text(
                hhmm(entry.parts.last.at),
                textAlign: TextAlign.right,
                style: TextStyle(fontSize: 11, color: p.muted),
              ),
            ),
        ],
      ),
    );
    return Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        if (lastOfRun) _Face(a) else const SizedBox(width: 32),
        const SizedBox(width: 8),
        Flexible(child: IntrinsicWidth(child: bubble)),
      ],
    );
  }
}

/// A card an agent shares in the chat: a job it set up, an app to connect.
class _CardTile extends StatelessWidget {
  const _CardTile({required this.card, required this.state, required this.onTap});

  final ChatCard card;
  final ({String? action, bool confirmed}) state;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final c = card;
    final (IconData icon, Color tint, Color ink) = switch (c.kind) {
      ChatCardKind.job => (Icons.bolt_rounded, p.peachSoft, const Color(0xFFE07A45)),
      ChatCardKind.rule => (Icons.verified_user_outlined, p.mintSoft, p.mintInk),
      ChatCardKind.setting => (Icons.tune_rounded, p.accentSoft, p.accent),
      ChatCardKind.email => (Icons.mail_outline_rounded, p.peachSoft, const Color(0xFFE07A45)),
      ChatCardKind.event => (Icons.event_rounded, const Color(0xFFDDEEFB), const Color(0xFF3B8FD9)),
      ChatCardKind.connect => (Icons.tag_rounded, p.accentSoft, p.accent),
      ChatCardKind.pr => (Icons.merge_rounded, p.mintSoft, p.mintInk),
      _ => (Icons.open_in_new_rounded, p.accentSoft, p.accent),
    };
    final link = c.link ?? '';
    final Widget trailing;
    if (state.confirmed) {
      trailing = Text(
        'Confirmed',
        style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: p.muted),
      );
    } else if (state.action != null) {
      trailing = DecoratedBox(
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(999),
          boxShadow: [BoxShadow(color: p.accentDeep, offset: const Offset(0, 2))],
        ),
        child: Material(
          color: p.accent,
          borderRadius: BorderRadius.circular(999),
          child: InkWell(
            key: Key('card-action:${c.title}'),
            borderRadius: BorderRadius.circular(999),
            onTap: onTap,
            child: Container(
              height: 32,
              padding: const EdgeInsets.symmetric(horizontal: 12),
              alignment: Alignment.center,
              child: Text(
                state.action!,
                style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w900, color: Colors.white),
              ),
            ),
          ),
        ),
      );
    } else if (link.isNotEmpty) {
      trailing = Text(
        link == 'plans' ? 'Plans' : 'Open',
        style: TextStyle(fontSize: 12, fontWeight: FontWeight.w900, color: p.accent),
      );
    } else {
      trailing = const SizedBox.shrink();
    }
    return Material(
      color: p.ground,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: BorderSide(color: p.line, width: 1.5),
      ),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        key: Key('card:${c.title}'),
        onTap: link.isEmpty || state.confirmed ? null : onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          child: Row(
            children: [
              Container(
                width: 38,
                height: 38,
                decoration: BoxDecoration(color: tint, borderRadius: BorderRadius.circular(12)),
                child: c.kind == ChatCardKind.connect
                    ? Center(
                        child: Text(
                          '#',
                          style: TextStyle(fontSize: 17, fontWeight: FontWeight.w900, color: ink),
                        ),
                      )
                    : Icon(icon, size: 20, color: ink),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(c.title, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w900)),
                    if (c.subtitle.isNotEmpty) Text(c.subtitle, style: TextStyle(fontSize: 12, color: p.muted)),
                  ],
                ),
              ),
              const SizedBox(width: 12),
              trailing,
            ],
          ),
        ),
      ),
    );
  }
}

/// An outward action proposed in the chat, with Review and Not now.
class _ActionCard extends StatelessWidget {
  const _ActionCard({required this.item, required this.owner, required this.onReview, required this.onNotNow});

  final ThreadItem item;
  final bool owner;
  final VoidCallback onReview, onNotNow;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final awaiting = item.status == 'awaiting_approval';
    final to = (item.recipients ?? '').isEmpty ? '' : ' to ${item.recipients}';
    final what = actionName(item.action ?? 'action');
    final status = switch (item.status) {
      'awaiting_approval' => 'Reaches someone else · needs your OK',
      'sent' => 'Sent',
      'rejected' => 'You declined it',
      'cancelled' => 'Cancelled',
      'failed' => 'Failed: ${item.lastError ?? ''}',
      'uncertain' => 'May or may not have been sent',
      'pending' || 'sending' => 'Approved, sending',
      _ => item.status ?? '',
    };
    return Container(
      margin: const EdgeInsets.fromLTRB(40, 6, 0, 0),
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      decoration: edgeBox(p, radius: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Container(
                width: 36,
                height: 36,
                decoration: BoxDecoration(color: p.peachSoft, borderRadius: BorderRadius.circular(12)),
                child: const Icon(Icons.mail_outline_rounded, size: 20, color: Color(0xFFE07A45)),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      awaiting ? 'Send $what$to?' : '${what[0].toUpperCase()}${what.substring(1)}$to',
                      style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w900),
                    ),
                    Text(status, style: TextStyle(fontSize: 12, color: p.muted)),
                  ],
                ),
              ),
            ],
          ),
          if (awaiting && owner) ...[
            const SizedBox(height: 10),
            Row(
              children: [
                Expanded(
                  child: FilledButton(
                    style: FilledButton.styleFrom(
                      minimumSize: const Size(44, 40),
                      shape: const StadiumBorder(),
                      textStyle: const TextStyle(fontFamily: fontFamily, fontSize: 14, fontWeight: FontWeight.w900),
                    ),
                    onPressed: onReview,
                    child: const Text('Review'),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: OutlinedButton(
                    style: OutlinedButton.styleFrom(
                      minimumSize: const Size(44, 40),
                      shape: const StadiumBorder(),
                      foregroundColor: p.ink,
                      side: BorderSide(color: p.line, width: 2),
                      textStyle: const TextStyle(fontFamily: fontFamily, fontSize: 14, fontWeight: FontWeight.w800),
                    ),
                    onPressed: onNotNow,
                    child: const Text('Not now'),
                  ),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}

class _ChoiceChip extends StatelessWidget {
  const _ChoiceChip({required this.label, required this.color, required this.onTap});

  final String label, color;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final (strong, _) = agentColors(color, p);
    return OutlinedButton(
      key: Key('choice:$label'),
      onPressed: onTap,
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(44, 40),
        padding: const EdgeInsets.symmetric(horizontal: 16),
        shape: const StadiumBorder(),
        side: BorderSide(color: strong, width: 2),
        backgroundColor: p.card,
        foregroundColor: agentInk(color, p),
        textStyle: const TextStyle(fontFamily: fontFamily, fontSize: 14, fontWeight: FontWeight.w800),
      ),
      child: Text(label),
    );
  }
}

/// Bit thinking: the agent's face bobs, looks around and blinks inside a
/// glowing ring, beside "Thinking…" with a light passing over it. It shows
/// from the moment a message is sent until the reply lands.
class ThinkingBubble extends StatefulWidget {
  const ThinkingBubble({super.key, required this.agent, this.working = false});

  final AgentRef? agent;

  /// A task has started: the words say so.
  final bool working;

  @override
  State<ThinkingBubble> createState() => _ThinkingBubbleState();
}

class _ThinkingBubbleState extends State<ThinkingBubble> with SingleTickerProviderStateMixin {
  late final _t = AnimationController(vsync: this, duration: const Duration(milliseconds: 1600))..repeat();

  @override
  void dispose() {
    _t.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final a = widget.agent;
    final (strong, soft) = agentColors(a?.color ?? 'violet', p);
    final still = MediaQuery.of(context).disableAnimations;
    final words = widget.working ? 'Working on it' : 'Thinking';
    return Semantics(
      liveRegion: true,
      label: '${a?.name ?? 'Bit'} is ${widget.working ? 'working' : 'thinking'}',
      child: ExcludeSemantics(
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            SizedBox(
              width: 52,
              height: 52,
              child: AnimatedBuilder(
                animation: _t,
                builder: (context, child) {
                  final v = still ? 0.0 : _t.value;
                  return Stack(
                    alignment: Alignment.center,
                    clipBehavior: Clip.none,
                    children: [
                      Transform.scale(
                        scale: 1 + .35 * v,
                        child: Container(
                          width: 46,
                          height: 46,
                          decoration: BoxDecoration(
                            shape: BoxShape.circle,
                            color: strong.withValues(alpha: .25 * (1 - v)),
                          ),
                        ),
                      ),
                      child!,
                    ],
                  );
                },
                child: Container(
                  width: 46,
                  height: 46,
                  decoration: BoxDecoration(
                    color: soft,
                    shape: BoxShape.circle,
                    border: Border.all(color: strong, width: 2),
                  ),
                  alignment: Alignment.center,
                  child: const Bit(mood: BitMood.thinking, size: 34),
                ),
              ),
            ),
            const SizedBox(width: 10),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
              decoration: BoxDecoration(
                color: p.card,
                borderRadius: _theirRadius,
                boxShadow: [BoxShadow(color: p.line, offset: const Offset(0, 2))],
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  AnimatedBuilder(
                    animation: _t,
                    builder: (context, _) {
                      final text = Text(
                        words,
                        style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w800, color: Colors.white),
                      );
                      if (still) {
                        return Text(
                          words,
                          style: TextStyle(fontSize: 15, fontWeight: FontWeight.w800, color: p.muted),
                        );
                      }
                      final x = -1.5 + 3 * _t.value;
                      return ShaderMask(
                        blendMode: BlendMode.srcIn,
                        shaderCallback: (r) =>
                            LinearGradient(begin: Alignment(x - .6, 0), end: Alignment(x + .6, 0), colors: [p.muted, strong, p.muted]).createShader(r),
                        child: text,
                      );
                    },
                  ),
                  const SizedBox(width: 10),
                  Dots(color: strong, size: 7, gap: 5, bounce: true),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
