import 'dart:async';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:qr_flutter/qr_flutter.dart';

import 'engine.dart';

void main() => runApp(const ChameleonApp());

class ChameleonApp extends StatelessWidget {
  const ChameleonApp({super.key, this.engine});

  /// Tests pass their own; the app makes the real one.
  final CameraEngine? engine;

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
      home: CameraPage(engine: engine),
    );
  }
}

/// The camera first: turn it on and see what it sees, right here. Sharing it
/// with a recorder is a separate, optional step below the picture.
class CameraPage extends StatefulWidget {
  const CameraPage({super.key, this.engine});

  final CameraEngine? engine;

  @override
  State<CameraPage> createState() => _CameraPageState();
}

class _CameraPageState extends State<CameraPage> {
  late final CameraEngine _engine = widget.engine ?? Engine();
  StreamSubscription<Status>? _updates;
  StreamSubscription<Uint8List>? _pictures;
  Status _status = const Status();
  Uint8List? _frame;
  List<String> _cameras = const [];
  String? _chosen;

  @override
  void initState() {
    super.initState();
    _updates = _engine.statuses.listen((status) {
      if (!mounted) return;
      setState(() {
        _status = status;
        // No picture left on screen from a camera that has stopped.
        if (!status.running) _frame = null;
      });
    });
    _pictures = _engine.frames.listen((frame) {
      if (mounted && _status.running) setState(() => _frame = frame);
    });
    _loadCameras();
  }

  @override
  void dispose() {
    // Without this the window keeps listening after it is gone, and the next
    // status from the engine lands on a dead widget.
    _updates?.cancel();
    _pictures?.cancel();
    _engine.stop();
    super.dispose();
  }

  Future<void> _loadCameras() async {
    try {
      final cameras = await _engine.cameras();
      if (!mounted) return;
      setState(() {
        _cameras = cameras;
        _chosen ??= cameras.isEmpty ? null : cameras.first;
      });
    } catch (error) {
      if (!mounted) return;
      setState(() => _status = Status(
          error: error is EngineError ? error.message : 'Could not find the engine: $error'));
    }
  }

  @override
  Widget build(BuildContext context) {
    final running = _status.running;
    return Scaffold(
      body: Center(
        child: SingleChildScrollView(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 720),
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  _Picture(status: _status, frame: _frame),
                  const SizedBox(height: 16),
                  Row(
                    children: [
                      if (_cameras.length > 1)
                        Expanded(
                          child: DropdownButtonFormField<String>(
                            initialValue: _chosen,
                            decoration: const InputDecoration(
                                labelText: 'Camera', border: OutlineInputBorder(), isDense: true),
                            items: [
                              for (final camera in _cameras)
                                DropdownMenuItem(value: camera, child: Text(camera)),
                            ],
                            onChanged: running ? null : (value) => setState(() => _chosen = value),
                          ),
                        )
                      else
                        Expanded(
                          child: Text(
                            _cameras.isEmpty ? '' : _cameras.first,
                            style: Theme.of(context).textTheme.bodyMedium,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                      const SizedBox(width: 12),
                      FilledButton.icon(
                        onPressed: () => running ? _engine.stop() : _engine.start(camera: _chosen),
                        icon: Icon(running ? Icons.videocam_off : Icons.videocam),
                        label: Text(running ? 'Turn off camera' : 'Turn on camera'),
                      ),
                    ],
                  ),
                  if (_status.error.isNotEmpty) ...[
                    const SizedBox(height: 12),
                    Text(_status.error, style: TextStyle(color: Theme.of(context).colorScheme.error)),
                  ],
                  if (_status.notes.isNotEmpty) ...[
                    const SizedBox(height: 12),
                    Text(_status.notes, style: Theme.of(context).textTheme.bodySmall),
                  ],
                  if (running && _status.streamUrl.isNotEmpty) ...[
                    const SizedBox(height: 16),
                    _UseWithRecorder(url: _status.streamUrl),
                  ],
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// What the camera sees, with whether it is live and who else is watching.
class _Picture extends StatelessWidget {
  const _Picture({required this.status, required this.frame});

  final Status status;
  final Uint8List? frame;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).colorScheme.onSurfaceVariant;
    final Widget content;
    if (!status.running) {
      content = Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.videocam_off_outlined, size: 40, color: muted),
          const SizedBox(height: 8),
          Text('Camera is off', style: TextStyle(color: muted)),
        ],
      );
    } else if (frame == null) {
      content = Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const SizedBox.square(dimension: 28, child: CircularProgressIndicator(strokeWidth: 2)),
          const SizedBox(height: 12),
          Text('Starting the camera…', style: TextStyle(color: muted)),
        ],
      );
    } else {
      // gaplessPlayback keeps the last picture up while the next one decodes,
      // so the preview does not flicker at every frame.
      content = Image.memory(frame!, gaplessPlayback: true, fit: BoxFit.contain);
    }

    return ClipRRect(
      borderRadius: BorderRadius.circular(16),
      child: AspectRatio(
        aspectRatio: 16 / 9,
        child: ColoredBox(
          color: Colors.black,
          child: Stack(
            fit: StackFit.expand,
            children: [
              Center(child: content),
              if (status.running)
                Positioned(
                  left: 12,
                  top: 12,
                  child: _Badge(
                    live: true,
                    text: status.viewers > 0
                        ? 'Live · ${status.viewers} other ${status.viewers == 1 ? 'device' : 'devices'} watching'
                        : 'Live',
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Badge extends StatelessWidget {
  const _Badge({required this.live, required this.text});

  final bool live;
  final String text;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        color: Colors.black.withValues(alpha: 0.55),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.circle, size: 8, color: live ? const Color(0xFFC9605C) : Colors.grey),
            const SizedBox(width: 6),
            Text(text, style: const TextStyle(color: Colors.white, fontSize: 12)),
          ],
        ),
      ),
    );
  }
}

/// Optional: the address and code that let a recorder, or any other device,
/// watch this camera. Folded away until someone wants it.
class _UseWithRecorder extends StatelessWidget {
  const _UseWithRecorder({required this.url});

  final String url;

  @override
  Widget build(BuildContext context) {
    return Card(
      clipBehavior: Clip.antiAlias,
      child: ExpansionTile(
        leading: const Icon(Icons.cast_connected),
        title: const Text('Use with a recorder'),
        subtitle: const Text('Optional: watch or record this camera on another device'),
        childrenPadding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        expandedCrossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            'Add this address in Anivar, Frigate, Blue Iris, VLC or any app that '
            'takes RTSP or ONVIF cameras. Many recorders on this network will also '
            'find it by themselves.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
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
    );
  }
}
