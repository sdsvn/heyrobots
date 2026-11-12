import 'package:flutter/material.dart';

import '../api/session.dart';
import '../state.dart';
import 'common.dart';

/// Daily digests of proactive results, newest first.
class DigestScreen extends StatelessWidget {
  const DigestScreen({super.key, required this.state});

  final AppState state;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(title: const Text('Daily digest')),
      body: Loaded<DigestList>(
        load: () => state.api.listDigests(),
        builder: (context, list, reload) {
          if (list.digests.isEmpty) {
            return const ErrorPanel(message: 'No digests yet. Results from proactive work are collected here once a day.');
          }
          return ListView(
            padding: const EdgeInsets.all(16),
            children: [
              for (final d in list.digests)
                Card(
                  child: Padding(
                    padding: const EdgeInsets.all(12),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(ago(d.createdAt), style: theme.textTheme.labelLarge),
                        const SizedBox(height: 8),
                        SelectableText(d.body),
                      ],
                    ),
                  ),
                ),
            ],
          );
        },
      ),
    );
  }
}
