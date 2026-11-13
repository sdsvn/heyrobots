import 'dart:async';

import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'approval_screen.dart';
import 'agent_avatar.dart';
import 'bit.dart';
import 'chat_screen.dart';
import 'common.dart';
import 'theme.dart';

/// One conversation, or a new one when threadId is null.
class ThreadScreen extends StatefulWidget {
  const ThreadScreen({super.key, required this.state, this.threadId});

  final AppState state;
  final String? threadId;

  @override
  State<ThreadScreen> createState() => _ThreadScreenState();
}

class _ThreadScreenState extends State<ThreadScreen> {
  final _input = TextEditingController();
  final _scroll = ScrollController();
  String? _threadId;
  ThreadDetail? _detail;
  String? _error;
  bool _sending = false;
  StreamSubscription<String>? _changes;

  @override
  void initState() {
    super.initState();
    _threadId = widget.threadId;
    _changes = widget.state.threadChanges.listen((id) {
      if (id == _threadId) _load();
    });
    if (_threadId != null) _load();
  }

  @override
  void dispose() {
    _changes?.cancel();
    _input.dispose();
    _scroll.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final id = _threadId;
    if (id == null) return;
    try {
      final d = await widget.state.api.getThread(threadId: id);
      if (!mounted) return;
      // Chats with an agent or a group have their own screen.
      if (d.agent != null || d.group != null) {
        Navigator.of(context).pushReplacement(MaterialPageRoute<void>(
          builder: (_) => ChatScreen(state: widget.state, threadId: id, agent: d.agent, group: d.group),
        ));
        return;
      }
      setState(() {
        _detail = d;
        _error = null;
      });
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_scroll.hasClients) _scroll.jumpTo(_scroll.position.maxScrollExtent);
      });
    } catch (e) {
      if (mounted) setState(() => _error = describeError(e));
    }
  }

  /// Sends what's typed, or the answer the person tapped.
  Future<void> _send([String? answer]) async {
    final text = (answer ?? _input.text).trim();
    if (text.isEmpty || _sending) return;
    setState(() => _sending = true);
    try {
      final res = await widget.state.send(text, threadId: _threadId);
      if (answer == null) _input.clear();
      _threadId = res.threadId;
      await _load();
    } catch (e) {
      if (mounted) showMessage(context, describeError(e));
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  /// Bit is working when a task here is open and not waiting on anyone.
  bool get _working {
    final d = _detail;
    if (d == null) return _sending;
    return d.tickets.any((t) => t.open && !t.stopped && (t.stage == 'working'));
  }

  /// The answers the person can tap for a task's open question.
  List<String> get _choices {
    for (final t in (_detail?.tickets ?? const <TicketView>[]).reversed) {
      if (t.open && !t.stopped && t.stage == 'awaiting_answer' && (t.choices ?? const []).isNotEmpty) return t.choices!;
    }
    return const [];
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final d = _detail;
    final ticket = d?.tickets.lastOrNull;
    final title = ticket?.scope ?? (_threadId == null ? 'New request' : 'Conversation');
    final items = d?.items.where((it) => _Item.visible(it)).toList() ?? const <ThreadItem>[];
    return Scaffold(
      backgroundColor: p.ground,
      body: Column(
        children: [
          Material(
            color: p.card,
            child: SafeArea(
              bottom: false,
              child: Container(
                decoration: BoxDecoration(border: Border(bottom: BorderSide(color: p.line.withValues(alpha: .6), width: 2))),
                padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
                child: Row(
                  children: [
                    _Square(label: 'Back', icon: Icons.chevron_left_rounded, onPressed: () => Navigator.of(context).maybePop()),
                    const SizedBox(width: 10),
                    if (ticket?.agent != null && ticket!.agent!.id != 'ag_general') ...[
                      AgentAvatar(color: ticket.agent!.color, badge: ticket.agent!.badge, size: 38, name: ticket.agent!.name, seed: ticket.agent!.id,
                          mood: ticket.open && !ticket.stopped ? BitMood.thinking : BitMood.idle),
                      const SizedBox(width: 10),
                    ],
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(title, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w900)),
                          if (ticket != null)
                            Text(
                              [
                                if (ticket.agent != null && ticket.agent!.id != 'ag_general') ticket.agent!.name,
                                if (ticket.stopped) 'Stopped' else if (ticket.open) 'Working on it' else 'Finished',
                                if (ticket.spentUsd != null) '${usd(ticket.spentUsd!)} so far',
                              ].join(' · '),
                              style: TextStyle(fontSize: 12, color: p.muted),
                            ),
                        ],
                      ),
                    ),
                    if (ticket != null && !ticket.stopped) ...[const SizedBox(width: 8), _stagePill(p, ticket)],
                  ],
                ),
              ),
            ),
          ),
          Expanded(
            child: _error != null && d == null
                ? ErrorPanel(message: _error!, onRetry: _load)
                : _threadId == null && !_sending
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.all(32),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Bit(mood: BitMood.idle, size: 96),
                          const SizedBox(height: 16),
                          Text('What can I do for you?', style: Theme.of(context).textTheme.titleLarge),
                          const SizedBox(height: 6),
                          Text('Research, a draft, a plan. You can stop a task anytime by saying so here.',
                              textAlign: TextAlign.center, style: TextStyle(color: p.muted, height: 1.45)),
                        ],
                      ),
                    ),
                  )
                : d == null
                ? const Center(child: Bit(mood: BitMood.thinking, size: 64))
                : RefreshIndicator(
                    onRefresh: _load,
                    child: ListView(
                      controller: _scroll,
                      padding: const EdgeInsets.all(16),
                      children: [
                        for (final it in items)
                          _Item(state: widget.state, item: it, onChanged: _load, agents: {
                            for (final t in _detail?.tickets ?? const <TicketView>[])
                              if (t.agent != null) t.id: t.agent!,
                          }),
                        if (_choices.isNotEmpty && !_sending)
                          Padding(
                            padding: const EdgeInsets.only(left: 42, top: 2, bottom: 6),
                            child: Wrap(
                              spacing: 8,
                              runSpacing: 8,
                              children: [for (final c in _choices) _Choice(label: c, onPressed: () => _send(c))],
                            ),
                          ),
                        if (_working)
                          const Padding(
                            padding: EdgeInsets.only(top: 6),
                            child: Row(
                              crossAxisAlignment: CrossAxisAlignment.end,
                              children: [Bit(mood: BitMood.thinking, size: 34), SizedBox(width: 8), _Bubble.dots()],
                            ),
                          ),
                      ],
                    ),
                  ),
          ),
          Material(
            color: p.card,
            child: Container(
              decoration: BoxDecoration(border: Border(top: BorderSide(color: p.line.withValues(alpha: .6), width: 2))),
              child: SafeArea(
                top: false,
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
                  child: Row(
                    children: [
                      Expanded(
                        child: TextField(
                          key: const Key('message'),
                          controller: _input,
                          minLines: 1,
                          maxLines: 5,
                          maxLength: 8000,
                          textInputAction: TextInputAction.send,
                          onSubmitted: (_) => _send(),
                          style: const TextStyle(fontSize: 15),
                          decoration: InputDecoration(
                            hintText: 'Message Bit…',
                            counterText: '',
                            filled: true,
                            fillColor: p.ground,
                            contentPadding: const EdgeInsets.symmetric(horizontal: 18, vertical: 14),
                            border: OutlineInputBorder(borderRadius: BorderRadius.circular(999), borderSide: BorderSide(color: p.line, width: 2)),
                            enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(999), borderSide: BorderSide(color: p.line, width: 2)),
                            focusedBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(999), borderSide: BorderSide(color: p.accent, width: 2)),
                          ),
                        ),
                      ),
                      const SizedBox(width: 10),
                      DecoratedBox(
                        decoration: BoxDecoration(shape: BoxShape.circle, boxShadow: [BoxShadow(color: p.accentDeep, offset: const Offset(0, 3))]),
                        child: Material(
                          color: p.accent,
                          shape: const CircleBorder(),
                          child: InkWell(
                            key: const Key('send'),
                            customBorder: const CircleBorder(),
                            onTap: _sending ? null : _send,
                            child: const SizedBox(
                              width: 50,
                              height: 50,
                              child: Icon(Icons.arrow_forward_rounded, color: Colors.white, semanticLabel: 'Send'),
                            ),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _stagePill(Palette p, TicketView t) => switch (t.stage) {
    'awaiting_answer' || 'awaiting_approval' || 'blocked' => Pill(stageLabel(t.stage), background: p.mintSoft, foreground: p.mintInk),
    'failed' => Pill('Failed', background: const Color(0xFFFFE3E0), foreground: const Color(0xFF9B2C20)),
    'ready' || 'ended' => Pill('Done', background: p.accentSoft, foreground: p.accent),
    _ => Pill(stageLabel(t.stage), background: p.accentSoft, foreground: p.accent),
  };
}

/// An answer the person can tap. Tapping sends it as their reply, exactly
/// as if they had typed it.
class _Choice extends StatelessWidget {
  const _Choice({required this.label, required this.onPressed});

  final String label;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return OutlinedButton(
      key: Key('choice:$label'),
      onPressed: onPressed,
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(44, 44),
        padding: const EdgeInsets.symmetric(horizontal: 18),
        shape: const StadiumBorder(),
        side: BorderSide(color: p.accent, width: 2),
        backgroundColor: p.card,
        foregroundColor: p.accent,
        textStyle: const TextStyle(fontFamily: fontFamily, fontSize: 14, fontWeight: FontWeight.w800),
      ),
      child: Text(label),
    );
  }
}

class _Square extends StatelessWidget {
  const _Square({required this.label, required this.icon, required this.onPressed});

  final String label;
  final IconData icon;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    return Material(
      color: p.ground,
      borderRadius: BorderRadius.circular(14),
      child: InkWell(
        borderRadius: BorderRadius.circular(14),
        onTap: onPressed,
        child: SizedBox(width: 44, height: 44, child: Icon(icon, color: p.ink, size: 28, semanticLabel: label)),
      ),
    );
  }
}

class _Item extends StatelessWidget {
  const _Item({required this.state, required this.item, required this.onChanged, this.agents = const {}});

  final AppState state;
  final ThreadItem item;
  final VoidCallback onChanged;

  /// Each task's agent, by task id, so a reply shows who wrote it.
  final Map<String, AgentRef> agents;

  static bool visible(ThreadItem it) {
    if (it.kind == ThreadItemKind.decision) return _decision(it.decision ?? '', it.reason ?? '') != null;
    return it.kind == ThreadItemKind.message || it.kind == ThreadItemKind.reply || it.kind == ThreadItemKind.action;
  }

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    switch (item.kind) {
      case ThreadItemKind.message:
        final mine = item.from == 'app:owner' && state.isOwner || item.from == 'app:delegate:${state.me?.delegateName}';
        return _Bubble(text: item.text ?? '', at: item.at, mine: mine, label: mine ? null : _from(item.from));
      case ThreadItemKind.reply:
        final failed = item.status == 'failed' || item.status == 'uncertain';
        final agent = agents[item.ticketId];
        return _Bubble(
          text: item.text ?? '',
          at: item.at,
          mine: false,
          bit: true,
          agent: agent,
          label: agent != null && agent.id != 'ag_general' ? agent.name : null,
          note: failed ? (item.lastError ?? 'Not delivered') : (item.status == 'held' ? 'Held for the daily digest' : null),
          warn: failed,
        );
      case ThreadItemKind.decision:
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: Center(
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              decoration: BoxDecoration(color: p.accentSoft, borderRadius: BorderRadius.circular(999)),
              child: Text(_decision(item.decision ?? '', item.reason ?? '')!,
                  textAlign: TextAlign.center, style: TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: p.accent)),
            ),
          ),
        );
      case ThreadItemKind.action:
        return _ActionCard(state: state, item: item, onChanged: onChanged);
      default:
        return const SizedBox.shrink();
    }
  }

  static String _from(String? from) {
    if (from == null) return '';
    final i = from.indexOf(':');
    return i < 0 ? from : from.substring(i + 1);
  }

  static String? _decision(String kind, String reason) => switch (kind) {
    'accept' => 'Started a task',
    'attach' => 'Added to the task',
    'stop' => 'Stopped the task',
    'refused' => reason.isEmpty ? "Couldn't take this on" : "Couldn't take this on: $reason",
    'unavailable' => "Bit can't think right now; it will try again.",
    'would_accept' => 'Would have started a task (observe mode)',
    _ => null,
  };
}

/// A chat bubble. Yours are on the right in the accent color; Bit's are on
/// the left beside a small Bit.
class _Bubble extends StatelessWidget {
  const _Bubble({required this.text, required this.at, required this.mine, this.label, this.note, this.warn = false, this.bit = false, this.agent})
      : _dots = false;

  const _Bubble.dots()
      : text = '',
        at = null,
        mine = false,
        label = null,
        note = null,
        warn = false,
        bit = false,
        agent = null,
        _dots = true;

  final String text;
  final DateTime? at;
  final bool mine;
  final String? label;
  final String? note;
  final bool warn;
  final bool bit;

  /// The agent that wrote it; the General agent shows as plain Bit.
  final AgentRef? agent;
  final bool _dots;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final radius = mine
        ? const BorderRadius.only(topLeft: Radius.circular(22), topRight: Radius.circular(22), bottomLeft: Radius.circular(22), bottomRight: Radius.circular(6))
        : const BorderRadius.only(topLeft: Radius.circular(22), topRight: Radius.circular(22), bottomLeft: Radius.circular(6), bottomRight: Radius.circular(22));
    final bubble = Container(
      padding: _dots ? const EdgeInsets.symmetric(horizontal: 16, vertical: 16) : const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      decoration: BoxDecoration(
        color: mine ? p.accent : p.card,
        borderRadius: radius,
        boxShadow: mine ? null : [BoxShadow(color: p.line, offset: const Offset(0, 2))],
      ),
      child: _dots
          ? const TypingDots()
          : Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (label != null && label!.isNotEmpty)
                  Text(label!, style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: p.muted)),
                SelectableText(text, style: TextStyle(fontSize: 15, height: 1.45, color: mine ? Colors.white : p.ink)),
              ],
            ),
    );
    if (_dots) return bubble;
    final time = Padding(
      padding: EdgeInsets.only(top: 4, left: mine ? 0 : (bit ? 42 : 0)),
      child: Text(
        note == null ? clock(at!) : '${clock(at!)} · $note',
        style: TextStyle(fontSize: 11, color: warn ? const Color(0xFFC0392B) : p.muted, fontWeight: warn ? FontWeight.w700 : FontWeight.w400),
      ),
    );
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Column(
        crossAxisAlignment: mine ? CrossAxisAlignment.end : CrossAxisAlignment.start,
        children: [
          Row(
            mainAxisAlignment: mine ? MainAxisAlignment.end : MainAxisAlignment.start,
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              if (bit) ...[
                if (agent != null && agent!.id != 'ag_general')
                  AgentAvatar(color: agent!.color, badge: agent!.badge, size: 34, name: agent!.name, seed: agent!.id)
                else
                  const Bit(size: 34),
                const SizedBox(width: 8),
              ],
              Flexible(child: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 520), child: bubble)),
            ],
          ),
          time,
        ],
      ),
    );
  }
}

class _ActionCard extends StatelessWidget {
  const _ActionCard({required this.state, required this.item, required this.onChanged});

  final AppState state;
  final ThreadItem item;
  final VoidCallback onChanged;

  @override
  Widget build(BuildContext context) {
    final p = Palette.of(context);
    final status = switch ((item.status, item.approval)) {
      ('awaiting_approval', _) => 'Waiting for your approval',
      ('sent', _) => 'Sent',
      ('rejected', _) => 'You declined it',
      ('cancelled', _) => 'Cancelled${item.lastError == null ? '' : ': ${item.lastError}'}',
      ('failed', _) => 'Failed: ${item.lastError ?? ''}',
      ('uncertain', _) => 'May or may not have been sent',
      ('pending' || 'sending', 'approved') => 'Approved, sending',
      _ => item.status ?? '',
    };
    final awaiting = item.status == 'awaiting_approval';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: SoftCard(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.outbox_rounded, color: p.accent, size: 20),
                const SizedBox(width: 8),
                Expanded(child: Text('Proposed ${actionName(item.action ?? 'action')}', style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w800))),
              ],
            ),
            if ((item.recipients ?? '').isNotEmpty) ...[
              const SizedBox(height: 8),
              Pill('To ${item.recipients}', background: p.peachSoft, foreground: p.peachInk),
            ],
            const SizedBox(height: 8),
            Text(item.text ?? '', maxLines: 4, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14, height: 1.45)),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(child: Text(status, style: TextStyle(fontSize: 12, fontWeight: FontWeight.w800, color: p.muted))),
                if (awaiting && state.isOwner)
                  FilledButton(
                    style: FilledButton.styleFrom(minimumSize: const Size(44, 44), padding: const EdgeInsets.symmetric(horizontal: 18)),
                    onPressed: () async {
                      await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => ApprovalScreen(state: state, approvalId: item.id)));
                      onChanged();
                    },
                    child: const Text('Review'),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
