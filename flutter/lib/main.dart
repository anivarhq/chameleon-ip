import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:qr_flutter/qr_flutter.dart';

import 'engine.dart';

void main() => runApp(const ChameleonApp());

class ChameleonApp extends StatelessWidget {
  const ChameleonApp({super.key});

  @override
  Widget build(BuildContext context) {
    // Dark only: this window sits on a shelf next to a camera, and a bright
    // panel in a dark room is its own kind of nuisance.
    return MaterialApp(
      title: 'Chameleon IP',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        useMaterial3: true,
        brightness: Brightness.dark,
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFFC9605C),
          brightness: Brightness.dark,
        ),
      ),
      home: const CameraPage(),
    );
  }
}

class CameraPage extends StatefulWidget {
  const CameraPage({super.key});

  @override
  State<CameraPage> createState() => _CameraPageState();
}

class _CameraPageState extends State<CameraPage> {
  final _engine = Engine();
  Status _status = const Status();
  List<String> _cameras = const [];
  String? _chosen;

  @override
  void initState() {
    super.initState();
    _engine.statuses.listen((status) => setState(() => _status = status));
    _loadCameras();
  }

  Future<void> _loadCameras() async {
    try {
      final cameras = await _engine.cameras();
      setState(() {
        _cameras = cameras;
        _chosen ??= cameras.isEmpty ? null : cameras.first;
      });
    } catch (error) {
      setState(() => _status = Status(error: 'Could not find the engine: $error'));
    }
  }

  @override
  Widget build(BuildContext context) {
    final running = _status.running;
    return Scaffold(
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 560),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                _StatusLine(status: _status),
                const SizedBox(height: 24),
                if (_cameras.isNotEmpty)
                  DropdownButtonFormField<String>(
                    initialValue: _chosen,
                    decoration: const InputDecoration(labelText: 'Camera', border: OutlineInputBorder()),
                    items: [
                      for (final camera in _cameras)
                        DropdownMenuItem(value: camera, child: Text(camera)),
                    ],
                    onChanged: running ? null : (value) => setState(() => _chosen = value),
                  ),
                const SizedBox(height: 16),
                if (running && _status.streamUrl.isNotEmpty) _AddToNvr(url: _status.streamUrl),
                if (_status.notes.isNotEmpty) ...[
                  const SizedBox(height: 12),
                  Text(_status.notes, style: Theme.of(context).textTheme.bodySmall),
                ],
                if (_status.error.isNotEmpty) ...[
                  const SizedBox(height: 12),
                  Text(_status.error, style: TextStyle(color: Theme.of(context).colorScheme.error)),
                ],
                const SizedBox(height: 24),
                FilledButton(
                  onPressed: () => running ? _engine.stop() : _engine.start(camera: _chosen),
                  child: Text(running ? 'Stop' : 'Start camera'),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _StatusLine extends StatelessWidget {
  const _StatusLine({required this.status});

  final Status status;

  @override
  Widget build(BuildContext context) {
    final text = !status.running
        ? 'Not streaming'
        : status.viewers > 0
            ? '${status.viewers} watching'
            : 'Waiting for a viewer';
    return Row(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        Icon(Icons.circle,
            size: 10,
            color: status.running
                ? const Color(0xFFC9605C)
                : Theme.of(context).disabledColor),
        const SizedBox(width: 8),
        Text(text, style: Theme.of(context).textTheme.titleMedium),
      ],
    );
  }
}

/// Everything needed to add this camera to an NVR: the address to type, and
/// the same thing as a code to scan from a phone.
class _AddToNvr extends StatelessWidget {
  const _AddToNvr({required this.url});

  final String url;

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('Add this to your NVR', style: Theme.of(context).textTheme.labelLarge),
            const SizedBox(height: 12),
            Center(
              child: QrImageView(
                data: url,
                size: 160,
                backgroundColor: Colors.white,
              ),
            ),
            const SizedBox(height: 12),
            SelectableText(url, style: const TextStyle(fontFamily: 'monospace', fontSize: 12)),
            const SizedBox(height: 8),
            Align(
              alignment: Alignment.centerRight,
              child: TextButton.icon(
                onPressed: () async {
                  await Clipboard.setData(ClipboardData(text: url));
                  if (context.mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(content: Text('Address copied')),
                    );
                  }
                },
                icon: const Icon(Icons.copy, size: 16),
                label: const Text('Copy'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
